import { EventEmitter } from "node:events";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
const handlers = vi.hoisted(() => new Map<string, (...args: any[]) => any>());
vi.mock("./ipc", () => ({
  handleTrusted: (channel: string, callback: (...args: any[]) => any) =>
    handlers.set(channel, callback),
}));
import { initDownloadManage } from "../main/downloadManage";
import { accountScope } from "./accountScope";

class Item extends EventEmitter {
  saved = "";
  canceled = false;
  constructor(readonly url: string) {
    super();
  }
  getURLChain() {
    return [this.url];
  }
  getFilename() {
    return "attachment.txt";
  }
  setSavePath(value: string) {
    this.saved = value;
    fs.mkdirSync(path.dirname(value), { recursive: true });
    fs.writeFileSync(value, "partial");
  }
  getTotalBytes() {
    return 10;
  }
  getReceivedBytes() {
    return 5;
  }
  isPaused() {
    return false;
  }
  pause = vi.fn();
  resume = vi.fn();
  cancel() {
    this.canceled = true;
    this.emit("done", {}, "cancelled");
  }
}

describe("Electron download account ownership", () => {
  let directory: string;
  let session: EventEmitter;
  let sender: any;
  let dispose: () => void;
  const url = "https://files.example/asset?signature=preserved%2Bexact";
  const start = (key = "task", sourceUrl = url, namespace = "A") =>
    handlers.get("startDownload")!({ sender }, key, { sourceUrl }, namespace);
  const arriving = (item: Item) => {
    const event = { preventDefault: vi.fn() };
    session.emit("will-download", event, item, sender);
    return event;
  };
  beforeEach(() => {
    directory = fs.mkdtempSync(path.join(os.tmpdir(), "luobo-download-"));
    session = new EventEmitter();
    sender = {
      id: 7,
      session,
      send: vi.fn(),
      downloadURL: vi.fn(),
      isDestroyed: () => false,
    };
    (globalThis as any).pathConfig = { fileCachePath: path.join(directory, "A") };
    accountScope.set(undefined);
    accountScope.set("A");
    dispose = initDownloadManage(sender);
  });
  afterEach(() => {
    dispose();
    accountScope.set(undefined);
    handlers.clear();
    fs.rmSync(directory, { recursive: true, force: true });
  });

  it("captures the owner before connection, preserves signed source URL, and refuses a late A download in B", () => {
    start();
    expect(sender.downloadURL).toHaveBeenCalledWith(url);
    accountScope.set("B");
    (globalThis as any).pathConfig.fileCachePath = path.join(directory, "B");
    expect(() => start("task B", url, "B")).toThrow("前次下载");
    const old = new Item(url);
    expect(arriving(old).preventDefault).toHaveBeenCalledOnce();
    expect(old.saved).toBe("");
    expect(sender.send).not.toHaveBeenCalled();
    start("task B", url, "B");
    const current = new Item(url);
    arriving(current);
    expect(current.saved.startsWith(path.join(directory, "B") + path.sep)).toBe(true);
  });
  it("cancels active work and removes only its partial file on logout; suppresses late progress/completion", () => {
    start();
    const item = new Item(url);
    arriving(item);
    expect(fs.existsSync(item.saved)).toBe(true);
    const retained = path.join(directory, "A", "retained.txt");
    fs.writeFileSync(retained, "retained");
    accountScope.set(undefined);
    expect(item.canceled).toBe(true);
    expect(fs.existsSync(item.saved)).toBe(false);
    expect(fs.existsSync(retained)).toBe(true);
    item.emit("updated", {}, "progressing");
    item.emit("done", {}, "completed");
    expect(sender.send).not.toHaveBeenCalled();
    expect(() => start()).toThrow("会话已切换");
  });
  it("rejects unsolicited downloads and forged old-owner controls; emits completion only for its owner", () => {
    expect(arriving(new Item(url)).preventDefault).toHaveBeenCalledOnce();
    start();
    const item = new Item(url);
    arriving(item);
    expect(() => handlers.get("resumeDownload")!({ sender }, "task", "B")).toThrow(
      "会话已切换",
    );
    expect(item.resume).not.toHaveBeenCalled();
    item.emit("done", {}, "completed");
    expect(sender.send).toHaveBeenCalledWith("downloadSuccess", "task", item.saved);
    expect(fs.existsSync(item.saved)).toBe(true);
  });
  it("blocks a downgrade redirect before setting a path", () => {
    start();
    const item = new Item(url);
    item.getURLChain = () => [url, "http://files.example/asset"];
    expect(arriving(item).preventDefault).toHaveBeenCalledOnce();
    expect(item.saved).toBe("");
    expect(sender.send).toHaveBeenCalledWith("downloadFailed", "task");
  });
});
