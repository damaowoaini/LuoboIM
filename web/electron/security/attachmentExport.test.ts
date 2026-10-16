import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { exportAttachment } from "./attachmentExport";

describe("native attachment export", () => {
  let directory: string;
  let root: string;
  let source: string;
  beforeEach(() => {
    directory = fs.mkdtempSync(path.join(os.tmpdir(), "luobo-export-test-"));
    root = path.join(directory, "owner-cache"); fs.mkdirSync(root);
    source = path.join(root, "attachment.txt"); fs.writeFileSync(source, "synthetic attachment\n");
  });
  afterEach(() => fs.rmSync(directory, { recursive: true, force: true }));
  const owner = () => undefined;
  it("copies exact bytes only to the destination returned by the native dialog", async () => {
    const target = path.join(directory, "chosen.txt");
    const chooseDestination = vi.fn(async () => target);
    expect(await exportAttachment({ sourcePath: source, filename: "../report.txt", roots: [root], assertOwner: owner, chooseDestination }))
      .toEqual({ saved: true, canceled: false });
    expect(chooseDestination).toHaveBeenCalledWith("report.txt");
    expect(fs.readFileSync(target)).toEqual(fs.readFileSync(source));
    expect(fs.readdirSync(directory).some(name => name.startsWith(".luobo-export-"))).toBe(false);
  });
  it("cancels without writing and rejects another owner's file or a symlink escape", async () => {
    const chooseDestination = vi.fn(async () => undefined);
    expect(await exportAttachment({ sourcePath: source, roots: [root], assertOwner: owner, chooseDestination }))
      .toEqual({ saved: false, canceled: true });
    const foreign = path.join(directory, "foreign.txt"); fs.writeFileSync(foreign, "foreign");
    const link = path.join(root, "escape"); fs.symlinkSync(foreign, link);
    chooseDestination.mockClear();
    for (const sourcePath of [foreign, link]) {
      await expect(exportAttachment({ sourcePath, roots: [root], assertOwner: owner, chooseDestination })).rejects.toThrow("当前账号缓存");
    }
    expect(chooseDestination).not.toHaveBeenCalled();
  });
  it("cannot write if the account changes while the native dialog is open", async () => {
    const target = path.join(directory, "should-not-exist.txt");
    let current = true;
    await expect(exportAttachment({ sourcePath: source, roots: [root],
      assertOwner: () => { if (!current) throw new Error("会话已切换"); },
      chooseDestination: async () => { current = false; return target; },
    })).rejects.toThrow("会话已切换");
    expect(fs.existsSync(target)).toBe(false);
  });
  it("removes only its temporary copy when the owner changes before commit", async () => {
    const target = path.join(directory, "retained.txt"); fs.writeFileSync(target, "existing destination");
    let checks = 0;
    await expect(exportAttachment({ sourcePath: source, roots: [root],
      assertOwner: () => { if (++checks === 3) throw new Error("会话已切换"); },
      chooseDestination: async () => target,
    })).rejects.toThrow("会话已切换");
    expect(fs.readFileSync(target, "utf8")).toBe("existing destination");
    expect(fs.readFileSync(source, "utf8")).toBe("synthetic attachment\n");
    expect(fs.readdirSync(directory).some(name => name.startsWith(".luobo-export-"))).toBe(false);
  });
});
