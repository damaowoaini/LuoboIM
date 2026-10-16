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

export function validateReport(report, audit, exit, { minimum, exact, before = false, positiveNames = [], requiredNames = [] }) {
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
  for (const name of requiredNames) assert(tests.filter((test) => test.name === name && test.state === "passed").length === 1, `Required test absent, duplicated, or failed: ${name}`);
  return { total: tests.length, passed: tests.length - failed.length, failed: failed.length, meaning: before ? "expected_before_assertions_diagnostic_not_product_pass" : "required_suite_passed" };
}

export function webCohort(value = "full-regression") {
  assert(["chat-card-42", "individual-forward-32", "full-regression"].includes(value), "Explicit chat-card-42, individual-forward-32 or full-regression cohort required");
  return value;
}

export function individualForwardCohort(individual) {
  assert(individual?.originManifestSHA256 === "e8ef19f09c1c0d38b764e16389333c9f426b50e312dde84e715f109be012b934" && individual.cardOriginManifestSHA256 === "d6404ca1d66bd5c972cbd9369eca309ef92c259478ca3b94204f4d938d41d48a", "Reviewed individual-forward lineage required");
  const productPaths = ["web/src/pages/chat/queryChat/ChatFooter/MultipleActionBar.tsx", "web/src/pages/common/ChooseModal/index.tsx", "web/src/core/individualForward.ts", "web/src/i18n/resources/zh.json", "web/src/i18n/resources/en.json"];
  const testPaths = ["src/core/individualForward/operation.test.ts", "src/core/individualForward/components.test.tsx"];
  const fixturePaths = [...testPaths.map((file) => `web/${file}`), "web/src/core/individualForward/fixture.ts"];
  assert(JSON.stringify(individual.currentAfter?.map((entry) => entry.path)) === JSON.stringify(productPaths) && JSON.stringify(individual.adoptedTests?.map((entry) => entry.path)) === JSON.stringify(fixturePaths), "Exact individual-forward source and fixture paths required");
  assert(JSON.stringify(individual.before?.map((entry) => entry.path)) === JSON.stringify(productPaths.filter((file) => file !== "web/src/core/individualForward.ts")), "Exact individual-forward predecessor paths required");
  assert(JSON.stringify(individual.testPaths) === JSON.stringify(testPaths) && individual.tests === 32 && individual.requiredNames?.length === 32 && new Set(individual.requiredNames).size === 32 && hash(Buffer.from(JSON.stringify(individual.requiredNames))) === "8d541cc496dc3345a5df5b3485edd13e1bb49591fc179d11c295ed9966140a12", "Exact 32 individual-forward names and normal test paths required");
  assert(individual.previousExpectedFullTests === 731 && individual.expectedFullTests === 763, "Explicit expected 731+32 regression lineage required");
  return { exact: individual.tests, requiredNames: individual.requiredNames };
}

export function currentIndividualForwardInput(previous, individual) {
  individualForwardCohort(individual);
  const current = individual.currentAfter.find((entry) => entry.path === previous.path);
  if (!current) return previous;
  const before = individual.before.find((entry) => entry.path === previous.path);
  assert(before?.bytes === previous.bytes && before?.sha256 === previous.sha256, "Individual-forward predecessor differs");
  return current;
}

export function currentChatCardInput(previous, card) {
  assert(card?.originManifestSHA256 === "d6404ca1d66bd5c972cbd9369eca309ef92c259478ca3b94204f4d938d41d48a", "Reviewed chat-card lineage required");
  const current = card.currentAfter.find((entry) => entry.path === previous.path);
  if (!current) return previous;
  const before = card.before.find((entry) => entry.path === previous.path);
  assert(before?.bytes === previous.bytes && before?.sha256 === previous.sha256, "Chat-card predecessor differs");
  return currentCardCopyInput(current, card.copyRetention);
}

