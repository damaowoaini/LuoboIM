import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, lstatSync, mkdirSync, readFileSync, readdirSync, renameSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { verifyAdminLinuxBindings, verifyFile, validateReport } from "./admin-ci.helpers.mjs";

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const admin = path.join(repo, "admin"), output = path.join(repo, "ci-output/admin");
const json = (name) => JSON.parse(readFileSync(name, "utf8"));
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const xml = (value) => String(value).replace(/[<>&"']/g, (c) => ({ "<": "&lt;", ">": "&gt;", "&": "&amp;", '"': "&quot;", "'": "&apos;" }[c]));
const save = (name, value) => writeFileSync(path.join(output, name), JSON.stringify(value, null, 2) + "\n", { flag: "wx" });

export function verifyInputs() {
  const input = json(path.join(repo, "ci/admin-ci.inputs.json"));
  for (const entry of input.requiredInputs) verifyFile(repo, entry);
  const pkg = json(path.join(admin, "package.json"));
  assert(pkg.scripts.setup === "max setup" && pkg.scripts["test:core"] === "vitest run" && pkg.scripts.tsc === "tsc --noEmit" && pkg.scripts.build === "max build", "Reviewed Admin commands changed");
  return { input, linuxBindings: verifyAdminLinuxBindings(json(path.join(admin, "package-lock.json"))) };
}

function env(extra) {
  const inherited = Object.fromEntries(["PATH", "HOME", "LANG", "LC_ALL", "TZ"].filter((key) => process.env[key]).map((key) => [key, process.env[key]]));
  return { ...inherited, CI: "true", NODE_OPTIONS: "--max-old-space-size=2048", TMPDIR: path.join(output, "tmp"),
    npm_config_cache: path.join(output, "npm-cache"), npm_config_userconfig: "/dev/null", UMI_TELEMETRY: "0", REACT_APP_ENV: "ci", MOCK: "none",
    ELECTRON_SKIP_BINARY_DOWNLOAD: "1", PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD: "1", ...extra };
}

async function execute(name, command, args, extra = {}, timeout = 600000) {
  const log = path.join(output, `${name}.log`); writeFileSync(log, "", { flag: "wx" });
  return await new Promise((resolve) => {
    const chunks = []; let child, bytes = 0, timedOut = false, capped = false, killTimer, settled = false;
    const stop = () => {
      if (!child?.pid) return;
      try { process.kill(-child.pid, "SIGTERM"); } catch {}
      killTimer = setTimeout(() => { try { process.kill(-child.pid, "SIGKILL"); } catch {} }, 5000);
    };
    const finish = (code, signal, error) => {
      if (settled) return; settled = true; clearTimeout(timer); clearTimeout(killTimer);
      writeFileSync(log, Buffer.concat(chunks));
      const result = { name, command: path.basename(command), args, exit: code, signal, timedOut, outputCapped: capped, error: error?.message ?? null };
      save(`${name}.process.json`, result); resolve(result);
    };
    const capture = (chunk) => {
      bytes += chunk.length;
      if (bytes > 20 * 1024 * 1024) { if (!capped) { capped = true; stop(); } return; }
      chunks.push(chunk); process.stdout.write(chunk);
    };
    const timer = setTimeout(() => { timedOut = true; stop(); }, timeout);
    try {
      child = spawn(command, args, { cwd: admin, env: env(extra), detached: true, stdio: ["ignore", "pipe", "pipe"] });
      child.stdout.on("data", capture); child.stderr.on("data", capture);
      child.once("error", (error) => finish(null, null, error)); child.once("close", (code, signal) => finish(code, signal));
    } catch (error) { finish(null, null, error); }
  });
}

function artifactManifest(directory) {
  const rows = [];
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const file = path.join(directory, entry.name);
    assert(!entry.isSymbolicLink(), "Symlink in Admin build output");
    if (entry.isDirectory()) rows.push(...artifactManifest(file));
    else { assert(entry.isFile(), "Special file in Admin output"); const bytes = readFileSync(file); rows.push({ path: path.relative(output, file), bytes: bytes.length, sha256: createHash("sha256").update(bytes).digest("hex") }); }
  }
  return rows.sort((a, b) => a.path.localeCompare(b.path));
}

async function main() {
  if (process.argv[2] === "verify-inputs") { const data = verifyInputs(); console.log(JSON.stringify({ verified: true, inputFiles: data.input.requiredInputs.length, linuxBindings: data.linuxBindings })); return; }
  assert(process.argv.length === 2, "Use no arguments, or verify-inputs");
  assert(process.version === "v24.21.0", `Pinned Node 24.21.0 required, got ${process.version}`);
  assert(!existsSync(output), "Fresh ci-output/admin required");
  assert(!existsSync(path.join(admin, "dist")) && !existsSync(path.join(admin, "dist-candidate")), "Clean checkout must not contain previous Admin bundles");
  mkdirSync(output, { recursive: true }); mkdirSync(path.join(output, "tmp"));
  const stages = [];
  const phase = async (name, callback) => {
    try { const detail = await callback(); stages.push({ name, passed: true, detail }); return true; }
    catch (error) { stages.push({ name, passed: false, error: error.message }); console.error(`${name}: ${error.message}`); return false; }
  };
  const checked = (result) => { assert(result.exit === 0 && !result.signal && !result.timedOut && !result.outputCapped && !result.error, `${result.name} child failed`); return result; };
  const verified = await phase("input-integrity", async () => verifyInputs());
  const installed = verified && await phase("locked-install", async () => checked(await execute("locked-install", "npm", ["ci", "--ignore-scripts", "--include=dev", "--no-audit", "--no-fund", "--registry=https://registry.npmjs.org"])));
  const generated = installed && await phase("max-setup", async () => {
    checked(await execute("max-setup", process.execPath, ["node_modules/@umijs/max/bin/max.js", "setup"]));
    assert(lstatSync(path.join(admin, "src/.umi/exports.ts")).isFile(), "max setup did not generate the Umi exports");
    return { generated: "admin/src/.umi/exports.ts" };
  });
  if (generated) {
    // These checks are deliberately independent: test/type failures stay red,
    // while a separately successful bundle remains downloadable for review.
    await phase("core-tests", async () => {
      const base = path.join(output, "core-tests");
      const result = await execute("core-tests", process.execPath, ["node_modules/vitest/vitest.mjs", "run", "--maxWorkers=1", "--no-file-parallelism", "--no-fsModuleCache", "--no-cache",
        "--reporter=verbose", "--reporter=json", "--reporter=junit", `--reporter=${path.join(repo, "ci/web-ci.audit-reporter.mjs")}`, `--outputFile.json=${base}.json`, `--outputFile.junit=${base}.junit.xml`], { WEB_CI_AUDIT_FILE: `${base}.audit.json` }, 180000);
      assert(!result.signal && !result.timedOut && !result.outputCapped && !result.error, "Core suite did not finish");
      return validateReport(json(`${base}.json`), json(`${base}.audit.json`), result.exit, { minimum: json(path.join(repo, "ci/admin-ci.inputs.json")).coreMinimumTests });
    });
    await phase("types-admin", async () => checked(await execute("types-admin", process.execPath, ["node_modules/typescript/bin/tsc", "--noEmit"])));
    await phase("max-build", async () => {
      const result = await execute("max-build", process.execPath, ["node_modules/@umijs/max/bin/max.js", "build"]);
      // Preserve partial output too, clearly marked incomplete on build failure.
      if (existsSync(path.join(admin, "dist"))) {
        renameSync(path.join(admin, "dist"), path.join(output, "dist"));
        const files = artifactManifest(path.join(output, "dist"));
        save("dist-manifest.json", { buildExit: result.exit, complete: result.exit === 0 && !result.signal && !result.timedOut && !result.outputCapped && !result.error && files.some((row) => row.path === "dist/index.html"), files });
      }
      checked(result);
      const manifest = json(path.join(output, "dist-manifest.json"));
      assert(manifest.files.some((row) => row.path === "dist/index.html"), "Missing built Admin index");
      return { files: manifest.files.length, deployed: false };
    });
    await phase("post-input-integrity", async () => verifyInputs());
  }
  const result = { commit: process.env.GITHUB_SHA ?? null, node: process.version, success: Boolean(generated) && stages.every((stage) => stage.passed), stages };
  save("summary.json", result);
  writeFileSync(path.join(output, "stages.junit.xml"), `<?xml version="1.0"?><testsuite name="admin-ci-stages" tests="${stages.length}" failures="${stages.filter((stage) => !stage.passed).length}">${stages.map((stage) => `<testcase name="${xml(stage.name)}">${stage.passed ? "" : `<failure message="${xml(stage.error)}"/>`}</testcase>`).join("")}</testsuite>\n`, { flag: "wx" });
  if (!result.success) process.exitCode = 1;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) await main().catch((error) => { console.error(error.message); process.exitCode = 1; });
