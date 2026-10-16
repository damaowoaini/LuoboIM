import { fileURLToPath } from "node:url";
import { builtinModules } from "node:module";
import path from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import electron from "vite-plugin-electron";
import { createRequire } from "node:module";
const pkg = createRequire(import.meta.url)("./package.json");
import { productionContentSecurityPolicy } from "./build/contentSecurityPolicy.mts";
const root = fileURLToPath(new URL(".", import.meta.url));
const external = [
  "electron",
  ...builtinModules,
  ...builtinModules.map((name) => `node:${name}`),
  ...Object.keys(pkg.dependencies),
];
const target = (entry: string, outDir: string) => ({
  entry,
  vite: {
    build: {
      outDir,
      emptyOutDir: true,
      target: "node24",
      sourcemap: false,
      lib: { entry, formats: ["cjs"] as ["cjs"], fileName: () => "index.js" },
      rolldownOptions: {
        external,
        output: { format: "cjs" as const, entryFileNames: "index.js" },
      },
    },
  },
  onstart: () => {}, // Development startup must explicitly preserve Electron sandbox defaults.
});
export default defineConfig({
  base: "./",
  define: { "process.env.DRAGGABLE_DEBUG": "false" },
  resolve: { alias: { "@": path.join(root, "src") } },
  plugins: [
    react(),
    {
      name: "production-csp",
      apply: "build",
      transformIndexHtml: () => [
        {
          tag: "meta",
          attrs: {
            "http-equiv": "Content-Security-Policy",
            content: productionContentSecurityPolicy,
          },
          injectTo: "head-prepend",
        },
      ],
    },
    electron([
      target("electron/main/index.ts", "dist-electron/main"),
      target("electron/preload/index.ts", "dist-electron/preload"),
    ]),
  ],
  server: {
    host: "127.0.0.1",
    headers: {
      "Cross-Origin-Embedder-Policy": "credentialless",
      "Cross-Origin-Opener-Policy": "same-origin",
    },
  },
  build: { target: "es2022", sourcemap: false, chunkSizeWarningLimit: 1000 },
});
