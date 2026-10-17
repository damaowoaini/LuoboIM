import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { closeSync, existsSync, fsyncSync, lstatSync, mkdirSync, openSync, readFileSync, readdirSync, realpathSync, statfsSync, writeFileSync } from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { verifyFile, verifyLinuxBindings } from "./web-ci.mjs";

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const web = path.join(repo, "web"), output = path.join(repo, "ci-output/web-build");
const descriptorPath = path.join(repo, "ci/web-build.inputs.json");
const cohort = "web4316-build-only-v3";
const sourceDigest = "245588d7977b71c86d1b33835dfbeef9d42b21a618267769895f1d3645d08780";
const assert = (ok, message) => { if (!ok) throw new Error(message); };
const hash = (body) => createHash("sha256").update(body).digest("hex");
const json = (file) => JSON.parse(readFileSync(file, "utf8"));
const sorted = (values) => values.sort((a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b)));
const rowsDigest = (rows) => hash(Buffer.concat([...rows].sort((a, b) => Buffer.compare(Buffer.from(a.path), Buffer.from(b.path))).map((row) => Buffer.from(`${row.path}\0${row.sha256}\n`))));

function writeNew(file, value) {
  const fd = openSync(file, "wx", 0o600);
  try { writeFileSync(fd, value); fsyncSync(fd); } finally { closeSync(fd); }
  const directory = openSync(path.dirname(file), "r");
  try { fsyncSync(directory); } finally { closeSync(directory); }
}
const writeJSON = (file, value) => writeNew(file, JSON.stringify(value, null, 2) + "\n");

export function validateDescriptor(input) {
  assert(input.schema === "luobo.web.build-only.v1" && input.cohort === cohort && input.node === "24.21.0", "Explicit build-only identity required");
  assert(input.sourceFiles === 4316 && input.files.length === 4316 && new Set(input.files.map((r) => r.path)).size === 4316, "Exact4316 source set required");
  for (const row of input.files) assert(typeof row.path === "string" && !path.isAbsolute(row.path) && !row.path.split(/[\\/]/).some((p) => !p || p === "." || p === "..") && /^[0-9a-f]{64}$/.test(row.sha256) && Number.isSafeInteger(row.bytes) && row.bytes >= 0, "Bounded relative source row required");
  assert(input.sourceDigest === sourceDigest && rowsDigest(input.files) === sourceDigest, "Reviewed4316 source digest differs");
  assert(input.files.filter((r) => r.path.startsWith("public/")).length === 13, "Exact public13 inventory required");
  return input;
}

export function verifySourceTree(directory, rows, installed = false) {
  assert(realpathSync(directory) === path.resolve(directory) && lstatSync(directory).isDirectory(), "Canonical real source root required");
  const names = [], walk = (at) => {
    for (const entry of readdirSync(at, { withFileTypes: true })) {
      const file = path.join(at, entry.name), relative = path.relative(directory, file), info = lstatSync(file);
      assert(!info.isSymbolicLink(), "Source alias refused");
      if (installed && relative === "node_modules") { assert(info.isDirectory(), "Fresh node_modules must be a real directory"); continue; }
      if (info.isDirectory()) walk(file);
      else { assert(info.isFile() && info.nlink === 1, "Single-link regular source required"); names.push(relative); }
    }
  };
  walk(directory);
  assert(JSON.stringify(sorted(names)) === JSON.stringify(sorted(rows.map((r) => r.path))), "Unexpected/missing source, including private environment/config, refused");
  for (const row of rows) { verifyFile(directory, row); assert(lstatSync(path.join(directory, row.path)).nlink === 1, "Source hardlink refused"); }
}

export function buildPlan(node, npm, dist) {
  return [
    { name: "locked-install", command: node, args: [npm, "ci", "--ignore-scripts", "--include=dev", "--no-audit", "--no-fund", "--registry=https://registry.npmjs.org"], timeout: 600000 },
    { name: "reviewed-sdk-prepare", command: node, args: ["tool/prepare-sdk.cjs"], timeout: 180000 },
    { name: "web-build", command: node, args: ["node_modules/vite/bin/vite.js", "build", "--config", "vite.web.config.mts", "--outDir", dist], timeout: 180000 },
  ];
}

function childEnvironment() {
  const env = Object.fromEntries(["HOME", "LANG", "LC_ALL", "TZ"].filter((key) => process.env[key]).map((key) => [key, process.env[key]]));
  return { ...env, PATH: `${path.dirname(process.execPath)}:/usr/bin:/bin`, CI: "true", NODE_OPTIONS: "--max-old-space-size=2048",
    TMPDIR: path.join(output, "tmp"), npm_config_cache: path.join(output, "npm-cache"),
    npm_config_userconfig: path.join(output, "npm-user.rc"), npm_config_globalconfig: path.join(output, "npm-global.rc"),
    ELECTRON_SKIP_BINARY_DOWNLOAD: "1", PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD: "1" };
}

