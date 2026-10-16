import { productionContentSecurityPolicy } from "./build/contentSecurityPolicy.mts";
import path from "node:path";
import { fileURLToPath } from "node:url";
const root = fileURLToPath(new URL(".", import.meta.url));
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  base: "./",
  define: { "process.env.DRAGGABLE_DEBUG": "false" },
  resolve: { alias: { "@": path.resolve(root, "src") } },
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
  ],
  server: {
    host: "127.0.0.1",
    headers: {
      "Cross-Origin-Embedder-Policy": "credentialless",
      "Cross-Origin-Opener-Policy": "same-origin",
    },
  },
  preview: {
    headers: {
      "Cross-Origin-Embedder-Policy": "credentialless",
      "Cross-Origin-Opener-Policy": "same-origin",
    },
  },
  build: { target: "es2022", sourcemap: false, chunkSizeWarningLimit: 1000 },
});
