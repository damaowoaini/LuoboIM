import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { buildPlan, validateDescriptor, verifySourceTree, distManifest } from "./web-build.mjs";

const input = JSON.parse(readFileSync(fileURLToPath(new URL("./web-build.inputs.json", import.meta.url)), "utf8"));
const pin = (name, body) => ({ path: name, bytes: Buffer.byteLength(body), sha256: createHash("sha256").update(body).digest("hex") });

test("build-only plan contains locked install, exact patch and Vite without any test or type command", () => {
  const plan = buildPlan("/official/bin/node", "/official/lib/npm-cli.js", "/owned/dist");
  assert.deepEqual(plan, [
    { name: "locked-install", command: "/official/bin/node", args: ["/official/lib/npm-cli.js", "ci", "--ignore-scripts", "--include=dev", "--no-audit", "--no-fund", "--registry=https://registry.npmjs.org"], timeout: 600000 },
    { name: "reviewed-sdk-prepare", command: "/official/bin/node", args: ["tool/prepare-sdk.cjs"], timeout: 180000 },
    { name: "web-build", command: "/official/bin/node", args: ["node_modules/vite/bin/vite.js", "build", "--config", "vite.web.config.mts", "--outDir", "/owned/dist"], timeout: 180000 },
  ]);
  assert.equal(plan.some((p) => p.args.some((v) => /vitest|tsc|--noEmit/.test(v))), false);
});

test("reviewed4340 descriptor binds SDK8/public2 and refuses changed source, duplicate paths and historical identity", () => {
  assert.equal(validateDescriptor(input).sourceFiles, 4340);
  assert.equal(input.sourceDigest, "3503ee773da527e252fa284c2380dcda386999012a5aa538fa60f84c42b2b636");
  const sdk = JSON.parse(readFileSync(fileURLToPath(new URL("../web/sdk-patches/manifest.json", import.meta.url)), "utf8"));
  assert.equal(sdk.package, "open-im-sdk-wasm");
  assert.equal(sdk.version, "3.5.1-alpha.3");
  assert.equal(sdk.files.length, 8);
  for (const entry of sdk.files) {
    const row = input.files.find((r) => r.path === `sdk-patches/${entry.path.replaceAll("/", "__")}.patched`);
    assert.equal(row?.sha256, entry.patched_sha256);
  }
  const public2 = [
    { path: "public/openIM.wasm", bytes: 35566446, sha256: "43a4a8929e01415ec9c03b6297137878c7ddab0fe03da31a90d686016396cea7" },
    { path: "public/wasm_exec.js", bytes: 16992, sha256: "0c949f4996f9a89698e4b5c586de32249c3b69b7baadb64d220073cc04acba14" },
  ];
  assert.deepEqual(sdk.coreFiles, public2);
  for (const expected of public2) assert.deepEqual(input.files.find((r) => r.path === expected.path), expected);
  for (const previous of [
    { path: "public/openIM.wasm", bytes: 30202912, sha256: "dff9e28c95e36d9ea95beb20bde7a53d4204c1c2fd2497f19b7c4a504ca665ab" },
    { path: "public/wasm_exec.js", bytes: 16952, sha256: "a3034fedf9b873ef5d9ce5b06f81d911019f245e86bbfb287473ebf22d7908ab" },
  ]) {
    const changed = structuredClone(input);
    Object.assign(changed.files.find((r) => r.path === previous.path), previous);
    assert.throws(() => validateDescriptor(changed));
  }
  for (const mutate of [
    (x) => { x.files[0].sha256 = "0".repeat(64); },
    (x) => { x.files[1].path = x.files[0].path; },
    (x) => { x.cohort = "web4290-build-only-v1"; },
    (x) => { x.cohort = "web4301-build-only-v1"; },
    (x) => { x.cohort = "web4304-build-only-v1"; },
    (x) => { x.cohort = "web4308-build-only-v1"; },
    (x) => { x.cohort = "web4308-build-only-v2"; },
    (x) => { x.cohort = "web4312-build-only-v1"; },
    (x) => { x.cohort = "web4315-build-only-v3"; },
    (x) => { x.cohort = "web4316-build-only-v1"; },
    (x) => { x.cohort = "web4316-build-only-v2"; },
    (x) => { x.cohort = "web4316-build-only-v3"; },
  ]) { const changed = structuredClone(input); mutate(changed); assert.throws(() => validateDescriptor(changed)); }
});

test("actual source inventory rejects an unlisted private environment file and source alias", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "web-build-source-"));
  try {
    writeFileSync(path.join(root, "index.html"), "public");
    const rows = [pin("index.html", "public")]; verifySourceTree(root, rows);
    writeFileSync(path.join(root, ".env.production"), "synthetic");
    assert.throws(() => verifySourceTree(root, rows), /Unexpected\/missing/);
    rmSync(path.join(root, ".env.production")); rmSync(path.join(root, "index.html"));
    writeFileSync(path.join(root, "target"), "public"); symlinkSync(path.join(root, "target"), path.join(root, "index.html"));
    assert.throws(() => verifySourceTree(root, rows), /alias refused/);
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test("actual dist audit preserves known public bytes and CSP and rejects changed WASM or symlink", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "web-build-dist-")), csp = "default-src 'self'";
  try {
    writeFileSync(path.join(root, "index.html"), `<meta http-equiv="Content-Security-Policy" content="${csp}">`);
    writeFileSync(path.join(root, "openIM.wasm"), "synthetic reviewed wasm");
    const publicRows = [pin("public/openIM.wasm", "synthetic reviewed wasm")];
    assert.equal(distManifest(root, publicRows, csp).length, 2);
    writeFileSync(path.join(root, "openIM.wasm"), "changed wasm");
    assert.throws(() => distManifest(root, publicRows, csp), /WASM output changed/);
    rmSync(path.join(root, "openIM.wasm")); symlinkSync(path.join(root, "index.html"), path.join(root, "openIM.wasm"));
    assert.throws(() => distManifest(root, publicRows, csp), /symlink refused/);
  } finally { rmSync(root, { recursive: true, force: true }); }
});
