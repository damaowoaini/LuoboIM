import path from "node:path";

export { verifyFile, validateReport } from "./web-ci.mjs";

// Resolve each optional package from its actual parent, including the three
// different esbuild and lightningcss versions in the current Admin lockfile.
export function verifyAdminLinuxBindings(lock) {
  if (lock.lockfileVersion !== 3 || !lock.packages) throw new Error("Expected npm lockfile v3");
  const checked = [];
  for (const [parent, value] of Object.entries(lock.packages)) {
    if (value.resolved && new URL(value.resolved).origin !== "https://registry.npmjs.org") throw new Error(`Unreviewed registry/source: ${parent}`);
    for (const [name, version] of Object.entries(value.optionalDependencies ?? {})) {
      if (!name.includes("linux") || !name.includes("x64")) continue;
      let directory = parent;
      let resolved;
      while (true) {
        const candidate = path.posix.join(directory, "node_modules", name);
        if (lock.packages[candidate]) { resolved = candidate; break; }
        if (!directory) break;
        const next = path.posix.dirname(directory);
        directory = next === "." ? "" : next;
        if (path.posix.basename(directory) === "node_modules") directory = path.posix.dirname(directory);
        if (directory === ".") directory = "";
      }
      const item = lock.packages[resolved];
      if (item?.version !== version || !item.os?.includes("linux") || !item.cpu?.includes("x64")) throw new Error(`Missing/resolution-mismatched Linux x64 binding: ${parent} -> ${name}@${version}`);
      checked.push({ parent, package: name, version, resolvedPath: resolved });
    }
  }
  return checked;
}
