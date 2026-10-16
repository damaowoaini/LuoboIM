import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { policyProof, retainBuildCandidate, distManifest } from "./web-build.mjs";

const expected = JSON.parse(readFileSync(fileURLToPath(new URL("./web-build.inputs.json", import.meta.url)), "utf8")).csp;
const html = (content) => `<!doctype html><html><head>${content}</head><body></body></html>`;
const meta = (value) => `<meta http-equiv="Content-Security-Policy" content="${value}">`;

test("real locked HTML parser accepts escaped attribute quotes and equal CSP directive/source ordering", () => {
  const escaped = expected.replaceAll("'", "&#39;");
  assert.equal(policyProof(html(meta(escaped)), expected).passed, true);
  const reordered = expected.split(";").reverse().map((d) => d.trim().split(" ").filter(Boolean)).map(([name, ...sources]) => [name, ...sources.reverse()].join("\t")).join(";\n");
  assert.equal(policyProof(html(`<meta HTTP-EQUIV='Content-Security-Policy' content='${reordered.replaceAll("'", "&#x27;")}'>`), expected).passed, true);
});

test("real parsed CSP refuses weakening, missing or duplicate active policy and fake text tags", () => {
  assert.equal(policyProof(html(meta(expected.replace("script-src 'self'", "script-src 'self' 'unsafe-inline'"))), expected).passed, false);
  assert.equal(policyProof(html(meta(expected.replace("object-src 'none'; ", ""))), expected).passed, false);
  assert.equal(policyProof(html(meta(expected) + meta(expected)), expected).passed, false);
  assert.equal(policyProof(html(`<!--${meta(expected)}-->`), expected).passed, false);
  assert.equal(policyProof(html(`<textarea>${meta(expected)}</textarea>`), expected).passed, false);
  assert.equal(policyProof(html(meta(expected.replace("worker-src", "worKer-src"))), expected).passed, false);
  assert.equal(policyProof(html(`<meta http-equiv=" Content-Security-Policy " content="${expected}">`), expected).passed, false);
});

test("a policy-rejected safe dist retains bounded exact index and candidate proof without approval", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "web-policy-"));
  try {
    const dist = path.join(root, "dist"); mkdirSync(dist);
    const index = html(meta(expected.replace("script-src 'self'", "script-src 'self' 'unsafe-inline'")));
    writeFileSync(path.join(dist, "index.html"), index);
    writeFileSync(path.join(dist, "openIM.wasm"), "synthetic already-pinned public body");
    const pub = [{ path: "public/openIM.wasm", bytes: Buffer.byteLength("synthetic already-pinned public body"), sha256: createHash("sha256").update("synthetic already-pinned public body").digest("hex") }];
    const result = retainBuildCandidate(root, dist, pub, expected, { cohort: "synthetic", sourceDigest: "synthetic" });
    assert.equal(result.proof.passed, false);
    assert.equal(readFileSync(path.join(root, "built-index.html"), "utf8"), index);
    assert.equal(JSON.parse(readFileSync(path.join(root, "policy-proof.json"))).capturedIndexSHA256, createHash("sha256").update(index).digest("hex"));
    assert.equal(JSON.parse(readFileSync(path.join(root, "dist-candidate-proof.json"))).state, "built-bytes-policy-and-resource-not-accepted");
    assert.equal(result.files.length, 2);
    assert.throws(() => distManifest(dist, pub, expected), /CSP semantic mismatch/);
  } finally { rmSync(root, { recursive: true, force: true }); }
});
