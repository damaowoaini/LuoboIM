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

test("reviewed4308 descriptor refuses changed source, duplicate paths and historical identity", () => {
  assert.equal(validateDescriptor(input).sourceFiles, 4308);
  for (const mutate of [
    (x) => { x.files[0].sha256 = "0".repeat(64); },
    (x) => { x.files[1].path = x.files[0].path; },
    (x) => { x.cohort = "web4290-build-only-v1"; },
    (x) => { x.cohort = "web4301-build-only-v1"; },
    (x) => { x.cohort = "web4304-build-only-v1"; },
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
