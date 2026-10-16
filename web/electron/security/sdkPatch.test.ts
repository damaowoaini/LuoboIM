import { it, expect } from "vitest";
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
  cpSync,
  rmSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { execFileSync } from "node:child_process";

it("replays the reviewed SDK patch idempotently and rejects changed versions or bytes", () => {
  const root = mkdtempSync(join(tmpdir(), "luobo-sdk-patch-"));
  const manifest = JSON.parse(readFileSync("sdk-patches/manifest.json", "utf8"));
  const sdk = join(root, "node_modules", manifest.package);
  try {
    mkdirSync(join(root, "tool"), { recursive: true });
    cpSync(resolve("tool/prepare-sdk.cjs"), join(root, "tool/prepare-sdk.cjs"));
    cpSync(resolve("sdk-patches"), join(root, "sdk-patches"), { recursive: true });
    const restore = () => {
      mkdirSync(sdk, { recursive: true });
      writeFileSync(
        join(sdk, "package.json"),
        JSON.stringify({ version: manifest.version }),
      );
      for (const entry of manifest.files) {
        mkdirSync(join(sdk, entry.path, ".."), { recursive: true });
        cpSync(
          join(root, "sdk-patches", entry.path.replaceAll("/", "__") + ".original"),
          join(sdk, entry.path),
        );
      }
    };
    const run = () =>
      execFileSync(process.execPath, [join(root, "tool/prepare-sdk.cjs")], {
        stdio: "pipe",
      });
    restore();
    run();
    run();
    for (const entry of manifest.files)
      expect(readFileSync(join(sdk, entry.path))).toEqual(
        readFileSync(
          join(root, "sdk-patches", entry.path.replaceAll("/", "__") + ".patched"),
        ),
      );
    writeFileSync(
      join(sdk, "package.json"),
      JSON.stringify({ version: "unexpected-version" }),
    );
    expect(run).toThrow();
    restore();
    writeFileSync(join(sdk, manifest.files[0].path), "unexpected installed source");
    expect(run).toThrow();
    restore();
    writeFileSync(
      join(
        root,
        "sdk-patches",
        manifest.files[0].path.replaceAll("/", "__") + ".patched",
      ),
      "corrupt patch",
    );
    expect(run).toThrow();
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
