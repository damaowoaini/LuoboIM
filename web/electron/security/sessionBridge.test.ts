import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
const fixture = vi.hoisted(() => ({
  handlers: new Map<string, (...args: any[]) => any>(),
  main: { id: 1, send: vi.fn(), isDestroyed: () => false },
}));
vi.mock("./ipc", () => ({
  handleTrusted: (channel: string, callback: (...args: any[]) => any) =>
    fixture.handlers.set(channel, callback),
}));
vi.mock("../main/windowManage", () => ({ getWebContents: () => fixture.main }));
vi.mock("electron", () => ({ BrowserWindow: { getAllWindows: () => [] } }));
import { initSessionBridge } from "../main/sessionBridge";
import { accountScope } from "./accountScope";
describe("Electron child session broker", () => {
  let dispose: () => void;
  let child: any;
  const call = (method: string, namespace = "A") =>
    fixture.handlers.get("callDesktopSession")!(
      { sender: child },
      method,
      [],
      namespace,
    );
  const reply = (sender: any, id: string, data: unknown = "result") =>
    fixture.handlers.get("replyDesktopSession")!({ sender }, { id, ok: true, data });
  beforeEach(() => {
    vi.useFakeTimers();
    accountScope.set(undefined);
    accountScope.set("A");
    child = { id: 2, isDestroyed: () => false };
    accountScope.bindWindow(child);
    fixture.main.send.mockClear();
    dispose = initSessionBridge(50);
  });
  afterEach(() => {
    dispose();
    accountScope.set(undefined);
    fixture.handlers.clear();
    vi.useRealTimers();
  });
  it("relays only allowlisted calls and accepts only the main window reply", async () => {
    const result = call("snapshot");
    const sent = fixture.main.send.mock.calls[0][1];
    expect(sent.namespace).toBe("A");
    expect(sent.method).toBe("snapshot");
    expect(() => reply(child, sent.id)).toThrow("仅主窗口");
    reply(fixture.main, sent.id, { namespace: "A", marker: "safe snapshot" });
    expect(await result).toEqual({ namespace: "A", marker: "safe snapshot" });
    expect(() => call("login")).toThrow("无效");
    expect(() => call("snapshot", "B")).toThrow("会话");
  });
  it("rejects a late response after logout and never lends the old window a fresh login of the same account", async () => {
    const result = call("snapshot").catch((error: Error) => error);
    const sent = fixture.main.send.mock.calls[0][1];
    accountScope.set(undefined);
    accountScope.set("A");
    reply(fixture.main, sent.id, "private obsolete data");
    expect(await result).toBeInstanceOf(Error);
    expect(accountScope.ownsWindow(child)).toBe(false);
    expect(() => call("snapshot")).toThrow("会话");
  });
  it("times out unresolved work and refuses a later reply without retaining its result", async () => {
    const result = call("snapshot").catch((error: Error) => error);
    const sent = fixture.main.send.mock.calls[0][1];
    await vi.advanceTimersByTimeAsync(51);
    expect((await result).message).toContain("超时");
    expect(() => reply(fixture.main, sent.id, "late")).not.toThrow();
  });
  it("refuses a window that did not originate from the active main session", () => {
    child = { id: 3, isDestroyed: () => false };
    expect(() => call("snapshot")).toThrow("会话");
  });
});