export function currentCardCopyInput(previous, copy) {
  const target = "web/src/pages/common/UserCardModal/index.tsx";
  assert(copy?.sourceParentManifestSHA256 === "d6404ca1d66bd5c972cbd9369eca309ef92c259478ca3b94204f4d938d41d48a" && copy.before?.length === 1 && copy.after?.length === 1, "Explicit copy-retention lineage required");
  const before = copy.before[0], after = copy.after[0];
  assert(before.path === target && before.bytes === 18758 && before.sha256 === "844a5c70a6ffe37e06ae01ed544a1fbf60a203ac65a189ba5971bfda94292a8d" && after.path === target, "Exact original card profile predecessor required");
  if (previous.path !== target) return previous;
  assert(previous.bytes === before.bytes && previous.sha256 === before.sha256, "Copy-retention predecessor differs");
  return after;
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

export function verifyStyleCaller(previous, now, style) {
  const same = (a, b) => a?.path === b?.path && a?.bytes === b?.bytes && a?.sha256 === b?.sha256;
  assert(style?.candidateManifestSHA256 === "23e9c373d29b18fe7575d6c5dd21abc12a4cfe0a6749778a15789365f2d34a03", "Reviewed mobile style lineage required");
  assert(style.before.path === "web/src/pages/chat/queryChat/ChatHeader/index.tsx" && style.before.bytes === 11927 && style.before.sha256 === "9663095f6a75cbd9d9a08806f94d489e9e4da951091caba886d6443541e79132", "Exact pre-style caller required");
  assert(style.after.path === style.before.path && style.after.bytes === 11991 && style.after.sha256 === "0fd3a5eb25b79119785755925213e53eca57820dcb6a3b4c3973ade2f9f43bd6", "Exact style successor required");
  assert(same(previous, style.before) && same(now, style.after), "Unrelated caller/SDK pin changed");
  assert(style.frozenBefore.path === "ci/candidates/web-direct-edit/context/ChatHeader-before-mobile-style.tsx" && style.frozenBefore.bytes === previous.bytes && style.frozenBefore.sha256 === previous.sha256, "Frozen caller predecessor differs");
}

export function verifyInputs() {
  const inputs = json(inputPath), direct = inputs.candidate, edit = inputs.editableContentAdoption;
  const card = inputs.chatCardAdoption;
  const copy = card?.copyRetention;
  const individual = inputs.individualForwardAdoption;
  individualForwardCohort(individual);
  assert(card?.originManifestSHA256 === "d6404ca1d66bd5c972cbd9369eca309ef92c259478ca3b94204f4d938d41d48a" && card.before.length === 13 && card.currentAfter.length === 15 && card.adoptedTests.length === 4, "Complete chat-card source/test lineage required");
  assert(card.tests === 37 && card.priorFullPassed === 689 && card.expectedFullTests === 726 && copy?.newTests === 5 && copy.expectedFullTests === 731 && inputs.fullMinimumTests === individual.expectedFullTests, "Explicit pending 689+37+5+32 regression cohort required");
  assert(card.requiredNames.length === 37 && new Set(card.requiredNames).size === 37, "Complete unique chat-card names required");
  assert(copy.requiredNames.length === 5 && new Set([...card.requiredNames, ...copy.requiredNames]).size === 42, "Complete unique copy-retention names required");
  assert(new Set([...card.requiredNames, ...copy.requiredNames, ...individual.requiredNames]).size === 74, "Complete unique 42+32 current names required");
  const cardTestPaths = ["src/core/messageCard/messageCard.test.ts", "src/core/messageCard/receivedCardComponents.test.tsx", "src/core/messageCard/cardShareSource.test.tsx"];
  assert(JSON.stringify(card.testPaths) === JSON.stringify(cardTestPaths), "Exact normal-source card test paths required");
  assert(JSON.stringify(copy.testPaths) === JSON.stringify(["src/core/messageCard/profileCopy.test.tsx"]) && copy.adoptedTest.path === "web/src/core/messageCard/profileCopy.test.tsx", "Exact new copy-retention test path required");
  verifyFile(repo, copy.adoptedTest);
  assert(card.config.path === "web/vitest.config.mts" && card.typeInputs.length === 2, "Normal Web test/type configuration required");
  for (const entry of [card.config, ...card.typeInputs]) verifyFile(repo, entry);
  assert(new Set(card.currentAfter.map((entry) => entry.path)).size === 15 && new Set(card.before.map((entry) => entry.path)).size === 13, "Duplicate chat-card source identity");
  assert(card.before.every((entry) => card.currentAfter.some((after) => after.path === entry.path)), "Missing chat-card source successor");
  const originalPicker = card.currentAfter.find((entry) => entry.path === "web/src/pages/common/ChooseModal/index.tsx");
  assert(originalPicker?.bytes === 18464 && originalPicker?.sha256 === "9d856011a50f95c81b3f841224a79f26c77c923128e70861c24794d65b335b44", "Exact original card picker identity required");
  currentIndividualForwardInput(originalPicker, individual);
  for (const entry of [...individual.currentAfter, ...individual.adoptedTests]) verifyFile(repo, entry);
  for (const entry of card.currentAfter) { assert(entry.path.startsWith("web/src/"), "Unsafe chat-card source path"); verifyFile(repo, currentIndividualForwardInput(currentCardCopyInput(entry, copy), individual)); }
  assert(card.adoptedTests.every((entry) => entry.path.startsWith("web/src/core/messageCard/")), "Unsafe adopted card test path");
  for (const entry of card.adoptedTests) verifyFile(repo, entry);
  const same = (a, b) => a?.path === b?.path && a?.bytes === b?.bytes && a?.sha256 === b?.sha256;
  for (const entry of inputs.requiredInputs) verifyFile(repo, entry);
  for (const entry of direct.files) verifyFile(repo, entry);
  assert(inputs.version === 3 && inputs.fullMinimumTests >= 683 && direct.productAdopted === true && !direct.beforeCurrent, "Explicit current lineage and baseline schema required");
  const baseline = direct.baselineProvenance;
  assert(baseline?.commitPrefix === "ed88107" && baseline.workflowRunID === 38845582472 && baseline.productBefore.length === 2, "Missing tested historical baseline provenance");
  for (const entry of baseline.productBefore) {
    assert(entry.path.startsWith("web/src/"), "Unsafe historical product path");
    verifyFile(repo, { ...entry, path: `ci/candidates/web-direct-edit/before/src/${entry.path.slice("web/src/".length)}` });
  }
  assert(direct.adoptedAfterBaseline.length === 2 && direct.currentAfter.length === 2 && direct.adoptedTests.length === 3, "Incomplete direct adoption pins");
  for (const entry of direct.adoptedAfterBaseline) {
    assert(entry.path.startsWith("web/src/"), "Unsafe original adoption path");
    verifyFile(repo, { ...entry, path: `ci/candidates/web-direct-edit/after/src/${entry.path.slice("web/src/".length)}` });
    assert(edit.before.some((before) => same(entry, before)), "Direct-after is not the documented editor predecessor");
  }
  assert(edit.candidateManifestSHA256 === "d72ee2c6ab329acf9a85196d19ce775b372ad3a6454c15c3724a1a2778443d4f" && edit.before.length === 4 && edit.currentAfter.length === 4 && edit.adoptedTests.length === 4, "Incomplete reviewed editor lineage");
  assert(edit.beforeCheckpoint.commit === "b45c90e121cfc7934c99d47ac45e12c152d78ed7" && edit.beforeCheckpoint.run === 38847106321, "Wrong predecessor checkpoint");
  verifyFile(repo, edit.patch);
  verifyFile(repo, edit.portableConfig);
  assert(edit.frozenCandidateFiles.length === 12 && edit.tests === 25 && edit.positiveNames.length === 7, "Incomplete frozen editor regression context");
  for (const entry of edit.frozenCandidateFiles) verifyFile(repo, entry);
  for (const entry of edit.before) {
    assert(entry.path.startsWith("web/src/"), "Unsafe editor predecessor path");
    verifyFile(repo, { ...entry, path: `ci/candidates/web-editable-save/before/src/${entry.path.slice("web/src/".length)}` });
  }
  for (const entry of edit.currentAfter) {
    assert(entry.path.startsWith("web/src/"), "Unsafe editor successor path");
    verifyFile(repo, currentIndividualForwardInput(currentChatCardInput(entry, card), individual));
    verifyFile(repo, { ...entry, path: `ci/candidates/web-editable-save/after/src/${entry.path.slice("web/src/".length)}` });
  }
  for (const entry of edit.adoptedTests) {
    assert(entry.path.startsWith("web/src/core/editableContentSave/"), "Unsafe adopted regression path");
    verifyFile(repo, { ...entry, path: `ci/candidates/web-editable-save/tests/${entry.path.slice("web/src/core/editableContentSave/".length)}` });
  }
  for (const entry of direct.currentAfter) assert(edit.currentAfter.some((after) => same(entry, after)), "Direct current pins must be a subset of the explicit four-file successor");
  const adapter = direct.adoptedTestTypeAdapter;
  assert(direct.adoptedTestsBaseline.length === 3 && adapter.path === "web/src/core/groupDirectEditScope/directEditHooks.test.ts", "Missing explicit test-only ACK type adapter");
  for (const previous of direct.adoptedTestsBaseline) {
    const now = direct.adoptedTests.find((entry) => entry.path === previous.path);
    if (previous.path === adapter.path) assert(same(previous, adapter.before) && same(now, adapter.after), "Test type adapter pin mismatch");
    else assert(same(previous, now), "Unrelated adopted test changed");
  }
  for (const entry of [...direct.adoptedTests, ...edit.adoptedTests]) verifyFile(repo, entry);
  assert(direct.historicalContext.length === 1 && direct.realCallers.length === 16 && direct.realCallersBaseline.length === 16, "Incomplete historical caller context");
  const editor = direct.realCallersBaseline.find((entry) => entry.path === "web/src/components/EditableContent/index.tsx");
  const historical = direct.historicalContext[0];
  assert(historical.path === "ci/candidates/web-direct-edit/context/EditableContent.tsx" && historical.bytes === editor?.bytes && historical.sha256 === editor?.sha256 && edit.before.some((entry) => same(entry, editor)), "Historical editor must exactly preserve the pre-editor caller");
  verifyFile(repo, historical);
  for (const previous of direct.realCallersBaseline) {
    const now = direct.realCallers.find((entry) => entry.path === previous.path);
    assert(now, "Missing current caller");
    if (previous.path === editor.path) assert(edit.currentAfter.some((entry) => same(entry, now)), "Current editor caller must match reviewed successor");
    else if (previous.path === "web/src/pages/chat/queryChat/ChatHeader/index.tsx") verifyStyleCaller(previous, now, inputs.mobileStyleAdoption);
    else assert(same(previous, now), "Unrelated caller/SDK pin changed");
  }
  for (const entry of direct.realCallers) verifyFile(repo, currentIndividualForwardInput(currentChatCardInput(entry, card), individual));
  const style = inputs.mobileStyleAdoption;
  assert(style.fullMinimumTests === 689 && inputs.fullMinimumTests >= style.fullMinimumTests && style.newTests === 6 && style.requiredInputs.length === 3, "Complete mobile style regression context required");
  verifyFile(repo, style.frozenBefore);
  for (const entry of style.requiredInputs) verifyFile(repo, entry);
  return { inputs, linuxBindings: verifyLinuxBindings(json(path.join(web, "package-lock.json"))) };
}

async function main() {
  if (process.argv[2] === "verify-inputs") { const verified = verifyInputs(); console.log(JSON.stringify({ verified: true, candidateFiles: verified.inputs.candidate.files.length, linuxBindings: verified.linuxBindings })); return; }
  assert(process.argv.length === 2 || process.argv.length === 3, "Use one fixed cohort, no arguments, or verify-inputs");
  const cohort = webCohort(process.argv[2]);
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
    const card = inputs.chatCardAdoption, copy = card.copyRetention;
    const individual = inputs.individualForwardAdoption;
    const cardNames = [...card.requiredNames, ...copy.requiredNames];
    if (cohort === "chat-card-42") await phase("chat-card-42", () => runTests("chat-card-42",
      { exact: card.tests + copy.newTests, requiredNames: cardNames }, [...card.testPaths, ...copy.testPaths]));
    else if (cohort === "individual-forward-32") await phase("individual-forward-32", () => runTests("individual-forward-32",
      individualForwardCohort(individual), individual.testPaths));
    else await phase("full-web", () => runTests("full-web", { minimum: inputs.fullMinimumTests, requiredNames: [...cardNames, ...individual.requiredNames] }));
    await phase("types-web-strict", async () => checkedProcess(await execute("types-web-strict", process.execPath, ["node_modules/typescript/bin/tsc", "--noEmit"])));
    await phase("types-desktop-original", async () => checkedProcess(await execute("types-desktop-original", process.execPath, ["node_modules/typescript/bin/tsc", "--noEmit", "-p", "tsconfig.desktop.json"])));
    if (cohort === "full-regression") {
    // Independent of the test/type outcomes: a successful preview bundle can be
    // downloaded for review even when another check failed. It is never deployed.
    await phase("web-build", async () => {
      checkedProcess(await execute("web-build", process.execPath, ["node_modules/vite/bin/vite.js", "build", "--config", "vite.web.config.mts", "--outDir", path.join(output, "dist")]));
      const files = artifactManifest(path.join(output, "dist")); assert(files.some((file) => file.path === "dist/index.html"), "Missing built index");
      writeFileSync(path.join(output, "dist-manifest.json"), JSON.stringify(files, null, 2) + "\n", { flag: "wx" }); return { files: files.length };
    });
    for (const stage of ["before", "after"]) await phase(`direct-historical-${stage}`, async () => {
      const result = await runTests(`direct-historical-${stage}`,
        { exact: inputs.candidate.tests, before: stage === "before", positiveNames: inputs.candidate.positiveNames },
        ["--config", path.join(repo, "ci/web-ci.candidate.config.mts"), "--configLoader", "runner"], { LUOBO_GROUP_DIRECT_EDIT_STAGE: stage });
      if (stage === "before") assert(result.passed === 9 && result.failed === 24, "Historical baseline outcome changed");
      return result;
    });
    for (const stage of ["before", "after"]) await phase(`editable-baseline-${stage}`, async () => {
      const result = await runTests(`editable-baseline-${stage}`,
        { exact: inputs.editableContentAdoption.tests, before: stage === "before", positiveNames: inputs.editableContentAdoption.positiveNames },
        ["--config", path.join(repo, "ci/web-ci.editable.config.mts"), "--configLoader", "runner"], { LUOBO_EDITABLE_CONTENT_STAGE: stage });
      if (stage === "before") assert(result.passed === 7 && result.failed === 18, "Editor baseline outcome changed");
      return result;
    });
    }
    await phase("post-input-integrity", async () => verifyInputs());
  }
  const result = { commit: process.env.GITHUB_SHA ?? null, node: process.version, cohort, success: stages.every((stage) => stage.passed) && Boolean(prepared), stages };
  writeFileSync(path.join(output, "summary.json"), JSON.stringify(result, null, 2) + "\n", { flag: "wx" });
  writeFileSync(path.join(output, "stages.junit.xml"), `<?xml version="1.0"?><testsuite name="web-ci-stages" tests="${stages.length}" failures="${stages.filter((stage) => !stage.passed).length}">${stages.map((stage) => `<testcase name="${xml(stage.name)}">${stage.passed ? "" : `<failure message="${xml(stage.error)}"/>`}</testcase>`).join("")}</testsuite>\n`, { flag: "wx" });
  if (!result.success) process.exitCode = 1;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) await main().catch((error) => { console.error(error.message); process.exitCode = 1; });
