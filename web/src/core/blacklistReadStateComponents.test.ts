// @vitest-environment jsdom
// Actual BlackListContent/BlackItem + real Zustand getters + SDK generation
// wrapper. Only native transport, session, toast and visual primitives adapt.
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
const f = vi.hoisted(() => ({ owner: {} as any, generation: 1,
  user: { selfInfo: { userID: "actor" as string | undefined } }, read: vi.fn(), remove: vi.fn(),
  viewed: vi.fn(), feedback: vi.fn(), buttons: new Map<string, () => Promise<void>>() }));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: {
  get current() { return f.owner; }, get generation() { return f.generation; }, onChange: () => () => undefined,
} }));
vi.mock("@/store/user", () => ({ useUserStore: Object.assign((select: any) => select(f.user), { getState: () => f.user }) }));
vi.mock("@/layout/MainContentWrap", async () => {
  const { bindSessionSdk } = await import("@/core/session/sdk");
  const { sessionCoordinator } = await import("@/core/session/browser");
  return { IMSDK: bindSessionSdk({ getBlackList: f.read, removeBlack: f.remove,
    on: () => {}, off: () => {}, login: async () => {}, logout: async () => {} }, sessionCoordinator) };
});
vi.mock("@/utils/storage", () => ({ getAccessedFriendApplication: f.viewed, getAccessedGroupApplication: f.viewed }));
vi.mock("@/utils/common", () => ({ feedbackToast: f.feedback }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("open-im-sdk-wasm", () => ({ ApplicationHandleResult: { Unprocessed: 0, Agree: 1, Reject: -1 } }));
vi.mock("@/components/OIMAvatar", () => ({ default: () => null }));
vi.mock("@ant-design/icons", () => ({ CloseOutlined: () => null }));
vi.mock("antd", () => ({
  Button: ({ children, disabled, loading, onClick }: any) => {
    if (typeof children === "string") f.buttons.set(children, onClick);
    return React.createElement("button", { disabled: disabled || loading, onClick }, children);
  },
  Spin: () => React.createElement("span", { "data-testid": "loading" }),
  Empty: () => React.createElement("div", { "data-testid": "empty" }, "confirmed-empty"),
  Modal: ({ children }: any) => React.createElement("div", null, children),
}));
import { useContactStore } from "@/store/contact";
import { BlackListContent } from "@/layout/LeftNavBar/BlackList";
const state = () => useContactStore.getState();
const one = { userID: "blocked-one", nickname: "first blocked person", faceURL: "" } as any;
const two = { ...one, userID: "blocked-two", nickname: "second blocked person" } as any;
let root: Root, host: HTMLDivElement, disposed: boolean;
function deferred() { let resolve!: (v: any) => void, reject!: (v: any) => void;
  const promise = new Promise<any>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
async function render() { await act(async () => root.render(React.createElement(BlackListContent))); }
const empty = () => host.querySelector('[data-testid="empty"]');
const text = () => host.textContent ?? "";
function callback(key: "retry" | "placeholder.remove") {
  const value = f.buttons.get(key); expect(value).toBeTypeOf("function"); return value!;
}
async function loaded(rows = [one]) { f.read.mockResolvedValueOnce({ data: rows }); await state().getBlackListByReq(); }
beforeEach(() => {
  vi.resetAllMocks(); f.buttons.clear(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  f.owner = { userID: "actor", issuer: "issuer-A", tenant: { serverID: "tenant-A" } };
  f.generation = 1; f.user.selfInfo.userID = "actor";
  f.read.mockResolvedValue({ data: [] }); f.remove.mockResolvedValue({ data: {} }); f.viewed.mockResolvedValue([]);
  state().clearContactStore(); host = document.createElement("div"); document.body.append(host);
  root = createRoot(host); disposed = false;
});
afterEach(async () => { if (!disposed) await act(async () => root.unmount()); host.remove(); });

describe("blacklist actual component states and captured row actions", () => {
  it("preserves normal nonempty rendering", async () => {
    await loaded([one, two]); await render(); expect(text()).toContain(one.nickname); expect(text()).toContain(two.nickname);
  });
  it("dispatches exactly one real initial getter while idle, not on every render", async () => {
    const slow = deferred(); f.read.mockReturnValueOnce(slow.promise);
    try { await render(); expect(f.read).toHaveBeenCalledOnce(); await render(); expect(f.read).toHaveBeenCalledOnce(); }
    finally { await act(async () => { slow.resolve({ data: [] }); }); }
  });
  it("shows pending progress without Empty until the actual read completes", async () => {
    const slow = deferred(); f.read.mockReturnValueOnce(slow.promise); const pending = state().getBlackListByReq();
    try { await render(); expect(f.read).toHaveBeenCalledOnce(); expect(empty()).toBeNull();
      expect(host.querySelector('[role="status"][aria-busy="true"]')).not.toBeNull(); }
    finally { await act(async () => { slow.resolve({ data: [] }); await pending; }); }
    expect(empty()).not.toBeNull();
  });
  it("shows Empty for a genuinely confirmed empty read", async () => {
    await loaded([]); await render(); expect(empty()).not.toBeNull(); expect(host.querySelector('[role="alert"]')).toBeNull();
  });
  it("shows explicit failure and retry instead of pretending the read was empty", async () => {
    f.read.mockRejectedValueOnce(new Error("initial failure")); await state().getBlackListByReq(); await render();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("toast.getBlackListFailed");
    expect(empty()).toBeNull(); expect(callback("retry")).toBeTypeOf("function");
  });
  it("retains the same owner's rows with an explicit refresh error", async () => {
    await loaded(); f.read.mockRejectedValueOnce(new Error("refresh")); await state().getBlackListByReq(); await render();
    expect(text()).toContain(one.nickname); expect(host.querySelector('[role="alert"]')).not.toBeNull(); expect(empty()).toBeNull();
  });
  it("never exposes A rows when B's actual read starts or rejects", async () => {
    await loaded(); await render(); expect(text()).toContain(one.nickname);
    f.owner = { ...f.owner, issuer: "issuer-B", tenant: { serverID: "tenant-B" } }; f.generation++;
    const slow = deferred(); f.read.mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    await act(async () => { pending = state().getBlackListByReq(); });
    try { await render(); expect(f.read).toHaveBeenCalledTimes(2); expect(text()).not.toContain(one.nickname); expect(empty()).toBeNull(); }
    finally { await act(async () => { slow.reject(new Error("B read")); await pending; }); }
    expect(text()).not.toContain(one.nickname); expect(host.querySelector('[role="alert"]')).not.toBeNull(); expect(empty()).toBeNull();
  });
  it("retries only the read once for synchronous double activation", async () => {
    f.read.mockRejectedValueOnce(new Error("initial failure")); await state().getBlackListByReq(); await render();
    const retry = callback("retry"), slow = deferred(); f.read.mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    try { await act(async () => { pending = retry(); await retry(); }); expect(f.read).toHaveBeenCalledTimes(2);
      expect(f.remove).not.toHaveBeenCalled(); }
    finally { await act(async () => { slow.resolve({ data: [] }); await pending; }); }
    expect(empty()).not.toBeNull();
  });
  it("discards an old owner's captured retry after equal-generation owner replacement", async () => {
    f.read.mockRejectedValueOnce(new Error("failure")); await state().getBlackListByReq(); await render();
    const retry = callback("retry"); f.owner = { ...f.owner }; await retry(); expect(f.read).toHaveBeenCalledOnce();
  });
  it("discards a captured retry after the page unmounts", async () => {
    f.read.mockRejectedValueOnce(new Error("failure")); await state().getBlackListByReq(); await render();
    const retry = callback("retry"); await act(async () => root.unmount()); disposed = true;
    await retry(); expect(f.read).toHaveBeenCalledOnce();
  });
  it("keeps normal removal and the existing authoritative SDK event update", async () => {
    await loaded(); await render();
    f.remove.mockImplementationOnce(async (userID: string) => { state().updateBlack({ ...one, userID }, true); return { data: {} }; });
    await act(async () => { await callback("placeholder.remove")(); });
    expect(f.remove).toHaveBeenCalledExactlyOnceWith(one.userID); expect(state().blackList).toEqual([]); expect(empty()).not.toBeNull();
  });
  it("releases the busy row after an SDK exception and allows explicit retry", async () => {
    await loaded(); await render(); f.remove.mockRejectedValueOnce(new Error("synthetic remove"));
    await act(async () => { await callback("placeholder.remove")(); }); expect(f.feedback).toHaveBeenCalledOnce();
    await act(async () => { await callback("placeholder.remove")(); }); expect(f.remove).toHaveBeenCalledTimes(2);
  });
  it("reports a same-owner removal failure after an unrelated list rerender", async () => {
    await loaded(); await render(); const slow = deferred(); f.remove.mockReturnValueOnce(slow.promise);
    let pending!: Promise<void>; await act(async () => { pending = callback("placeholder.remove")(); });
    expect(f.remove).toHaveBeenCalledExactlyOnceWith(one.userID);
    await act(async () => { state().pushNewBlack(two); });
    expect(text()).toContain(two.nickname);
    await act(async () => { slow.reject(new Error("current same-owner failure")); await pending; });
    expect(f.feedback).toHaveBeenCalledOnce(); expect(state().blackList).toContain(one);
  });
  it("reads once again after an idle reset while the same-owner page remains mounted", async () => {
    await loaded(); await render(); expect(f.read).toHaveBeenCalledOnce();
    await act(async () => { state().clearContactStore(); });
    expect(f.read).toHaveBeenCalledTimes(2); expect(empty()).not.toBeNull();
  });
  it("does not let an invalidated pending read block the new idle read", async () => {
    const slow = deferred(); f.read.mockReturnValueOnce(slow.promise);
    try {
      await render(); expect(f.read).toHaveBeenCalledOnce();
      f.read.mockResolvedValueOnce({ data: [two] });
      await act(async () => { state().clearContactStore(); });
      expect(f.read).toHaveBeenCalledTimes(2); expect(text()).toContain(two.nickname);
    } finally { await act(async () => { slow.resolve({ data: [one] }); }); }
    expect(text()).toContain(two.nickname); expect(text()).not.toContain(one.nickname);
  });
  it("does not dispatch a second native removal on a synchronous double tap", async () => {
    await loaded(); await render(); const remove = callback("placeholder.remove"), slow = deferred();
    f.remove.mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    try { await act(async () => { pending = remove(); await remove(); }); expect(f.remove).toHaveBeenCalledOnce(); }
    finally { await act(async () => { slow.resolve({ data: {} }); await pending; }); }
  });
  it("never dispatches an old row callback under a replacement owner", async () => {
    await loaded(); await render(); const remove = callback("placeholder.remove");
    f.owner = { ...f.owner }; await act(async () => { await remove(); }); expect(f.remove).not.toHaveBeenCalled();
  });
  it("never dispatches a captured row callback after unmount", async () => {
    await loaded(); await render(); const remove = callback("placeholder.remove");
    await act(async () => root.unmount()); disposed = true; await remove(); expect(f.remove).not.toHaveBeenCalled();
  });
  it("rejects a stale row when the actual store has replaced that identity", async () => {
    await loaded(); await render(); const remove = callback("placeholder.remove");
    await act(async () => { state().updateBlack({ ...one, nickname: "fresh row" }); });
    await act(async () => { await remove(); }); expect(f.remove).not.toHaveBeenCalled();
  });
  it("does not toast a rejected late removal into a new session", async () => {
    await loaded(); await render(); const slow = deferred(); f.remove.mockReturnValueOnce(slow.promise);
    let pending!: Promise<void>; await act(async () => { pending = callback("placeholder.remove")(); });
    expect(f.remove).toHaveBeenCalledOnce(); f.owner = { ...f.owner }; f.generation++;
    await act(async () => { slow.reject(new Error("late old-session rejection")); await pending; });
    expect(f.feedback).not.toHaveBeenCalled();
  });
});
