const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const root = path.resolve(__dirname, "..");
const manifest = require("../sdk-patches/manifest.json");
const directory = path.join(root, "node_modules", manifest.package);
if (require(path.join(directory, "package.json")).version !== manifest.version)
  throw new Error("SDK version changed: review the storage/privacy patch before building.");
const hash = (value) => crypto.createHash("sha256").update(value).digest("hex");
// A controlled core and its Go glue must be adopted as the reviewed pair.
if (manifest.coreFiles !== undefined) {
  const expected = new Set(["public/openIM.wasm", "public/wasm_exec.js"]);
  if (!Array.isArray(manifest.coreFiles) || manifest.coreFiles.length !== 2)
    throw new Error("The reviewed WASM and Go glue pair is required.");
  for (const entry of manifest.coreFiles) {
    if (!expected.delete(entry.path)) throw new Error("Unexpected core file.");
    const file = path.join(root, entry.path);
    if (!fs.lstatSync(file).isFile()) throw new Error("Core file must be regular.");
    const value = fs.readFileSync(file);
    if (value.length !== entry.bytes || hash(value) !== entry.sha256)
      throw new Error("Controlled SDK core differs from the reviewed pair: " + entry.path);
  }
}
// Preflight the entire batch before writing any installed file. An explicitly
// pinned previous patch permits an offline upgrade without accepting unknown bytes.
const planned = [];
for (const entry of manifest.files) {
  const destination = path.join(directory, entry.path);
  const current = fs.readFileSync(destination);
  const digest = hash(current);
  const patched = fs.readFileSync(
    path.join(root, "sdk-patches", entry.path.replaceAll("/", "__") + ".patched"),
  );
  if (hash(patched) !== entry.patched_sha256)
    throw new Error("SDK patch integrity check failed: " + entry.path);
  if (digest === entry.patched_sha256) continue;
  if (digest !== entry.original_sha256 && digest !== entry.previous_patched_sha256)
    throw new Error("SDK source differs from the reviewed npm package or previous patch: " + entry.path);
  planned.push({ destination, patched });
}
for (const { destination, patched } of planned) fs.writeFileSync(destination, patched);
if (planned.length)
  fs.rmSync(path.join(root, "node_modules", ".vite"), { recursive: true, force: true });
console.log("Reviewed SDK storage/privacy patch verified.");
