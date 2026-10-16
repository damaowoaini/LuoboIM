// Two selected production paths; actual Drawer/edit/select callers use real base source.
import { defineConfig } from "../web/node_modules/vitest/dist/config.js";
import path from "node:path";
import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
const ci = path.dirname(fileURLToPath(import.meta.url));
const here = path.join(ci, "candidates", "web-direct-edit");
const web = path.resolve(ci, "../web");
const stage = process.env.LUOBO_GROUP_DIRECT_EDIT_STAGE;
if (stage !== "before" && stage !== "after") throw new Error("Explicit before|after stage required");
const source = path.join(here, stage, "src");
const baseSource = path.join(web, "src");
const selected = new Set(["pages/chat/queryChat/GroupSetting/GroupSettings.tsx", "pages/chat/queryChat/GroupSetting/useGroupSettings.tsx"]);
export default defineConfig({
  root: here, cacheDir: path.join(here, "test-cache-" + stage),
  oxc: { jsx: { runtime: "automatic" } },
  plugins: [{ name: "group-direct-edit-real-relative-dependencies", enforce: "pre", resolveId(id, importer) {
    if (!importer || !id.startsWith(".")) return;
    const clean = importer.split("?")[0];
    const origin = clean.startsWith(source + path.sep) ? source : clean.startsWith(baseSource + path.sep) ? baseSource : undefined;
    if (!origin) return;
    const target = path.resolve(path.dirname(clean), id);
    const relative = path.relative(origin, target);
    if (relative.startsWith("..") || path.isAbsolute(relative)) return;
    const extensions = ["", ".ts", ".tsx", ".d.ts", "/index.ts", "/index.tsx"];
    const selectedRelative = extensions.map((extension) => relative + extension).find((name) => selected.has(name));
    if (selectedRelative) return path.join(source, selectedRelative);
    if (origin === baseSource) return;
    if (extensions.some((extension) => existsSync(target + extension))) return;
    return extensions.map((extension) => path.join(web, "src", relative) + extension).find(existsSync);
  } }],
  resolve: { dedupe: ["react", "react-dom", "zustand"], alias: [
    { find: /^@\/pages\/chat\/queryChat\/GroupSetting\/GroupSettings$/, replacement: path.join(source, "pages/chat/queryChat/GroupSetting/GroupSettings.tsx") },
    { find: /^@\/pages\/chat\/queryChat\/GroupSetting\/useGroupSettings$/, replacement: path.join(source, "pages/chat/queryChat/GroupSetting/useGroupSettings.tsx") },
    { find: "@", replacement: path.join(web, "src") },
    { find: /^react$/, replacement: path.join(web, "node_modules/react/index.js") },
    { find: /^react\/jsx-runtime$/, replacement: path.join(web, "node_modules/react/jsx-runtime.js") },
    { find: /^react\/jsx-dev-runtime$/, replacement: path.join(web, "node_modules/react/jsx-dev-runtime.js") },
    { find: /^react-dom\/client$/, replacement: path.join(web, "node_modules/react-dom/client.js") },
    { find: /^react-dom$/, replacement: path.join(web, "node_modules/react-dom/index.js") },
    { find: /^zustand$/, replacement: path.join(web, "node_modules/zustand/index.js") },
    { find: /^react-use$/, replacement: path.join(web, "node_modules/react-use/lib/index.js") },
    { find: /^uuid$/, replacement: path.join(web, "node_modules/uuid/dist-node/index.js") },
    { find: /^vitest$/, replacement: path.join(web, "node_modules/vitest/dist/index.js") },
    { find: /^i18next$/, replacement: path.join(web, "node_modules/i18next/dist/esm/i18next.js") },
    { find: /^antd$/, replacement: path.join(web, "node_modules/antd/es/index.js") },
    { find: /^ahooks$/, replacement: path.join(web, "node_modules/ahooks/es/index.js") },
    { find: /^react-virtuoso$/, replacement: path.join(web, "node_modules/react-virtuoso/dist/index.mjs") },
    { find: /^clsx$/, replacement: path.join(web, "node_modules/clsx/dist/clsx.m.js") },
    { find: /^@ant-design\/icons$/, replacement: path.join(web, "node_modules/@ant-design/icons/es/index.js") },
    { find: /^open-im-sdk-wasm$/, replacement: path.join(web, "node_modules/open-im-sdk-wasm/lib/index.js") },
  ] },
  test: { include: ["tests/*.test.ts"], environment: "jsdom", pool: "threads",
    cache: false, fsModuleCache: false, maxWorkers: 1, fileParallelism: false },
});