function execute(step, cwd = web) {
  const start = Date.now(), result = spawnSync(step.command, step.args, { cwd, env: childEnvironment(), timeout: step.timeout, maxBuffer: 2 * 1024 ** 2, killSignal: "SIGTERM" });
  writeNew(path.join(output, `${step.name}.stdout.log`), result.stdout ?? Buffer.alloc(0));
  writeNew(path.join(output, `${step.name}.stderr.log`), result.stderr ?? Buffer.alloc(0));
  const record = { ...step, exit: result.status, signal: result.signal, errorCode: result.error?.code ?? null, elapsedMs: Date.now() - start };
  writeJSON(path.join(output, `${step.name}.process.json`), record);
  assert(result.status === 0 && !result.signal && !result.error, `${step.name} failed; exact raw streams retained`);
  return result.stdout.toString("utf8");
}

function checkedInputs() {
  const input = validateDescriptor(json(descriptorPath));
  for (const row of [...input.controllerInputs, ...input.borrowedInputs]) verifyFile(repo, row);
  return input;
}

function verifySDK() {
  const manifest = json(path.join(web, "sdk-patches/manifest.json"));
  const root = path.join(web, "node_modules", manifest.package);
  assert(json(path.join(root, "package.json")).version === manifest.version, "Reviewed SDK version required");
  for (const entry of manifest.files) {
    const file = path.join(root, entry.path);
    assert(realpathSync(file) === file && lstatSync(file).isFile() && lstatSync(file).nlink === 1 && hash(readFileSync(file)) === entry.patched_sha256, "Actual SDK patch output differs");
  }
}

function collectDist(directory, publicRows) {
  assert(realpathSync(directory) === path.resolve(directory) && lstatSync(directory).isDirectory(), "Canonical real dist root required");
  const rows = [], walk = (at) => {
    for (const entry of readdirSync(at, { withFileTypes: true })) {
      const file = path.join(at, entry.name), info = lstatSync(file);
      assert(!info.isSymbolicLink(), "Dist symlink refused");
      if (info.isDirectory()) walk(file);
      else {
        assert(info.isFile() && info.nlink === 1 && !(info.mode & 0o7111) && info.size <= 64 * 1024 ** 2, "Bounded plain dist file required");
        const relative = path.relative(directory, file); assert(!relative.endsWith(".map"), "Source map outside fixed configuration refused");
        rows.push({ path: `dist/${relative}`, bytes: info.size, sha256: hash(readFileSync(file)) });
      }
    }
  };
  walk(directory);
  assert(rows.length > 0 && rows.length <= 256 && rows.reduce((n, r) => n + r.bytes, 0) <= 128 * 1024 ** 2, "Dist collection cap exceeded");
  for (const source of publicRows) {
    const row = rows.find((r) => r.path === `dist/${source.path.slice("public/".length)}`);
    assert(row?.bytes === source.bytes && row?.sha256 === source.sha256, "Pinned public/WASM output changed or missing");
  }
  return rows.sort((a, b) => Buffer.compare(Buffer.from(a.path), Buffer.from(b.path)));
}

function htmlParser() {
  const spec = json(descriptorPath).htmlParser;
  const locked = json(path.join(web, "package-lock.json")).packages["node_modules/parse5"];
  assert(locked.version === spec.version && locked.resolved === spec.resolved && locked.integrity === spec.integrity, "Independent existing npm parser lock must match");
  const require = createRequire(path.join(web, "package.json"));
  const root = realpathSync(path.join(web, "node_modules/parse5"));
  assert(spec.version === "8.0.1" && json(path.join(root, "package.json")).version === spec.version, "Locked real HTML parser required");
  for (const row of spec.files) verifyFile(root, row);
  assert(require.resolve("parse5") === path.join(root, "dist/index.js"), "Parser must resolve from this job's installed package");
  return { parse: require("parse5").parse, identity: { version: spec.version, integrity: spec.integrity, files: spec.files } };
}

function normalizedPolicy(value) {
  assert(typeof value === "string" && Buffer.byteLength(value) <= 8192, "bounded-policy");
  const rows = [], seen = new Set();
  for (const directive of value.split(";")) {
    const fields = directive.replace(/^[\t\n\f\r ]+|[\t\n\f\r ]+$/g, "").split(/[\t\n\f\r ]+/);
    if (fields.length === 1 && fields[0] === "") continue;
    const name = fields.shift().replace(/[A-Z]/g, (c) => c.toLowerCase());
    assert(/^[a-z][a-z0-9-]*$/.test(name) && !seen.has(name), "duplicate-or-invalid-directive");
    assert(new Set(fields).size === fields.length, "duplicate-source-token");
    seen.add(name); rows.push([name, sorted(fields)]);
  }
  return rows.sort((a, b) => a[0].localeCompare(b[0], "en"));
}

