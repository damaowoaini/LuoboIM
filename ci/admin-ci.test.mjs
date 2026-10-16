import test from "node:test";
import assert from "node:assert/strict";
import { verifyAdminLinuxBindings, validateReport } from "./admin-ci.helpers.mjs";

const native = (version) => ({ version, os: ["linux"], cpu: ["x64"] });
function lock() { return { lockfileVersion: 3, packages: {
  "node_modules/esbuild": { optionalDependencies: { "@esbuild/linux-x64": "0.28.2" } },
  "node_modules/@esbuild/linux-x64": native("0.28.2"),
  "node_modules/outer/node_modules/esbuild": { optionalDependencies: { "@esbuild/linux-x64": "0.21.4" } },
  "node_modules/outer/node_modules/@esbuild/linux-x64": native("0.21.4"),
} }; }
test("all nested Linux binding versions resolve from their own package", () => {
  assert.equal(verifyAdminLinuxBindings(lock()).length, 2);
});
test("a matching unrelated binding cannot hide a missing nested version", () => {
  const value = lock(); delete value.packages["node_modules/outer/node_modules/@esbuild/linux-x64"];
  value.packages["node_modules/unrelated/node_modules/@esbuild/linux-x64"] = native("0.21.4");
  assert.throws(() => verifyAdminLinuxBindings(value), /resolution-mismatched/);
});
test("a nearer incompatible binding must not fall back to a farther match", () => {
  const value = lock(); value.packages["node_modules/outer/node_modules/@esbuild/linux-x64"] = native("wrong");
  assert.throws(() => verifyAdminLinuxBindings(value), /resolution-mismatched/);
});
test("unapproved registry and missing Linux metadata are rejected", () => {
  const value = lock(); value.packages["node_modules/esbuild"].resolved = "https://untrusted.invalid/package.tgz";
  assert.throws(() => verifyAdminLinuxBindings(value), /Unreviewed/);
  delete value.packages["node_modules/esbuild"].resolved;
  value.packages["node_modules/@esbuild/linux-x64"].os = ["darwin"];
  assert.throws(() => verifyAdminLinuxBindings(value), /resolution-mismatched/);
});
test("Admin required suite cannot accept skipped, incomplete, or failed results", () => {
  const report = { numTotalTests: 1, numFailedTests: 0, numPassedTests: 1, numPendingTests: 0, numTodoTests: 0, numPendingTestSuites: 0, success: true, testResults: [{ message: "", assertionResults: [{}] }] };
  const audit = { reason: "passed", unhandledErrors: [], suiteErrors: [], failedHooks: [], tests: [{ state: "passed", mode: "run" }] };
  assert.equal(validateReport(report, audit, 0, { minimum: 1 }).passed, 1);
  assert.throws(() => validateReport(report, audit, 0, { minimum: 93 }), /at least/);
  audit.tests[0].mode = "skip";
  assert.throws(() => validateReport(report, audit, 0, { minimum: 1 }), /Skipped/);
});
