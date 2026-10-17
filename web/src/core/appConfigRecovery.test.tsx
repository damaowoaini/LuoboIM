// @vitest-environment jsdom
// Real Login effects, Zustand action and SessionCoordinator generations. Only
// transport, storage and unrelated leaf UI are adapted; no HTTP or SDK runs.
import React, { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";
import { SessionCoordinator } from "./session/session";
import { AppConfigResponseError } from "./appConfig";
import type { AppConfig } from "@/store/type";
import type { AppConfigFailure } from "./appConfigFailure";
import { ApiError, TransportApiError } from "@/utils/request";

const f = vi.hoisted(() => ({
  issuer: "https://route-a.example",
  session: undefined as import("./session/session").SessionCoordinator | undefined,
  config: vi.fn(),
}));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: {
  get generation() { return f.session!.generation; },
  get current() { return f.session!.current; },
  lease: () => f.session!.lease(),
} }));
vi.mock("@/core/session/route", () => ({ getRouteUrl: () => f.issuer }));
vi.mock("@/core/session/loginFailure", () => ({ readSdkLoginFailure: () => undefined }));
vi.mock("@/api/login", () => ({
  BusinessAllowType: { Allow: 1, NotAllow: 2 }, getAppConfig: f.config, getBusinessUserInfo: vi.fn(),
}));
vi.mock("@/api/moments", () => ({ getMomentsUnreadCount: vi.fn() }));
vi.mock("@/layout/MainContentWrap", () => ({ IMSDK: {} }));
vi.mock("@/routes", () => ({ default: { navigate: vi.fn() } }));
vi.mock("@/utils/common", () => ({ feedbackToast: vi.fn() }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("@/utils/storage", () => ({
  clearIMProfile: () => f.session!.clear(), getLocale: () => "zh-CN",
  setImageCache: vi.fn(), setLocale: vi.fn(), getLoginMethod: () => "phone", setLoginMethod: vi.fn(),
}));
vi.mock("@/store/contact", () => ({ useContactStore: { getState: () => ({ clearContactStore: vi.fn() }) } }));
vi.mock("@/store/conversation", () => ({ useConversationStore: { getState: () => ({ clearConversationStore: vi.fn() }) } }));
vi.mock("@/store", async () => ({ useUserStore: (await import("@/store/user")).useUserStore }));
vi.mock("react-router-dom", () => ({ useSearchParams: () => [new URLSearchParams(), vi.fn()] }));
vi.mock("@/config", () => ({ APP_VERSION: "synthetic-version" }));
vi.mock("@ant-design/icons", () => ({ SettingOutlined: () => null }));
vi.mock("antd", () => ({
  Alert: ({ message, description, action }: { message?: ReactNode; description?: ReactNode; action?: ReactNode }) =>
    <section>{message}{description}{action}</section>,
  Button: ({ children, onClick, "aria-label": label }: { children?: ReactNode; onClick?: () => void; "aria-label"?: string }) =>
    <button onClick={onClick} aria-label={label}>{children}</button>,
}));
vi.mock("@/components/WindowControlBar", () => ({ default: () => null }));
vi.mock("@/pages/login/ConfigModal", () => ({ default: () => null }));
vi.mock("@/pages/login/LoginForm", () => ({ default: () => null }));
vi.mock("@/pages/login/ModifyForm", () => ({ default: () => null }));
vi.mock("@/pages/login/RegisterForm", () => ({ default: () => null }));

import { useUserStore } from "@/store/user";
import { Login } from "@/pages/login";

type Reply = { data: { config: AppConfig } };
const config = (): AppConfig => ({
  discoverPageURL: "https://synthetic.example", ordinaryUserAddFriend: 1,
  allowSendMsgNotFriend: 1, needInvitationCodeRegister: 0,
});
const reply = (): Reply => ({ data: { config: config() } });
function deferred() {
  let resolve!: (value: Reply) => void;
  const promise = new Promise<Reply>(yes => { resolve = yes; });
  return { promise, resolve };
}
let root: Root | undefined, host: HTMLDivElement, visibility: DocumentVisibilityState;
let windowAdd: MockInstance<Window["addEventListener"]>;
let windowRemove: MockInstance<Window["removeEventListener"]>;
let documentAdd: MockInstance<Document["addEventListener"]>;
let documentRemove: MockInstance<Document["removeEventListener"]>;

async function flush() { await Promise.resolve(); await Promise.resolve(); }
async function mount() {
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  await act(async () => { root!.render(<Login />); await flush(); });
}
async function stop() {
  if (root) await act(async () => { root!.unmount(); });
  root = undefined; host?.remove();
}
async function transient(reason: "timeout" | "connection" = "connection") {
  f.config.mockRejectedValueOnce(new TransportApiError(reason));
  await useUserStore.getState().getAppConfigByReq();
  expect(useUserStore.getState().appConfigFailure?.reason).toBe(reason);
  f.config.mockClear();
}
function handlers() {
  const online = windowAdd.mock.calls.filter(([type]) => type === "online");
  const visible = documentAdd.mock.calls.filter(([type]) => type === "visibilitychange");
  expect(online).toHaveLength(1); expect(visible).toHaveLength(1);
  return { online: online[0][1], visible: visible[0][1] };
}
function removed(value: ReturnType<typeof handlers>) {
  expect(windowRemove).toHaveBeenCalledWith("online", value.online);
  expect(documentRemove).toHaveBeenCalledWith("visibilitychange", value.visible);
}
function visibilityEvent(next: DocumentVisibilityState) {
  visibility = next; document.dispatchEvent(new Event("visibilitychange"));
}
function invokeRetired(listener: EventListenerOrEventListenerObject) {
  const event = new Event("online");
  if (typeof listener === "function") listener.call(window, event);
  else listener.handleEvent(event);
}

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  f.config.mockReset(); f.issuer = "https://route-a.example";
  f.session = new SessionCoordinator({ read: async () => undefined, write: async () => undefined, clear: async () => undefined });
  useUserStore.setState({ appConfig: config(), appConfigStatus: "idle", appConfigFailure: null });
  visibility = "visible";
  vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
  windowAdd = vi.spyOn(window, "addEventListener"); windowRemove = vi.spyOn(window, "removeEventListener");
  documentAdd = vi.spyOn(document, "addEventListener"); documentRemove = vi.spyOn(document, "removeEventListener");
});
afterEach(async () => { await stop(); vi.restoreAllMocks(); });

