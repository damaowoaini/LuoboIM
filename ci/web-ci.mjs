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
  assert(["chat-card-42", "individual-forward-32", "announcement-read-21", "loopback-dev-endpoints-8", "full-regression"].includes(value), "Explicit chat-card-42, individual-forward-32, announcement-read-21, loopback-dev-endpoints-8 or full-regression cohort required");
  return value;
}

export function readReceiptExpiryInputs(adoption) {
  assert(adoption?.originManifestSHA256 === "53e4d951431a9a2aad085783817577119408687210c58eac1309cbd8f18d5e2d" && adoption.uiSuccessorRevision === "read-time-barrier-r2" && adoption.supersedesNormalCIManifestSHA256 === "06c7e362dc03f1a36038052519e35e9d22c0ef3146b9f16b290719b8744c61c1", "Reviewed read-receipt expiry R2 lineage required");
  const before = [{"path":"web/src/core/messagePrivacyRuntime.ts","bytes":3597,"sha256":"90e02b9e841393c2f4f5cda67ff5f6d90570e3fb846e3f7b9c2770d3d162b193"},{"path":"web/src/pages/chat/queryChat/MessageItem/index.tsx","bytes":12110,"sha256":"f6ff9fd4c6b0c3334ececd31450ad01d449560427c754245c687946bf10fd372"},{"path":"web/src/pages/chat/queryChat/SingleSetting/index.tsx","bytes":13140,"sha256":"f68b80fb2a41ede12ee0db3d5d6e69330037ffb9708f7c7746f62d21ca96ae5c"},{"path":"web/src/pages/chat/queryChat/index.tsx","bytes":3919,"sha256":"b06174b10c41cd1e362d2e516a2f386a59f547acbb017cd57571f8cf576c0a10"},{"path":"web/src/pages/chat/queryChat/useConversationState.ts","bytes":4041,"sha256":"62575e5e93f435c87b2f24194501ff79c68da760a585898b282a6387ccac4aef"},{"path":"web/src/pages/chat/queryChat/useMessageReceipt.ts","bytes":2430,"sha256":"38cf319765cf22ca575e5d29f29da15c6d44cd089a1da357eaa7c818237f4b13"}];
  const current = [{"path":"web/src/core/messagePrivacyRuntime.ts","bytes":4733,"sha256":"30b8715650c716a850e0a6bb87ecee7df141d7be0d6d53ae3165efa1d20d8a38"},{"path":"web/src/core/messageReadBarrier.ts","bytes":2305,"sha256":"64875db9d9249f78d04d1d0622ae7d8a92f392d9a5bb8392a672072310be7cfa"},{"path":"web/src/core/messageReadReceipt.ts","bytes":13071,"sha256":"7f13a1f7c7f0f6858536d48c8dc02eff6bff547265eae41a20798443048ac2db"},{"path":"web/src/pages/chat/queryChat/MessageItem/index.tsx","bytes":12396,"sha256":"f3a744d43ec365d9e1a5cc8134065861c62091d50e8d57927780c2e8f00f5bdd"},{"path":"web/src/pages/chat/queryChat/ReadSyncStatus.tsx","bytes":1924,"sha256":"51b2ced626577765ea2017cf32d12f91ad87987f83d54cee5aa02ac756344691"},{"path":"web/src/pages/chat/queryChat/SingleSetting/index.tsx","bytes":13187,"sha256":"0f54d79c5753ae441f0bb7d5b29d49f710b61fee30d1ebdd2eef07e17f1c506a"},{"path":"web/src/pages/chat/queryChat/index.tsx","bytes":4197,"sha256":"0501647e1ccb927a42e426bda500f2a21536dc9920f5fee546eced83109757c7"},{"path":"web/src/pages/chat/queryChat/useConversationState.ts","bytes":9553,"sha256":"b02c919dff26e53a73dc91dd0b9d4b0ba28e6cca74938cc4eb87083666bc15a9"},{"path":"web/src/pages/chat/queryChat/useMessageReceipt.ts","bytes":2177,"sha256":"76943dcf669079d32896a49e735aebfec8773ca8be4ccda7af9cd65129402e93"}];
  const adopted = [{"path":"web/src/core/messageReadReceipt/harness.ts","bytes":5436,"sha256":"d8cf3d29b7de771a01ac327fe8e83da06e10677d3e5d2239e9c032a6bc0f5ff3"},{"path":"web/src/core/messageReadReceipt/read-receipt-hooks.test.tsx","bytes":9678,"sha256":"8941ad682d21a105d744761c4b3f023217896a8616f57054bc707b6745ee51c9"},{"path":"web/src/core/messageReadReceipt/zero-duration.test.tsx","bytes":2745,"sha256":"e708e54915291174b190d377b88373c9e35dff0676ce5b9efca45ce717828e64"},{"path":"web/src/core/messageReadReceipt/read-sync-ui.test.tsx","bytes":7717,"sha256":"e554c1933d473428df150ac21432fd3012ffd15b63ab07195610c3d45b728492"},{"path":"web/src/core/messageReadReceipt/live-read-admission.test.tsx","bytes":5259,"sha256":"e4ef2b921f9aaf47005ff7f8172a65980bf3693759560cbf1bd65e6ad8d0dd70"}];
  assert(JSON.stringify(adoption.before) === JSON.stringify(before) && JSON.stringify(adoption.currentAfter) === JSON.stringify(current) && JSON.stringify(adoption.adoptedTests) === JSON.stringify(adopted), "Exact read-receipt product and normal test/fixture lineage required");
  assert(JSON.stringify(adoption.testPaths) === JSON.stringify(["src/core/messageReadReceipt/read-receipt-hooks.test.tsx","src/core/messageReadReceipt/zero-duration.test.tsx","src/core/messageReadReceipt/read-sync-ui.test.tsx","src/core/messageReadReceipt/live-read-admission.test.tsx"]), "Exact normal read-receipt test paths required");
  assert(adoption.newTests === 15 && adoption.requiredNames?.length === 15 && new Set(adoption.requiredNames).size === 15 && hash(Buffer.from(JSON.stringify(adoption.requiredNames))) === "b268e741877f020f789f0e6d443b92e6e485ce6f27dfa0e5b1fd86b5c0fc1cab", "Exact15 read-receipt names required");
  assert(JSON.stringify(adoption.normalInputs) === JSON.stringify([{"path":"web/vitest.config.mts","bytes":430,"sha256":"8b51040e3c72c6d53f5247f8c70522866cb763c8311dd42912dcb913eca9ee62"},{"path":"web/tsconfig.json","bytes":758,"sha256":"d2c545c69816fe22cfdf11f25a3a7a01ce3e99f69a93e0a6a2e25bab47a3c92d"},{"path":"web/tsconfig.desktop.json","bytes":158,"sha256":"3897e9726f17e81d163e1d38c72f2237f510d7defbdba160d65210bbf51a355f"}]) && JSON.stringify(adoption.directAffectedInputs) === JSON.stringify([{"path":"web/src/core/currentGroupState/current-group-hook.test.tsx","bytes":10066,"sha256":"8883e84e4aa6168a848de6908e6ba7c62d74b608031a1d64304b105844688c78"},{"path":"web/src/core/currentGroupState/harness.ts","bytes":4731,"sha256":"9fe7b48b9ae913da969895087a04c3abcd0a1a5228a8bd879dec7539297a9c7e"}]), "Unchanged normal config/types and affected3 source context required");
  assert(adoption.previousExpectedFullTests === 796 && adoption.expectedFullTests === 811 && adoption.focusedManualModeAdded === false, "Explicit796+15 expected-only lineage required");
  assert(adoption.liveIncomingAdmission?.parentR4ManifestSHA256 === "42da00f60d3c1e6c68b46513134b8e0785ac91b1530d3a70e77796f3227a708b" && adoption.liveIncomingAdmission.previousExpectedFull === 808 && adoption.liveIncomingAdmission.newExpectedFull === 811, "Reviewed live incoming admission successor required");
  assert(adoption.liveIncomingAdmission.ackBarrierSuccessor?.parentR5ManifestSHA256 === "38775c8829ebadea894214af7d5617e8042792f3e3c0a9d9ebae4775b05abf16", "Reviewed ACK-aware incoming admission required");
  return { newTests: adoption.newTests, requiredNames: adoption.requiredNames, expectedFullTests: adoption.expectedFullTests };
}