export function policyProof(index, expected) {
  const parser = htmlParser(), errors = [], policies = [];
  const document = parser.parse(index, { onParseError: (e) => errors.push(e.code) });
  const walk = (node) => {
    if (node.tagName === "meta" && node.namespaceURI === "http://www.w3.org/1999/xhtml") {
      const attrs = Object.fromEntries(node.attrs.map((a) => [a.name, a.value]));
      if (attrs["http-equiv"]?.replace(/[A-Z]/g, (c) => c.toLowerCase()) === "content-security-policy")
        policies.push({ inHead: node.parentNode?.tagName === "head", content: attrs.content });
    }
    for (const child of node.childNodes ?? []) walk(child);
  };
  walk(document);
  const proof = { passed: false, reason: "policy-count-or-placement", policies: policies.length,
    parser: parser.identity, HTMLParseErrorCodes: [...new Set(errors)], expected: normalizedPolicy(expected) };
  if (policies.length !== 1 || !policies[0].inHead) return proof;
  if (errors.includes("duplicate-attribute")) return { ...proof, reason: "duplicate-HTML-attribute" };
  try { proof.actual = normalizedPolicy(policies[0].content); }
  catch { return { ...proof, reason: "invalid-policy-syntax" }; }
  proof.passed = JSON.stringify(proof.actual) === JSON.stringify(proof.expected);
  proof.reason = proof.passed ? "exact-directive-source-semantics" : "policy-mismatch";
  return proof;
}

export function retainBuildCandidate(directory, dist, publicRows, csp, identity) {
  const files = collectDist(dist, publicRows);
  writeJSON(path.join(directory, "dist-candidate-manifest.json"), files);
  writeJSON(path.join(directory, "dist-candidate-proof.json"), { ...identity,
    state: "built-bytes-policy-and-resource-not-accepted", files: files.length,
    manifestSHA256: hash(readFileSync(path.join(directory, "dist-candidate-manifest.json"))), deployment: false });
  const row = files.find((r) => r.path === "dist/index.html");
  let proof = { passed: false, reason: "missing-or-oversize-index", index: row ?? null, indexCaptureCap: 256 * 1024 };
  if (row && row.bytes <= 256 * 1024) {
    const body = readFileSync(path.join(dist, "index.html"));
    writeNew(path.join(directory, "built-index.html"), body);
    proof = body.length === row.bytes && hash(body) === row.sha256
      ? { ...policyProof(body.toString("utf8"), csp), index: row, capturedIndexSHA256: hash(body) }
      : { ...proof, reason: "index-changed-after-collection", capturedIndexSHA256: hash(body) };
  }
  writeJSON(path.join(directory, "policy-proof.json"), proof);
  return { files, proof };
}

export function distManifest(directory, publicRows, csp) {
  const files = collectDist(directory, publicRows);
  const row = files.find((r) => r.path === "dist/index.html");
  assert(row && row.bytes <= 256 * 1024, "Bounded actual index required");
  assert(policyProof(readFileSync(path.join(directory, "index.html"), "utf8"), csp).passed, "Pinned production CSP semantic mismatch");
  return files;
}

function bundleWorker() {
  assert(process.platform === "linux" && process.getuid() !== 0 && process.version === "v24.21.0", "Fixed ordinary-user Linux Node required");
  const free = statfsSync(repo, { bigint: true }); assert(free.bavail * free.bsize >= 4n * 1024n ** 3n, "Build phase4GiB admission");
  const input = checkedInputs(), prepared = json(path.join(output, "prepared.json"));
  assert(prepared.cohort === cohort && prepared.sourceDigest === sourceDigest && prepared.descriptorSHA256 === hash(readFileSync(descriptorPath)), "Exact prepared activation required");
  const setup = json(path.join(repo, "ci-output/web-build-guard/setup.resource.json"));
  assert(setup.status === "passed" && setup.exit_code === 0, "Successful complete setup resource proof required");
  writeJSON(path.join(output, "bundle-claim.json"), { claimedOnce: true, cohort, sourceDigest });
  const links = execute({ name: "offline-links", command: "/usr/sbin/ip", args: ["-o", "link", "show"], timeout: 5000 }, repo).trim().split("\n");
  assert(links.length === 1 && /: lo:/.test(links[0]), "Build requires independent loopback-only network namespace");
  for (const family of ["-4", "-6"]) assert(execute({ name: `offline-route${family}`, command: "/usr/sbin/ip", args: [family, "route", "show", "default"], timeout: 5000 }, repo).trim() === "", "External build route refused");
  verifySourceTree(web, input.files, true); verifySDK();
  execute(buildPlan(process.execPath, "unused-in-worker", path.join(output, "dist"))[2]);
  verifySourceTree(web, input.files, true); verifySDK(); checkedInputs();
  const candidate = retainBuildCandidate(output, path.join(output, "dist"), input.files.filter((r) => r.path.startsWith("public/")), input.csp,
    { cohort, commit: prepared.commit, sourceFiles: 4316, sourceDigest });
  assert(candidate.proof.passed, "Pinned production CSP semantic mismatch; bounded actual index/proof and candidate retained");
  const files = candidate.files;
  writeJSON(path.join(output, "dist-manifest.json"), files);
  writeJSON(path.join(output, "dist-proof.json"), { cohort, commit: prepared.commit, sourceFiles: 4316, sourceDigest, files: files.length, bytes: files.reduce((n, r) => n + r.bytes, 0), filesDigest: rowsDigest(files), manifestSHA256: hash(readFileSync(path.join(output, "dist-manifest.json"))), opaqueWASMSameAsReviewedPublicSource: true, SDKSourceRebuilt: false, runtimeOrPreviewAcceptance: false, deployment: false, resourceGuardPassStillRequired: true });
  console.log("BUILD_CANDIDATE_READY; outer resource exit/status must also pass");
}

