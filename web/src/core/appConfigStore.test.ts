// @vitest-environment jsdom
// Real Zustand action, real SessionCoordinator generations and local logout;
// only the API/renderer/storage boundaries are adapted. No HTTP or SDK calls.
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SessionCoordinator, type Session } from "./session/session";
import type { AppConfig } from "@/store/type";
import { ApiError, TransportApiError } from "@/utils/request";
import { decodeAppConfigData } from "./appConfig";
import { appConfigFailureDescription } from "./appConfigFailure";

const f = vi.hoisted(() => ({
  issuer: "https://route-a.example",
  session: undefined as import("./session/session").SessionCoordinator | undefined,
  config: vi.fn(), navigate: vi.fn(), clearContacts: vi.fn(), clearConversations: vi.fn(),
}));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: {
  get generation() { return f.session!.generation; },
  get current() { return f.session!.current; },
  lease: () => f.session!.lease(),
} }));
vi.mock("@/core/session/route", () => ({ getRouteUrl: () => f.issuer }));
vi.mock("@/api/login", () => ({
  BusinessAllowType: { Allow: 1, NotAllow: 2 }, getAppConfig: f.config, getBusinessUserInfo: vi.fn(),
}));
vi.mock("@/api/moments", () => ({ getMomentsUnreadCount: vi.fn() }));
vi.mock("@/layout/MainContentWrap", () => ({ IMSDK: {} }));
vi.mock("@/routes", () => ({ default: { navigate: f.navigate } }));
vi.mock("@/utils/common", () => ({ feedbackToast: vi.fn() }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("@/utils/storage", () => ({
  clearIMProfile: () => f.session!.clear(), getLocale: () => "zh-CN",
  setImageCache: vi.fn(), setLocale: vi.fn(),
}));
vi.mock("@/store/contact", () => ({ useContactStore: { getState: () => ({ clearContactStore: f.clearContacts }) } }));
vi.mock("@/store/conversation", () => ({ useConversationStore: { getState: () => ({ clearConversationStore: f.clearConversations }) } }));
import { useUserStore } from "@/store/user";

type Reply = { data: { config: AppConfig } };
const deferred = () => {
  let resolve!: (value: Reply) => void, reject!: (error: Error) => void;
  const promise = new Promise<Reply>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};
const config = (name: string): AppConfig => ({
  discoverPageURL: `https://${name}.example`, ordinaryUserAddFriend: 1,
  allowSendMsgNotFriend: 1, needInvitationCodeRegister: 0,
});
const reply = (value: AppConfig): Reply => ({ data: { config: value } });
const owner = (name = "owner-a", issuer = f.issuer) => ({
  issuer, userID: name, chatToken: `synthetic-chat-${name}`, imToken: `synthetic-im-${name}`,
  serverInfo: { serverID: "synthetic-tenant", serverName: "Synthetic", domains: ["tenant.example"] },
});
const coordinator = (stored?: Session) => new SessionCoordinator({
  read: async () => stored, write: async () => undefined, clear: async () => undefined,
});
const snapshot = () => ({ appConfig: useUserStore.getState().appConfig, appConfigStatus: useUserStore.getState().appConfigStatus, appConfigFailure: useUserStore.getState().appConfigFailure });
const request = () => useUserStore.getState().getAppConfigByReq();
beforeEach(() => {
  vi.clearAllMocks(); f.config.mockReset(); f.issuer = "https://route-a.example"; f.session = coordinator();
  useUserStore.setState({ appConfig: config("initial"), appConfigStatus: "idle", appConfigFailure: null });
});

describe("real app-config store request publication", () => {
  it("keeps a newer same-generation success when an older request fails late", async () => {
    const a = deferred(), b = deferred(), latest = config("latest");
    f.config.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise);
    const first = request(), second = request();
    expect(useUserStore.getState().appConfigStatus).toBe("loading");
    b.resolve(reply(latest)); await second;
    expect(snapshot()).toEqual({ appConfig: latest, appConfigStatus: "ready", appConfigFailure: null });
    a.reject(new Error("synthetic old failure")); await first;
    expect(snapshot()).toEqual({ appConfig: latest, appConfigStatus: "ready", appConfigFailure: null });
    expect(f.config).toHaveBeenCalledTimes(2);
  });

  it("keeps a newer same-generation failure when an older request succeeds late", async () => {
    const a = deferred(), b = deferred(), initial = useUserStore.getState().appConfig;
    f.config.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise);
    const first = request(), second = request();
    b.reject(new Error("synthetic latest failure")); await second;
    expect(snapshot()).toEqual({ appConfig: initial, appConfigStatus: "error", appConfigFailure: { reason: "unknown" } });
    a.resolve(reply(config("obsolete"))); await first;
    expect(snapshot()).toEqual({ appConfig: initial, appConfigStatus: "error", appConfigFailure: { reason: "unknown" } });
    expect(f.config).toHaveBeenCalledTimes(2);
  });

  it("ignores old success and failure after issuer changes logout owner replacement or same-owner ABA", async () => {
    for (const boundary of ["issuer", "logout", "session-reset", "owner-replacement", "owner-ABA", "issuer-ABA"] as const) {
      for (const outcome of ["success", "failure"] as const) {
        f.config.mockReset(); f.issuer = "https://route-a.example"; f.session = coordinator();
        await f.session.commit(owner());
        const originalGeneration = f.session.generation, slow = deferred();
        f.config.mockReturnValueOnce(slow.promise);
        const pending = request();
        if (boundary === "issuer") f.issuer = "https://route-b.example";
        else if (boundary === "logout") await useUserStore.getState().userLogout(true);
        else if (boundary === "session-reset") f.session.invalidate();
        else if (boundary === "owner-replacement") await f.session.commit(owner("owner-b"));
        else if (boundary === "owner-ABA") {
          await useUserStore.getState().userLogout(true); await f.session.commit(owner());
        } else {
          await useUserStore.getState().userLogout(true); f.issuer = "https://route-b.example";
          await f.session.commit(owner("owner-b"));
          await useUserStore.getState().userLogout(true); f.issuer = "https://route-a.example";
          await f.session.commit(owner());
        }
        if (boundary === "issuer") expect(f.session.generation).toBe(originalGeneration);
        else expect(f.session.generation).toBeGreaterThan(originalGeneration);
        // App/session reset owns the new view. No newer config request is used
        // here, so this tests the issuer/generation fence, not just the ticket.
        const current = config("current");
        useUserStore.setState({ appConfig: current, appConfigStatus: "idle", appConfigFailure: null });
        if (outcome === "success") slow.resolve(reply(config("obsolete")));
        else slow.reject(new Error("synthetic obsolete failure"));
        await pending;
        expect(snapshot(), `${boundary}/${outcome}`).toEqual({ appConfig: current, appConfigStatus: "idle", appConfigFailure: null });
        expect(f.config).toHaveBeenCalledOnce();
      }
    }
  });

  it("publishes current ready and error states and recovers on explicit retry after normal restoration", async () => {
    const stored: Session = { version: 1, issuer: f.issuer, userID: "owner-a",
      chatToken: "synthetic-chat", imToken: "synthetic-im",
      tenant: { serverID: "synthetic-tenant", serverName: "Synthetic", domains: ["tenant.example"], activeLine: 0 } };
    f.session = coordinator(stored);
    const initialGeneration = f.session.generation, ready = config("ready"), recovered = config("recovered");
    f.config.mockImplementationOnce(async () => { await f.session!.restore(); return reply(ready); });
    const first = request(); expect(useUserStore.getState().appConfigStatus).toBe("loading"); await first;
    expect(f.session.current?.userID).toBe("owner-a"); expect(f.session.generation).toBe(initialGeneration);
    expect(snapshot()).toEqual({ appConfig: ready, appConfigStatus: "ready", appConfigFailure: null });
    f.config.mockRejectedValueOnce(new Error("synthetic current transient failure")); await request();
    expect(snapshot()).toEqual({ appConfig: ready, appConfigStatus: "error", appConfigFailure: { reason: "unknown" } });
    f.config.mockResolvedValueOnce(reply(recovered)); const retry = request();
    expect(useUserStore.getState().appConfigStatus).toBe("loading"); await retry;
    expect(snapshot()).toEqual({ appConfig: recovered, appConfigStatus: "ready", appConfigFailure: null });
    expect(f.config).toHaveBeenCalledTimes(3);
  });

  it("keeps only bounded current failure reasons and clears them on retry without publishing upstream private details", async () => {
    const privateText = "synthetic-private URL, token and server body";
    const cases = [
      { error: new TransportApiError("timeout"), expected: { reason: "timeout", code: 503 } },
      { error: new TransportApiError("connection"), expected: { reason: "connection", code: 503 } },
      { error: new ApiError(503, privateText), expected: { reason: "rejected", code: 503 } },
      { error: new ApiError(503, "请求超时，请重试"), expected: { reason: "rejected", code: 503 } },
      { error: new ApiError(401, privateText), expected: { reason: "rejected", code: 401 } },
      { error: new ApiError(502, privateText), expected: { reason: "rejected", code: 502 } },
      { error: new ApiError(Number.MAX_SAFE_INTEGER, privateText), expected: { reason: "rejected" } },
      { error: new Error(privateText), expected: { reason: "unknown" } },
    ];
    for (const { error, expected } of cases) {
      const before = useUserStore.getState().appConfig;
      f.config.mockRejectedValueOnce(error); await request();
      const state = snapshot();
      expect(state).toEqual({ appConfig: before, appConfigStatus: "error", appConfigFailure: expected });
      expect(JSON.stringify(state.appConfigFailure)).not.toContain(privateText);
      expect(appConfigFailureDescription(state.appConfigFailure)).not.toContain(privateText);
      const next = deferred(); f.config.mockReturnValueOnce(next.promise);
      const retry = request();
      expect(snapshot()).toEqual({ appConfig: before, appConfigStatus: "loading", appConfigFailure: null });
      next.resolve(reply(before)); await retry;
      expect(snapshot()).toEqual({ appConfig: before, appConfigStatus: "ready", appConfigFailure: null });
    }
    f.config.mockImplementationOnce(() => decodeAppConfigData(null)); await request();
    expect(useUserStore.getState().appConfigFailure).toEqual({ reason: "response" });
    expect(appConfigFailureDescription(useUserStore.getState().appConfigFailure)).toContain("数据格式异常");
    await useUserStore.getState().userLogout(true);
    expect(useUserStore.getState().appConfigFailure).toBeNull();
  });
});
