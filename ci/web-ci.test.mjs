import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, writeFileSync, rmSync, symlinkSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { validateReport, verifyFile, verifyLinuxBindings, verifyStyleCaller, currentChatCardInput, currentCardCopyInput, webCohort, individualForwardCohort, currentIndividualForwardInput, announcementReadCohort } from "./web-ci.mjs";

function runFixture(callback) {
  const root = mkdtempSync(path.join(os.tmpdir(), "luobo-web-ci-"));
  try { callback(root); } finally { rmSync(root, { recursive: true }); }
}
const entry = (value = "source") => ({ path: "source.ts", bytes: Buffer.byteLength(value), sha256: createHash("sha256").update(value).digest("hex") });
function reports(states = ["passed", "passed"]) {
  const tests = states.map((state, index) => ({ id: String(index), name: `test-${index}`, state, mode: "run", errors: state === "failed" ? [{ name: "AssertionError" }] : [] }));
  const failed = tests.filter((item) => item.state === "failed").length;
  return { report: { numTotalTests: states.length, numFailedTests: failed, numPassedTests: states.length - failed, numPendingTests: 0, numTodoTests: 0, numPendingTestSuites: 0, success: failed === 0,
    testResults: [{ message: "", assertionResults: tests }] }, audit: { reason: failed ? "failed" : "passed", unhandledErrors: [], suiteErrors: [], failedHooks: [], tests } };
}
function checked(data, exit, options) { return validateReport(data.report, data.audit, exit, options); }

test("exact regular input is accepted without mutating it", () => runFixture((root) => {
  writeFileSync(path.join(root, "source.ts"), "source"); verifyFile(root, entry());
}));
test("parent traversal is rejected before reading", () => assert.throws(() => verifyFile("/absent", { ...entry(), path: "../source.ts" }), /Unsafe/));
test("symlink input is rejected even when target bytes match", () => runFixture((root) => {
  writeFileSync(path.join(root, "target"), "source"); symlinkSync("target", path.join(root, "source.ts"));
  assert.throws(() => verifyFile(root, entry()), /Symlink/);
}));
test("changed source is rejected", () => runFixture((root) => {
  writeFileSync(path.join(root, "source.ts"), "different"); assert.throws(() => verifyFile(root, entry()), /mismatch/);
}));
test("Linux x64 binding must match its exact locked parent version", () => {
  const lock = { lockfileVersion: 3, packages: { "node_modules/rolldown": { optionalDependencies: { "@rolldown/binding-linux-x64-gnu": "1.2.14" } }, "node_modules/@rolldown/binding-linux-x64-gnu": { version: "1.2.14", cpu: ["x64"], os: ["linux"] } } };
  assert.equal(verifyLinuxBindings(lock).length, 1); delete lock.packages["node_modules/@rolldown/binding-linux-x64-gnu"];
  assert.throws(() => verifyLinuxBindings(lock), /Missing locked Linux/);
});
test("required full suite needs a completed minimum count", () => {
  assert.equal(checked(reports(), 0, { minimum: 2 }).passed, 2);
  assert.throws(() => checked(reports(), 0, { minimum: 3 }), /at least/);
});
test("before accepts only a complete assertion baseline with positive controls", () => {
  assert.equal(checked(reports(["passed", "failed"]), 1, { exact: 2, before: true, positiveNames: ["test-0"] }).failed, 1);
  assert.throws(() => checked(reports(), 0, { exact: 2, before: true }), /Frozen before/);
});
test("before runtime TypeError is a fixture/runtime failure", () => {
  const data = reports(["passed", "failed"]); data.audit.tests[1].errors[0].name = "TypeError";
  assert.throws(() => checked(data, 1, { exact: 2, before: true }), /non-assertion/);
});
test("before must retain a passing positive control", () => {
  assert.throws(() => checked(reports(["passed", "failed"]), 1, { exact: 2, before: true, positiveNames: ["test-1"] }), /positive control/);
});
test("skip and only cannot disguise a completed suite", () => {
  const data = reports(); data.audit.tests[0].mode = "only";
  assert.throws(() => checked(data, 0, { exact: 2 }), /Skipped/);
});
test("unhandled and hook errors invalidate even a green JSON footer", () => {
  const data = reports(); data.audit.unhandledErrors.push({ name: "Error" });
  assert.throws(() => checked(data, 0, { exact: 2 }), /Unhandled/);
  data.audit.unhandledErrors = []; data.audit.failedHooks.push({ name: "beforeEach" });
  assert.throws(() => checked(data, 0, { exact: 2 }), /Unhandled/);
});
test("module loading errors and reporter count disagreement fail closed", () => {
  const data = reports(); data.report.testResults[0].message = "cannot resolve module";
  assert.throws(() => checked(data, 0, { exact: 2 }), /loading/);
  data.report.testResults[0].message = ""; data.report.numTotalTests++;
  assert.throws(() => checked(data, 0, { exact: 2 }), /counts differ/);
});