describe("real login configuration event recovery", () => {
  it("recovers a visible current transient error on online or hidden-to-visible without concurrent requests", async () => {
    await transient("timeout"); await mount(); const first = handlers();
    expect(host.textContent).toContain("暂时无法获取服务配置");
    expect(f.config).not.toHaveBeenCalled();
    const slow = deferred(); f.config.mockReturnValueOnce(slow.promise);
    await act(async () => {
      visibilityEvent("hidden"); window.dispatchEvent(new Event("online")); await flush();
    });
    expect(f.config).not.toHaveBeenCalled();
    await act(async () => {
      visibilityEvent("visible"); window.dispatchEvent(new Event("online"));
      visibilityEvent("hidden"); visibilityEvent("visible"); await flush();
    });
    expect(f.config).toHaveBeenCalledTimes(1);
    expect(useUserStore.getState().appConfigStatus).toBe("loading"); removed(first);
    await act(async () => { slow.resolve(reply()); await flush(); });
    expect(useUserStore.getState().appConfigStatus).toBe("ready");
    expect(host.textContent).not.toContain("暂时无法获取服务配置");
    await act(async () => { window.dispatchEvent(new Event("online")); visibilityEvent("hidden"); visibilityEvent("visible"); await flush(); });
    expect(f.config).toHaveBeenCalledTimes(1);

    // A later real transport failure creates a fresh listener, not a timer.
    await act(async () => { await transient(); });
    const next = deferred(); f.config.mockReturnValueOnce(next.promise);
    await act(async () => { window.dispatchEvent(new Event("online")); window.dispatchEvent(new Event("online")); await flush(); });
    expect(f.config).toHaveBeenCalledTimes(1);
    await act(async () => { next.resolve(reply()); await flush(); });
    expect(useUserStore.getState().appConfigStatus).toBe("ready");
  });

  it("removes recovery listeners after ready or unmount and refuses retired issuer and generation handlers", async () => {
    for (const boundary of ["issuer", "generation"] as const) {
      f.issuer = "https://route-a.example"; await transient();
      windowAdd.mockClear(); documentAdd.mockClear(); await mount(); const original = handlers();
      if (boundary === "issuer") f.issuer = "https://route-b.example";
      else f.session!.invalidate();
      await act(async () => { window.dispatchEvent(new Event("online")); visibilityEvent("hidden"); visibilityEvent("visible"); await flush(); });
      expect(f.config, boundary).not.toHaveBeenCalled();
      await stop(); removed(original);
      // Even a retained callback cannot recover a new issuer/generation. After
      // unmount it stays retired when the same visible error remains current.
      await act(async () => { invokeRetired(original.online); await flush(); });
      expect(f.config, boundary).not.toHaveBeenCalled();
    }
    await transient(); windowAdd.mockClear(); documentAdd.mockClear(); await mount();
    const current = handlers(); f.config.mockResolvedValueOnce(reply());
    const retry = [...host.querySelectorAll<HTMLButtonElement>("button")].filter(button => button.textContent === "重试");
    expect(retry).toHaveLength(1);
    await act(async () => { retry[0].click(); await flush(); });
    expect(f.config).toHaveBeenCalledTimes(1); expect(useUserStore.getState().appConfigStatus).toBe("ready"); removed(current);
  });

  it("never auto retries loading ready business malformed or unknown config failures", async () => {
    const states: { status: "idle" | "loading" | "ready" | "error"; error?: Error; failure?: AppConfigFailure }[] = [
      { status: "idle" }, { status: "loading" }, { status: "ready" },
      { status: "error", error: new ApiError(503, "请求超时，请重试"), failure: { reason: "rejected", code: 503 } },
      { status: "error", error: new ApiError(500, "网络连接失败，请重试"), failure: { reason: "rejected", code: 500 } },
      { status: "error", error: new AppConfigResponseError(), failure: { reason: "response" } },
      { status: "error", error: new Error("synthetic unknown"), failure: { reason: "unknown" } },
    ];
    for (const state of states) {
      if (state.error) {
        f.config.mockRejectedValueOnce(state.error); await useUserStore.getState().getAppConfigByReq();
        expect(useUserStore.getState().appConfigFailure).toEqual(state.failure);
      } else useUserStore.setState({ appConfigStatus: state.status, appConfigFailure: null });
      f.config.mockClear(); windowAdd.mockClear(); documentAdd.mockClear(); await mount();
      expect(windowAdd.mock.calls.filter(([type]) => type === "online")).toHaveLength(0);
      expect(documentAdd.mock.calls.filter(([type]) => type === "visibilitychange")).toHaveLength(0);
      await act(async () => { window.dispatchEvent(new Event("online")); visibilityEvent("hidden"); visibilityEvent("visible"); await flush(); });
      expect(f.config).not.toHaveBeenCalled(); await stop();
    }
  });
});
