const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const root = path.resolve(__dirname, "..");
const manifest = require("../sdk-patches/manifest.json");
const directory = path.join(root, "node_modules", manifest.package);
if (require(path.join(directory, "package.json")).version !== manifest.version)
  throw new Error(
    "SDK version changed: review the storage/privacy patch before building.",
  );
const hash = (value) => crypto.createHash("sha256").update(value).digest("hex");
let changed = false;
for (const entry of manifest.files) {
  const destination = path.join(directory, entry.path);
  const current = fs.readFileSync(destination);
  if (hash(current) === entry.patched_sha256) continue;
  if (hash(current) !== entry.original_sha256)
    throw new Error("SDK source differs from the reviewed npm package: " + entry.path);
  const patched = fs.readFileSync(
    path.join(root, "sdk-patches", entry.path.replaceAll("/", "__") + ".patched"),
  );
  if (hash(patched) !== entry.patched_sha256)
    throw new Error("SDK patch integrity check failed: " + entry.path);
  fs.writeFileSync(destination, patched);
  changed = true;
}
if (changed)
  fs.rmSync(path.join(root, "node_modules", ".vite"), { recursive: true, force: true });
console.log("Reviewed SDK storage/privacy patch verified.");