const style = JSON.parse(readFileSync(new URL("./web-ci.inputs.json", import.meta.url), "utf8")).mobileStyleAdoption;
test("explicit style successor preserves the exact caller predecessor", () => verifyStyleCaller(style.before, style.after, style));
test("style lineage refuses an unrelated caller or altered predecessor", () => {
  assert.throws(() => verifyStyleCaller({ ...style.before, sha256: "0".repeat(64) }, style.after, style), /Unrelated/);
  assert.throws(() => verifyStyleCaller(style.before, { ...style.after, path: "web/src/another.tsx" }, style), /Unrelated/);
});
test("style lineage cannot discard frozen source or use an unreviewed candidate", () => {
  assert.throws(() => verifyStyleCaller(style.before, style.after, { ...style, frozenBefore: { ...style.frozenBefore, bytes: 1 } }), /Frozen/);
  assert.throws(() => verifyStyleCaller(style.before, style.after, { ...style, candidateManifestSHA256: "0".repeat(64) }), /Reviewed/);
});

const card = JSON.parse(readFileSync(new URL("./web-ci.inputs.json", import.meta.url), "utf8")).chatCardAdoption;
test("chat-card source lineage preserves the tested predecessor and resolves only reviewed current pins", () => {
  for (const path of ["web/src/store/conversation.ts", "web/src/pages/common/UserCardModal/index.tsx"]) {
    const before = card.before.find((entry) => entry.path === path);
    const original = card.currentAfter.find((entry) => entry.path === path);
    assert.equal(currentChatCardInput(before, card), currentCardCopyInput(original, card.copyRetention));
  }
  const unrelated = entry(); assert.equal(currentChatCardInput(unrelated, card), unrelated);
});
test("chat-card source lineage refuses a changed predecessor or unreviewed parent", () => {
  const previous = card.before[0];
  assert.throws(() => currentChatCardInput({ ...previous, sha256: "0".repeat(64) }, card), /predecessor differs/);
  assert.throws(() => currentChatCardInput(previous, { ...card, originManifestSHA256: "0".repeat(64) }), /Reviewed/);
});
test("chat-card report requires each exact name once rather than accepting a matching count", () => {
  assert.equal(checked(reports(), 0, { exact: 2, requiredNames: ["test-0", "test-1"] }).passed, 2);
  assert.throws(() => checked(reports(), 0, { exact: 2, requiredNames: ["missing"] }), /Required test/);
  const duplicate = reports(); duplicate.audit.tests[1].name = "test-0";
  assert.throws(() => checked(duplicate, 0, { exact: 2, requiredNames: ["test-0"] }), /Required test/);
});
test("chat-card manual cohort rejects unknown modes and preserves explicit full regression", () => {
  assert.equal(webCohort("chat-card-42"), "chat-card-42");
  assert.equal(webCohort("full-regression"), "full-regression");
  assert.equal(webCohort(), "full-regression");
  for (const unknown of ["", "card", "full", "chat-card-37", "chat-card-42 --extra"]) assert.throws(() => webCohort(unknown), /Explicit/);
});
test("chat-card copy retention successor preserves its exact original profile identity", () => {
  const copy = card.copyRetention;
  assert.equal(currentCardCopyInput(copy.before[0], copy), copy.after[0]);
  assert.throws(() => currentCardCopyInput({ ...copy.before[0], sha256: "0".repeat(64) }, copy), /predecessor differs/);
  assert.throws(() => currentCardCopyInput(copy.before[0], { ...copy, before: [{ ...copy.before[0], sha256: "0".repeat(64) }] }), /Exact original/);
  assert.throws(() => currentCardCopyInput(copy.before[0], { ...copy, after: [{ ...copy.after[0], path: "web/src/unrelated.tsx" }] }), /Exact original/);
});