export function currentReadReceiptInput(previous, adoption) {
  readReceiptExpiryInputs(adoption);
  const current = adoption.currentAfter.find(entry => entry.path === previous.path);
  if (!current) return previous;
  const before = adoption.before.find(entry => entry.path === previous.path);
  assert(before?.bytes === previous.bytes && before?.sha256 === previous.sha256, "Read-receipt predecessor differs");
  return current;
}

export function attachedInfoNullInputs(adoption) {
  assert(adoption?.originManifestSHA256 === "e4db439d130bebc6ff8a167569f11f1740f421c9ac4456c4f22065a5681ca72a", "Reviewed attached-info null lineage required");
  const before = [{"path":"web/src/core/messagePrivacy.ts","bytes":8398,"sha256":"75b464cbaeea1fc76164878672338ab041ad3eb5f2dc9ffc84d1186537d437f5"},{"path":"web/src/core/messagePrivacy.test.ts","bytes":5861,"sha256":"3a42f63f5372c8e6444042113739f5b0d3d8eeff31b4dcfde4fea800b4a14f1a"}];
  const current = [{"path":"web/src/core/messagePrivacy.ts","bytes":8417,"sha256":"84c2aee8072ad73ac3ad1ddbf8d00862906cf3fd6a20b4212337fd1f0b798ecc"},{"path":"web/src/core/messagePrivacy.test.ts","bytes":9345,"sha256":"3f6fce50b1b6caa9cda4c91948ba4aa166f7488ee45e95c30df03aae7c57fab2"}];
  assert(JSON.stringify(adoption.before) === JSON.stringify(before) && JSON.stringify(adoption.currentAfter) === JSON.stringify(current), "Exact attached-info null source/test lineage required");
  assert(adoption.testPath === "src/core/messagePrivacy.test.ts" && adoption.newTests === 4 && adoption.requiredNames?.length === 4 && new Set(adoption.requiredNames).size === 4 && hash(Buffer.from(JSON.stringify(adoption.requiredNames))) === "cced75d7a160477f069e8f0fa0de4c5f242705adb3c16947b394673a2f9d4a15", "Exact4 attached-info null names and existing path required");
  assert(adoption.previousExpectedFullTests === 792 && adoption.expectedFullTests === 796 && adoption.focusedManualModeAdded === false, "Explicit792+4 expected-only lineage required");
  return { newTests: adoption.newTests, requiredNames: adoption.requiredNames, expectedFullTests: adoption.expectedFullTests };
}

