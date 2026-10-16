import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, writeFileSync, rmSync, symlinkSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { validateReport, verifyFile, verifyLinuxBindings } from "./web-ci.mjs";

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
