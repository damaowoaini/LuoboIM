// @vitest-environment jsdom
// Actual bridge callback + SessionCoordinator leases + SDK generation wrapper.
// Electron subscription/reply transport and the native read are explicit mocks.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
const f = vi.hoisted(() => ({ session: undefined as any, listener: undefined as any,
  read: vi.fn(), invoke: vi.fn(), feedback: vi.fn(), viewed: vi.fn() }));
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
vi.mock("@/utils/common", () => ({ feedbackToast: f.feedback }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("open-im-sdk-wasm", () => ({ ApplicationHandleResult: { Unprocessed: 0, Agree: 1, Reject: -1 } }));
import { bindDesktopMainSdk } from "@/core/session/desktopBridge";
import { IMSDK } from "@/layout/MainContentWrap";

const response = (id: string) => ({ issuer: "https://synthetic.example", userID: id,
  chatToken: "synthetic-chat-secret", imToken: "synthetic-im-secret",
  serverInfo: { serverID: "synthetic-tenant", serverName: "Synthetic", domains: ["synthetic.example"] } });
let unbind: (() => void) | undefined, oldElectron: typeof window.electronAPI;
beforeEach(async () => {
  vi.resetAllMocks(); f.listener = undefined; f.read.mockResolvedValue({ data: [{ userID: "blocked" }] });
  f.invoke.mockResolvedValue(undefined); f.viewed.mockResolvedValue([]);
  await f.session.clear(); await f.session.commit(response("actor"));
  oldElectron = window.electronAPI;
  (window as any).electronAPI = { subscribe: (event: string, callback: any) => {
    if (event === "desktopSessionRequest") f.listener = callback; return () => {};
  }, ipcInvoke: f.invoke };
  unbind = bindDesktopMainSdk(IMSDK);
});
afterEach(() => { unbind?.(); unbind = undefined; (window as any).electronAPI = oldElectron; });
function request(namespace = f.session.namespace) {
  return f.listener({ id: "synthetic-read", namespace, method: "getBlackList", args: [] });
}
describe("preserved desktop third-window read contract", () => {
  it("allows a normal same-namespace read through the actual bridge", async () => {
    await request(); expect(f.read).toHaveBeenCalledOnce();
    expect(f.invoke).toHaveBeenCalledWith("replyDesktopSession", {
      id: "synthetic-read", ok: true, data: { data: [{ userID: "blocked" }] },
    });
  });
  it("preserves wrong-namespace rejection before any native read", async () => {
    await request("foreign-namespace"); expect(f.read).not.toHaveBeenCalled();
    expect(f.invoke).toHaveBeenCalledWith("replyDesktopSession", { id: "synthetic-read", ok: false });
  });
  it("does not return an admitted old-session response after a real coordinator rotation", async () => {
    let resolve: ((value: any) => void) | undefined;
    f.read.mockReturnValueOnce(new Promise((yes) => { resolve = yes; }));
    const pending = request();
    try { expect(f.read).toHaveBeenCalledOnce(); await f.session.commit(response("next-actor")); }
    finally {
      // The real before whitelist rejects before reaching the native read.
      // Preserve its dispatch assertion failure without a second cleanup error.
      if (resolve) resolve({ data: [{ userID: "old-owner-blocked" }] });
      await pending;
    }
    expect(f.invoke).toHaveBeenCalledWith("replyDesktopSession", { id: "synthetic-read", ok: false });
    expect(f.invoke.mock.calls.some((call: any[]) => call[1]?.ok === true)).toBe(false);
  });
});