export function loopbackDevEndpointsCohort(loopback) {
  assert(loopback?.originManifestSHA256 === "617d484a27b27c08e6b5c931ed3b4706d4f2bd62b033e4261f4e035c52b12d33", "Reviewed loopback transport lineage required");
  const before = [{"path":"web/src/main.tsx","bytes":681,"sha256":"ff759b0f45f7b2399b73a7f9c10803cc721186cdfbcd4931e89236cf35dd3b83"},{"path":"web/src/config/index.ts","bytes":994,"sha256":"1a6544cf6efaf6ee45b8eb31b67256d1b2ed1aa4ff3e233773d9bd6c6b397059"},{"path":"web/src/core/session/logout.ts","bytes":3296,"sha256":"c1b750b285e6feb6bc64b2a7b0238c992096b839b56aa55485f5d319da60d1ad"},{"path":"web/src/api/login.ts","bytes":7929,"sha256":"34fe2ed0c4f6ca9fd61b6f32633b105d0c19dfb02ed799d1f435fd88c7c4a42d"}];
  const current = [{"path":"web/src/main.tsx","bytes":801,"sha256":"9074b47cfa4d3cd523b77370fe304d197aa73d20e1a494c673940e9377c99a63"},{"path":"web/src/config/index.ts","bytes":1027,"sha256":"10fda6ffb430a2da7d75626bc842283e3579d55ae04cb109f62817715abad511"},{"path":"web/src/core/session/logout.ts","bytes":3343,"sha256":"d23cbe06bda6d36c36dd959bfdf0e41a2ee9c9e68e0936922b82b5c7a101e985"},{"path":"web/src/api/login.ts","bytes":8437,"sha256":"b5db2f5ca82a6750df0a794abce7bb758c96a3490aaad7497023815c3adfa3c4"},{"path":"web/src/core/session/devEndpoints.ts","bytes":4794,"sha256":"58529eb378b2bec89ac9277666bc43318a9003494a46e2efb6273b8f64220b89"},{"path":"web/src/core/appConfig.ts","bytes":702,"sha256":"5cfd0226bddce023b693f5fc155e5ecdbd1cd826c81d32f14be9068f79ac4f4a"}];
  const adopted = [{"path":"web/src/core/session/devEndpoints.test.ts","bytes":8874,"sha256":"6e476df43b0aafaf00c9cbba3c6262df19fbdf24c90ae4c2b856d89fa459cc78"},{"path":"web/src/core/appConfig.test.ts","bytes":2431,"sha256":"5e354dee761560f933f8782cd50ee39ba6c6a0982af4ac5de740c431397c9d14"}];
  assert(JSON.stringify(loopback.before) === JSON.stringify(before) && JSON.stringify(loopback.currentAfter) === JSON.stringify(current) && JSON.stringify(loopback.adoptedTests) === JSON.stringify(adopted), "Exact loopback source/test lineage required");
  assert(JSON.stringify(loopback.testPaths) === JSON.stringify(["src/core/session/devEndpoints.test.ts","src/core/appConfig.test.ts"]), "Exact normal loopback test paths required");
  assert(loopback.tests === 8 && loopback.requiredNames?.length === 8 && new Set(loopback.requiredNames).size === 8 && hash(Buffer.from(JSON.stringify(loopback.requiredNames))) === "4b28c318c911416bfcd5be1b802b5c73f6a43b07f3f94534f141a20eb595d294", "Exact8 loopback names required");
  assert(loopback.previousExpectedFullTests === 784 && loopback.expectedFullTests === 792, "Explicit784+8 loopback regression lineage required");
  return { exact: loopback.tests, requiredNames: loopback.requiredNames };
}

