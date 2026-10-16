// @vitest-environment jsdom
// Actual Electron main broker -> actual renderer callback -> actual SDK guard.
// Trusted IPC registration/Electron windows and native transport are explicit
// adapters, as in the existing electron/security/sessionBridge.test.ts.
// AccountScope and SessionCoordinator leases remain real production classes.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
const f = vi.hoisted(() => ({ session: undefined as any, listener: undefined as any,
  handlers: new Map<string, (...args: any[]) => any>(), main: { id: 1, isDestroyed: () => false, send: vi.fn() },
  read: vi.fn(), viewed: vi.fn(), invoke: vi.fn(), deliveries: [] as Promise<unknown>[] }));
vi.mock("./ipc", () => ({ handleTrusted: (name: string, listener: any) => f.handlers.set(name, listener) }));
vi.mock("../main/windowManage", () => ({ getWebContents: () => f.main }));
vi.mock("electron", () => ({ BrowserWindow: { getAllWindows: () => [] } }));
vi.mock("@/core/session/browser", async () => {
  const { SessionCoordinator } = await import("@/core/session/session");
  f.session = new SessionCoordinator({ read: async () => undefined, write: async () => {}, clear: async () => {} });
  return { sessionCoordinator: f.session };
});
vi.mock("@/store/user", () => ({ useUserStore: { getState: () => ({ selfInfo: { userID: "actor" } }) } }));
vi.mock("@/store", async () => ({
  useContactStore: (await import("@/store/contact")).useContactStore,
  useUserStore: { getState: () => ({ selfInfo: { userID: "actor" } }), subscribe: () => () => {} },
  useMessageStore: {},
}));
vi.mock("@/layout/MainContentWrap", async () => {
  const { bindSessionSdk } = await import("@/core/session/sdk");
  const { sessionCoordinator } = await import("@/core/session/browser");
  return { IMSDK: bindSessionSdk({ getBlackList: (...args: any[]) => f.read(...args),
    on: () => {}, off: () => {}, login: async () => {}, logout: async () => {} }, sessionCoordinator) };
});
vi.mock("@/utils/storage", () => ({ getAccessedFriendApplication: f.viewed, getAccessedGroupApplication: f.viewed }));
vi.mock("@/utils/common", () => ({ feedbackToast: () => {} }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("open-im-sdk-wasm", () => ({ ApplicationHandleResult: { Unprocessed: 0, Agree: 1, Reject: -1 } }));
import { initSessionBridge } from "../main/sessionBridge";
import { accountScope } from "./accountScope";
import { bindDesktopMainSdk } from "@/core/session/desktopBridge";
import { IMSDK } from "@/layout/MainContentWrap";

const response = () => ({ issuer: "https://synthetic.example", userID: "actor",
  chatToken: "synthetic-chat-secret", imToken: "synthetic-im-secret",
  serverInfo: { serverID: "synthetic-tenant", serverName: "Synthetic", domains: ["synthetic.example"] } });
let child: any, closeMain: (() => void) | undefined, closeRenderer: (() => void) | undefined;
let oldElectron: typeof window.electronAPI;
beforeEach(async () => {
  vi.resetAllMocks(); f.handlers.clear(); f.deliveries = []; f.listener = undefined;
  f.read.mockResolvedValue({ data: [{ userID: "blocked" }] }); f.viewed.mockResolvedValue([]);
  await f.session.clear(); await f.session.commit(response());
  accountScope.set(undefined); accountScope.set(f.session.namespace);
  child = { id: 2, isDestroyed: () => false }; accountScope.bindWindow(child);
  closeMain = initSessionBridge(1000);
  oldElectron = window.electronAPI;
  (window as any).electronAPI = {
    subscribe: (event: string, callback: any) => {
      if (event === "desktopSessionRequest") f.listener = callback; return () => {};
    },
    ipcInvoke: f.invoke,
  };
  f.invoke.mockImplementation(async (channel: string, value: any) => {
    if (channel === "replyDesktopSession") return f.handlers.get(channel)!({ sender: f.main }, value);
    throw new Error("unexpected synthetic IPC channel");
  });
  closeRenderer = bindDesktopMainSdk(IMSDK);
  f.main.send.mockImplementation((channel: string, value: any) => {
    if (channel !== "desktopSessionRequest") throw new Error("unexpected synthetic delivery");
    f.deliveries.push(Promise.resolve(f.listener(value)));
  });
});
afterEach(async () => {
  closeRenderer?.(); closeRenderer = undefined; closeMain?.(); closeMain = undefined;
  await Promise.allSettled(f.deliveries); accountScope.set(undefined);
  f.handlers.clear(); (window as any).electronAPI = oldElectron;
});
// Normalize only the test-facing synchronous error boundary. The genuine main
// method, whitelist and dispatch are never replaced or recreated.
function call(namespace = f.session.namespace, sender = child): Promise<any> {
  try { return Promise.resolve(f.handlers.get("callDesktopSession")!({ sender }, "getBlackList", [], namespace)); }
  catch (error) { return Promise.reject(error); }
}
describe("blacklist full main-to-renderer read contract", () => {
  it("lets an owned child read in its active namespace through both actual brokers", async () => {
    await expect(call()).resolves.toEqual({ data: [{ userID: "blocked" }] });
    expect(f.main.send).toHaveBeenCalledOnce(); expect(f.read).toHaveBeenCalledOnce();
    expect(f.main.send.mock.calls[0][1]).toMatchObject({ method: "getBlackList", args: [], namespace: f.session.namespace });
  });
  it("rejects a wrong namespace before renderer or SDK dispatch", async () => {
    await expect(call("foreign-namespace")).rejects.toThrow("会话");
    expect(f.main.send).not.toHaveBeenCalled(); expect(f.read).not.toHaveBeenCalled();
  });
  it("rejects a foreign unbound window even with the right namespace", async () => {
    await expect(call(f.session.namespace, { id: 3, isDestroyed: () => false })).rejects.toThrow("会话");
    expect(f.main.send).not.toHaveBeenCalled(); expect(f.read).not.toHaveBeenCalled();
  });
  it("revokes an admitted read and discards its late reply across a real same-account rotation", async () => {
    let resolve: ((value: any) => void) | undefined;
    f.read.mockImplementationOnce(() => new Promise((yes) => { resolve = yes; }));
    const pending = call().then((value) => ({ value }), (error: Error) => ({ error }));
    try {
      // Before has no getBlackList whitelist entry: preserve that assertion
      // failure; it is not proof the old lease admitted this new method.
      expect(f.read).toHaveBeenCalledOnce();
      accountScope.set(undefined); accountScope.set(f.session.namespace);
      await f.session.clear(); await f.session.commit(response());
    } finally {
      if (resolve) resolve({ data: [{ userID: "old-owner-private-row" }] });
      await pending; await Promise.allSettled(f.deliveries);
    }
    expect(await pending).toMatchObject({ error: expect.any(Error) });
    expect(accountScope.ownsWindow(child)).toBe(false);
    await expect(call()).rejects.toThrow("会话");
  });
});
