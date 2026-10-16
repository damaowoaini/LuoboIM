import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, lstatSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const web = path.join(repo, "web");
const output = path.join(repo, "ci-output", "web");
const inputPath = path.join(repo, "ci", "web-ci.inputs.json");
const hash = (bytes) => createHash("sha256").update(bytes).digest("hex");
const json = (file) => JSON.parse(readFileSync(file, "utf8"));
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const xml = (value) => String(value).replace(/[<>&"']/g, (c) => ({ "<": "&lt;", ">": "&gt;", "&": "&amp;", '"': "&quot;", "'": "&apos;" }[c]));

export function verifyFile(root, entry) {
  assert(typeof entry.path === "string" && !path.isAbsolute(entry.path) && !entry.path.split(/[\\/]/).some((part) => !part || part === "." || part === ".."), "Unsafe input path");
  let file = root;
  for (const part of entry.path.split("/")) { file = path.join(file, part); assert(!lstatSync(file).isSymbolicLink(), `Symlink input: ${entry.path}`); }
  assert(lstatSync(file).isFile(), `Non-file input: ${entry.path}`);
  const bytes = readFileSync(file);
  assert(bytes.length === entry.bytes && hash(bytes) === entry.sha256, `Input mismatch: ${entry.path}`);
}

export function verifyLinuxBindings(lock) {
  const packages = lock.packages;
  assert(lock.lockfileVersion === 3 && packages, "Expected npm lockfile v3");
  const checked = [];
  for (const [key, value] of Object.entries(packages)) {
    if (value.resolved) assert(new URL(value.resolved).origin === "https://registry.npmjs.org", `Unreviewed registry/source: ${key}`);
    for (const [name, version] of Object.entries(value.optionalDependencies ?? {})) {
      if (!name.includes("linux") || !name.includes("x64")) continue;
      const candidates = Object.entries(packages).filter(([entry]) => entry.endsWith(`node_modules/${name}`));
      assert(candidates.some(([, item]) => item.version === version && item.os?.includes("linux") && item.cpu?.includes("x64")), `Missing locked Linux x64 binding: ${name}@${version}`);
      checked.push({ parent: key, package: name, version });
    }
  }
  return checked;
}

export function validateReport(report, audit, exit, { minimum, exact, before = false, positiveNames = [] }) {
  const tests = audit.tests;
  assert(Array.isArray(tests) && Array.isArray(report.testResults), "Missing complete test reports");
  assert(["passed", "failed"].includes(audit.reason), "Interrupted/incomplete test run");
  assert(audit.unhandledErrors?.length === 0 && audit.suiteErrors?.length === 0 && audit.failedHooks?.length === 0, "Unhandled, collection, or hook errors");
  assert(report.testResults.every((file) => !file.message && file.assertionResults.length > 0), "Module/fixture loading error");
  assert(report.numPendingTests === 0 && report.numTodoTests === 0 && report.numPendingTestSuites === 0, "Skipped or unfinished tests");
  assert(tests.every((test) => ["passed", "failed"].includes(test.state) && test.mode === "run"), "Skipped/todo/only/incomplete test");
  assert(tests.length === report.numTotalTests && report.testResults.reduce((sum, file) => sum + file.assertionResults.length, 0) === tests.length, "Reporter counts differ");
  if (exact !== undefined) assert(tests.length === exact, `Expected exactly ${exact} tests, got ${tests.length}`);
  if (minimum !== undefined) assert(tests.length >= minimum, `Expected at least ${minimum} tests, got ${tests.length}`);
  const failed = tests.filter((test) => test.state === "failed");
  assert(report.numFailedTests === failed.length && report.numPassedTests === tests.length - failed.length, "Pass/fail totals differ");
  if (before) {
    assert(exit === 1 && failed.length > 0, "Frozen before must complete with assertion failures, not arbitrary nonzero/all-green");
    assert(failed.every((test) => test.errors.length > 0 && test.errors.every((error) => error.name === "AssertionError")), "Before has non-assertion errors (fixture/runtime failure)");
    for (const name of positiveNames) assert(tests.filter((test) => test.name === name && test.state === "passed").length === 1, `Before positive control did not pass: ${name}`);
  } else assert(exit === 0 && report.success === true && failed.length === 0, "Required suite failed");
  return { total: tests.length, passed: tests.length - failed.length, failed: failed.length, meaning: before ? "expected_before_assertions_diagnostic_not_product_pass" : "required_suite_passed" };
}

function childEnv(extra = {}) {
  // No deployment/registry credentials are supplied to package/test processes.
  const env = Object.fromEntries(["PATH", "HOME", "LANG", "LC_ALL", "TZ"].filter((key) => process.env[key]).map((key) => [key, process.env[key]]));
  return { ...env, CI: "true", NODE_OPTIONS: "--max-old-space-size=2048", TMPDIR: path.join(output, "tmp"),
    npm_config_cache: path.join(output, "npm-cache"), npm_config_userconfig: "/dev/null",
    ELECTRON_SKIP_BINARY_DOWNLOAD: "1", PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD: "1", ...extra };
}

async function execute(name, command, args, { timeout = 180000, env = {} } = {}) {
  const log = path.join(output, `${name}.log`), chunks = [];
  // Preserve the file even if spawning fails; never reuse an old run's report.
  writeFileSync(log, "", { flag: "wx" });
  return await new Promise((resolve) => {
    let child, killTimer, settled = false, bytes = 0, timedOut = false, capped = false;
    const finish = (code, signal, error) => {
      if (settled) return; settled = true; clearTimeout(timer); clearTimeout(killTimer);
      writeFileSync(log, Buffer.concat(chunks));
      const result = { name, command: path.basename(command), args, exit: code, signal, timedOut, outputCapped: capped, error: error?.message ?? null };
      writeFileSync(path.join(output, `${name}.process.json`), JSON.stringify(result, null, 2) + "\n", { flag: "wx" }); resolve(result);
    };
    const stop = () => { try { process.kill(-child.pid, "SIGTERM"); } catch {} killTimer = setTimeout(() => { try { process.kill(-child.pid, "SIGKILL"); } catch {} }, 5000); };
    const capture = (chunk) => {
      bytes += chunk.length;
      if (bytes > 20 * 1024 * 1024) { if (!capped) { capped = true; stop(); } return; }
      chunks.push(chunk); process.stdout.write(chunk);
    };
    const timer = setTimeout(() => { timedOut = true; stop(); }, timeout);
    try {
      child = spawn(command, args, { cwd: web, env: childEnv(env), detached: true, stdio: ["ignore", "pipe", "pipe"] });
      child.stdout.on("data", capture); child.stderr.on("data", capture);
      child.once("error", (error) => finish(null, null, error)); child.once("close", (code, signal) => finish(code, signal));
    } catch (error) { finish(null, null, error); }
  });
}

function artifactManifest(directory) {
  const rows = [];
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const file = path.join(directory, entry.name);
    assert(!entry.isSymbolicLink(), "Symlink in build output");
    if (entry.isDirectory()) rows.push(...artifactManifest(file));
    else { const bytes = readFileSync(file); rows.push({ path: path.relative(output, file), bytes: bytes.length, sha256: hash(bytes) }); }
  }
  return rows;
}

export function verifyInputs() {
  const inputs = json(inputPath);
  for (const entry of inputs.requiredInputs) verifyFile(repo, entry);
  for (const entry of inputs.candidate.files) verifyFile(repo, entry);
  // The copied before overlay must really be the product baseline, never a
  // silently adopted after. Critical real caller dependencies are also pinned.
  for (const entry of inputs.candidate.beforeCurrent) verifyFile(repo, entry);
  for (const entry of inputs.candidate.realCallers) verifyFile(repo, entry);
  return { inputs, linuxBindings: verifyLinuxBindings(json(path.join(web, "package-lock.json"))) };
}

async function main() {
  if (process.argv[2] === "verify-inputs") { const verified = verifyInputs(); console.log(JSON.stringify({ verified: true, candidateFiles: verified.inputs.candidate.files.length, linuxBindings: verified.linuxBindings })); return; }
  assert(process.argv.length === 2, "Use no arguments, or verify-inputs");
  assert(process.version === "v24.21.0", `Pinned Node 24.21.0 required, got ${process.version}`);
  assert(!existsSync(output), "Fresh ci-output/web required; old evidence will not be overwritten");
  mkdirSync(output, { recursive: true }); mkdirSync(path.join(output, "tmp"));
  const stages = [];
  const phase = async (name, action) => {
    try { const detail = await action(); stages.push({ name, passed: true, detail }); return true; }
    catch (error) { stages.push({ name, passed: false, error: error.message }); console.error(`${name}: ${error.message}`); return false; }
  };
  const checkedProcess = (result) => { assert(result.exit === 0 && !result.signal && !result.timedOut && !result.outputCapped && !result.error, `${result.name} child failed`); return result; };
  const verified = await phase("input-integrity", async () => verifyInputs());
  const installed = verified && await phase("locked-install", async () => checkedProcess(await execute("locked-install", "npm", ["ci", "--ignore-scripts", "--include=dev", "--no-audit", "--no-fund", "--registry=https://registry.npmjs.org"], { timeout: 600000 })));
  const prepared = installed && await phase("reviewed-sdk-prepare", async () => checkedProcess(await execute("reviewed-sdk-prepare", process.execPath, ["tool/prepare-sdk.cjs"])));
  if (prepared) {
    const runTests = async (name, options, extraArgs = [], env = {}) => {
      const file = path.join(output, name);
      const result = await execute(name, process.execPath, ["node_modules/vitest/vitest.mjs", "run", ...extraArgs,
        "--maxWorkers=1", "--no-file-parallelism", "--no-fsModuleCache", "--no-cache", "--reporter=verbose", "--reporter=json", "--reporter=junit",
        `--reporter=${path.join(repo, "ci/web-ci.audit-reporter.mjs")}`, `--outputFile.json=${file}.json`, `--outputFile.junit=${file}.junit.xml`],
        { env: { ...env, WEB_CI_AUDIT_FILE: `${file}.audit.json` } });
      assert(!result.signal && !result.timedOut && !result.outputCapped && !result.error, `${name} did not finish`);
      return validateReport(json(`${file}.json`), json(`${file}.audit.json`), result.exit, options);
    };
    const inputs = json(inputPath);
    await phase("full-web", () => runTests("full-web", { minimum: inputs.fullMinimumTests }));
    await phase("types-web-strict", async () => checkedProcess(await execute("types-web-strict", process.execPath, ["node_modules/typescript/bin/tsc", "--noEmit"])));
    await phase("types-desktop-original", async () => checkedProcess(await execute("types-desktop-original", process.execPath, ["node_modules/typescript/bin/tsc", "--noEmit", "-p", "tsconfig.desktop.json"])));
    // Independent of the test/type outcomes: a successful preview bundle can be
    // downloaded for review even when another check failed. It is never deployed.
    await phase("web-build", async () => {
      checkedProcess(await execute("web-build", process.execPath, ["node_modules/vite/bin/vite.js", "build", "--config", "vite.web.config.mts", "--outDir", path.join(output, "dist")]));
      const files = artifactManifest(path.join(output, "dist")); assert(files.some((file) => file.path === "dist/index.html"), "Missing built index");
      writeFileSync(path.join(output, "dist-manifest.json"), JSON.stringify(files, null, 2) + "\n", { flag: "wx" }); return { files: files.length };
    });
    for (const stage of ["before", "after"]) await phase(`direct-${stage}`, () => runTests(`direct-${stage}`,
      { exact: inputs.candidate.tests, before: stage === "before", positiveNames: inputs.candidate.positiveNames },
      ["--config", path.join(repo, "ci/web-ci.candidate.config.mts"), "--configLoader", "runner"], { LUOBO_GROUP_DIRECT_EDIT_STAGE: stage }));
    await phase("post-input-integrity", async () => verifyInputs());
  }
  const result = { commit: process.env.GITHUB_SHA ?? null, node: process.version, success: stages.every((stage) => stage.passed) && Boolean(prepared), stages };
  writeFileSync(path.join(output, "summary.json"), JSON.stringify(result, null, 2) + "\n", { flag: "wx" });
  writeFileSync(path.join(output, "stages.junit.xml"), `<?xml version="1.0"?><testsuite name="web-ci-stages" tests="${stages.length}" failures="${stages.filter((stage) => !stage.passed).length}">${stages.map((stage) => `<testcase name="${xml(stage.name)}">${stage.passed ? "" : `<failure message="${xml(stage.error)}"/>`}</testcase>`).join("")}</testsuite>\n`, { flag: "wx" });
  if (!result.success) process.exitCode = 1;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) await main().catch((error) => { console.error(error.message); process.exitCode = 1; });