const individual = JSON.parse(readFileSync(new URL("./web-ci.inputs.json", import.meta.url), "utf8")).individualForwardAdoption;
test("individual-forward manual cohort retains exact reviewed names and normal paths", () => {
  assert.equal(webCohort("individual-forward-32"), "individual-forward-32");
  const options = individualForwardCohort(individual);
  assert.equal(options.exact, 32); assert.equal(options.requiredNames, individual.requiredNames);
  const data = reports(Array(32).fill("passed"));
  data.audit.tests.forEach((entry, index) => { entry.name = individual.requiredNames[index]; });
  assert.equal(checked(data, 0, options).passed, 32);
  data.audit.tests[0].name = "unreviewed name";
  assert.throws(() => checked(data, 0, options), /Required test/);
  assert.throws(() => individualForwardCohort({ ...individual, requiredNames: individual.requiredNames.slice(1) }), /Exact 32/);
  assert.throws(() => individualForwardCohort({ ...individual, requiredNames: ["unreviewed name", ...individual.requiredNames.slice(1)] }), /Exact 32/);
  assert.throws(() => individualForwardCohort({ ...individual, testPaths: [...individual.testPaths, "src/unrelated.test.ts"] }), /Exact 32/);
  assert.throws(() => individualForwardCohort({ ...individual, expectedFullTests: 731 }), /regression lineage/);
});
test("individual-forward successor preserves card history and rejects changed lineage", () => {
  const picker = card.currentAfter.find((entry) => entry.path === "web/src/pages/common/ChooseModal/index.tsx");
  assert.equal(currentIndividualForwardInput(picker, individual), individual.currentAfter.find((entry) => entry.path === picker.path));
  const unchanged = card.currentAfter.find((entry) => entry.path === "web/src/core/messageCard.ts");
  assert.equal(currentIndividualForwardInput(unchanged, individual), unchanged);
  assert.throws(() => currentIndividualForwardInput({ ...picker, sha256: "0".repeat(64) }, individual), /predecessor differs/);
  assert.throws(() => currentIndividualForwardInput(picker, { ...individual, originManifestSHA256: "0".repeat(64) }), /Reviewed/);
  assert.throws(() => currentIndividualForwardInput(picker, { ...individual, before: individual.before.filter((entry) => entry.path !== picker.path) }), /predecessor paths/);
  assert.throws(() => currentIndividualForwardInput(picker, { ...individual, currentAfter: individual.currentAfter.slice(1) }), /source and fixture paths/);
});

const announcement = JSON.parse(readFileSync(new URL("./web-ci.inputs.json", import.meta.url), "utf8")).announcementReadAdoption;
test("announcement-read cohort requires all21 names and normal source paths", () => {
  assert.equal(webCohort("announcement-read-21"), "announcement-read-21");
  const options = announcementReadCohort(announcement);
  assert.equal(options.exact, 21);
  const data = reports(Array(21).fill("passed"));
  data.audit.tests.forEach((entry, index) => { entry.name = announcement.requiredNames[index]; });
  assert.equal(checked(data, 0, options).passed, 21);
  data.audit.tests[0].name = "unreviewed announcement name";
  assert.throws(() => checked(data, 0, options), /Required test/);
  assert.throws(() => announcementReadCohort({ ...announcement, requiredNames: announcement.requiredNames.slice(1) }), /Exact21/);
  assert.throws(() => announcementReadCohort({ ...announcement, requiredNames: ["wrong", ...announcement.requiredNames.slice(1)] }), /Exact21/);
  assert.throws(() => announcementReadCohort({ ...announcement, testPaths: [...announcement.testPaths, "src/unrelated.test.ts"] }), /normal announcement/);
  assert.throws(() => announcementReadCohort({ ...announcement, expectedFullTests: 763 }), /regression lineage/);
});
test("announcement successor rejects wrong source and keeps card individual history exact", () => {
  assert.deepEqual(announcementReadCohort(announcement).requiredNames, announcement.requiredNames);
  assert.throws(() => announcementReadCohort({ ...announcement, originManifestSHA256: "0".repeat(64) }), /Reviewed/);
  const currentAfter = structuredClone(announcement.currentAfter); currentAfter[0].sha256 = "0".repeat(64);
  assert.throws(() => announcementReadCohort({ ...announcement, currentAfter }), /source\/test lineage/);
  const before = structuredClone(announcement.before); before[0].bytes++;
  assert.throws(() => announcementReadCohort({ ...announcement, before }), /source\/test lineage/);
  const input = JSON.parse(readFileSync(new URL("./web-ci.inputs.json", import.meta.url), "utf8"));
  assert.equal(input.fullMinimumTests, 784);
  assert.equal(input.announcementReadAdoption.previousExpectedFullTests, input.individualForwardAdoption.expectedFullTests);
  assert.equal(new Set([...input.chatCardAdoption.requiredNames, ...input.chatCardAdoption.copyRetention.requiredNames, ...input.individualForwardAdoption.requiredNames, ...announcement.requiredNames]).size, 95);
});