function main() {
  assert(process.argv.length === 3 && process.argv[2] === cohort, "Only the explicit build-only cohort is accepted");
  assert(process.platform === "linux" && process.getuid() !== 0 && process.version === "v24.21.0", "Fixed ordinary-user Linux Node required");
  const free = statfsSync(repo, { bigint: true }); assert(free.bavail * free.bsize >= 8n * 1024n ** 3n, "Cold8GiB admission: zero install/build");
  assert(!existsSync(output), "Fresh build output required; partial resume refused");
  const input = checkedInputs(); verifySourceTree(web, input.files);
  verifyLinuxBindings(json(path.join(web, "package-lock.json")));
  mkdirSync(output, { recursive: true, mode: 0o700 });
  for (const name of ["tmp", "npm-cache"]) mkdirSync(path.join(output, name), { mode: 0o700 });
  for (const name of ["npm-user.rc", "npm-global.rc"]) writeNew(path.join(output, name), "");
  const node = realpathSync(process.execPath), npm = path.resolve(path.dirname(node), "../lib/node_modules/npm/bin/npm-cli.js");
  assert(lstatSync(node).isFile() && lstatSync(npm).isFile(), "Official setup-node runtime and its bundled npm required");
  const plan = buildPlan(node, npm, path.join(output, "dist"));
  try {
    const actualHead = execute({ name: "checkout-head", command: "/usr/bin/git", args: ["rev-parse", "HEAD"], timeout: 5000 }, repo).trim();
    assert(actualHead === process.env.GITHUB_SHA && /^[0-9a-f]{40}$/.test(actualHead), "Actual workflow checkout/head mismatch");
    const npmVersion = execute({ name: "npm-version", command: node, args: [npm, "--version"], timeout: 10000 }, repo).trim();
    assert(/^\d+\.\d+\.\d+$/.test(npmVersion), "Exact bundled npm version query required");
    writeNew(path.join(output, "inputs.json"), readFileSync(descriptorPath));
    writeJSON(path.join(output, "source.json"), { cohort, commit: actualHead, sourceFiles: 4316, sourceDigest, sourceDeclarationSHA256: hash(readFileSync(descriptorPath)), npmVersion,
      node: { version: process.version, bytes: lstatSync(node).size, sha256: hash(readFileSync(node)), bundledNpmCLI_SHA256: hash(readFileSync(npm)), officialActionVersionPin: input.node }, testsRun: 0, typeChecksRun: 0, businessActions: 0, nativeOrSDKBind: 0, HOMEUnchanged: true, sourceIncludesPrivateConfiguration: false });
    execute(plan[0]); execute(plan[1]); verifySDK(); verifySourceTree(web, input.files, true);
    checkedInputs();
    writeJSON(path.join(output, "prepared.json"), { cohort, commit: actualHead, sourceDigest, descriptorSHA256: hash(readFileSync(descriptorPath)), npmLockSHA256: hash(readFileSync(path.join(web, "package-lock.json"))) });
    console.log("LOCKED_SETUP_CANDIDATE_READY; outer resource exit/status must also pass");
  } catch (error) {
    writeJSON(path.join(output, "failure.json"), { cohort, error: error.message, testsRun: 0, typeChecksRun: 0, buildOrInstallEvidenceRetained: true });
    throw error;
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { if (process.argv.length === 3 && process.argv[2] === "--bundle-worker") bundleWorker(); else main(); }
  catch (error) { console.error(error.message); process.exitCode = 1; }
}
