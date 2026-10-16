import { defineConfig } from "vitest/config";
import path from "node:path";
import { fileURLToPath } from "node:url";
const root = fileURLToPath(new URL(".", import.meta.url));
export default defineConfig({
  oxc: { jsx: { runtime: "automatic" } },
  resolve: { alias: { "@": path.resolve(root, "src") } },
  test: { include: ["src/**/*.test.ts", "src/**/*.test.tsx", "electron/security/**/*.test.ts"], environment: "node" },
});