export function announcementReadCohort(announcement) {
  assert(announcement?.originManifestSHA256 === "012853b3d517e34c97db227b932e91afcff2895783df16e5bfc71effcbd6cd3c", "Reviewed announcement-read lineage required");
  const before = [{"bytes":2721,"path":"web/src/pages/chat/queryChat/ChatHeader/GroupAnnouncementCard.tsx","sha256":"98c055ade4188e56fe51775dd08cbd4449fef3d7c942b658cfe605c594702ed0"}];
  const current = [{"bytes":11272,"path":"web/src/pages/chat/queryChat/ChatHeader/GroupAnnouncementCard.tsx","sha256":"57d1849f58f95f43d3e2935082392cb1d064c8208ef83c3836b2f010aee26eb7"},{"bytes":5142,"path":"web/src/core/groupAnnouncementAck.ts","sha256":"2e1bb5b626b239a97ec95220c8bc56872d4ce23149371ef8f48bc1492ffd582f"}];
  const adopted = [{"bytes":4316,"path":"web/src/core/groupAnnouncementAck.test.ts","sha256":"c1ab5310630769983491b8f6c7bb74b4da1dcdc4dfd7f302c46af918b661c917"},{"bytes":12381,"path":"web/src/core/groupAnnouncementScope/announcement-read.test.tsx","sha256":"2f222fa1fae29bf23ba06ab375f677ff0252ae39ff7ab814b91b8daa88197ce3"}];
  assert(JSON.stringify(announcement.before) === JSON.stringify(before) && JSON.stringify(announcement.currentAfter) === JSON.stringify(current) && JSON.stringify(announcement.adoptedTests) === JSON.stringify(adopted), "Exact announcement source/test lineage required");
  assert(JSON.stringify(announcement.testPaths) === JSON.stringify(["src/core/groupAnnouncementAck.test.ts","src/core/groupAnnouncementScope/announcement-read.test.tsx"]), "Exact normal announcement test paths required");
  assert(announcement.tests === 21 && announcement.requiredNames?.length === 21 && new Set(announcement.requiredNames).size === 21 && hash(Buffer.from(JSON.stringify(announcement.requiredNames))) === "1a16d714b2cd262bc49767b8d20c7d9552aad3a064afcaf08bbbc01c83c63cb6", "Exact21 announcement names required");
  assert(announcement.previousExpectedFullTests === 763 && announcement.expectedFullTests === 784, "Explicit763+21 announcement regression lineage required");
  return { exact: announcement.tests, requiredNames: announcement.requiredNames };
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
  const announcement = inputs.announcementReadAdoption;
  const loopback = inputs.loopbackDevEndpointsAdoption;
  const readReceipt = inputs.readReceiptExpiryAdoption;
  readReceiptExpiryInputs(readReceipt);
  for (const entry of [...readReceipt.currentAfter, ...readReceipt.adoptedTests, ...readReceipt.normalInputs, ...readReceipt.directAffectedInputs]) verifyFile(repo, entry);
  const attachedNull = inputs.attachedInfoNullAdoption;
  attachedInfoNullInputs(attachedNull);
  for (const entry of attachedNull.currentAfter) verifyFile(repo, entry);
  loopbackDevEndpointsCohort(loopback);
  for (const entry of [...loopback.currentAfter, ...loopback.adoptedTests]) verifyFile(repo, entry);
  individualForwardCohort(individual);
  announcementReadCohort(announcement);
  for (const entry of [...announcement.currentAfter, ...announcement.adoptedTests]) verifyFile(repo, entry);
  assert(card?.originManifestSHA256 === "d6404ca1d66bd5c972cbd9369eca309ef92c259478ca3b94204f4d938d41d48a" && card.before.length === 13 && card.currentAfter.length === 15 && card.adoptedTests.length === 4, "Complete chat-card source/test lineage required");
  assert(card.tests === 37 && card.priorFullPassed === 689 && card.expectedFullTests === 726 && copy?.newTests === 5 && copy.expectedFullTests === 731 && inputs.fullMinimumTests === readReceipt.expectedFullTests && readReceipt.previousExpectedFullTests === attachedNull.expectedFullTests && attachedNull.previousExpectedFullTests === loopback.expectedFullTests && loopback.previousExpectedFullTests === announcement.expectedFullTests && announcement.previousExpectedFullTests === individual.expectedFullTests, "Explicit pending689+37+5+32+21+8+4+15 regression cohort required");
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
  for (const entry of card.currentAfter) { assert(entry.path.startsWith("web/src/"), "Unsafe chat-card source path"); verifyFile(repo, currentReadReceiptInput(currentIndividualForwardInput(currentCardCopyInput(entry, copy), individual), readReceipt)); }
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
  for (const entry of direct.realCallers) verifyFile(repo, currentReadReceiptInput(currentIndividualForwardInput(currentChatCardInput(entry, card), individual), readReceipt));
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
    else if (cohort === "announcement-read-21") await phase("announcement-read-21", () => runTests("announcement-read-21",
      announcementReadCohort(inputs.announcementReadAdoption), inputs.announcementReadAdoption.testPaths));
    else if (cohort === "loopback-dev-endpoints-8") await phase("loopback-dev-endpoints-8", () => runTests("loopback-dev-endpoints-8",
      loopbackDevEndpointsCohort(inputs.loopbackDevEndpointsAdoption), inputs.loopbackDevEndpointsAdoption.testPaths));
    else await phase("full-web", () => runTests("full-web", { minimum: inputs.fullMinimumTests, requiredNames: [...cardNames, ...individual.requiredNames, ...inputs.announcementReadAdoption.requiredNames, ...inputs.loopbackDevEndpointsAdoption.requiredNames, ...inputs.attachedInfoNullAdoption.requiredNames, ...inputs.readReceiptExpiryAdoption.requiredNames] }));
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
