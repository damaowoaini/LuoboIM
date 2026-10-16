// @vitest-environment jsdom
// Actual NewFriends + real Zustand getter state + ApplicationItem. SDK/OS and
// AntD/Virtuoso presentation primitives adapt, not the page or business store.
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const f = vi.hoisted(() => ({
  owner: {} as any, generation: 1, user: { selfInfo: { userID: "actor" as string | undefined } },
  recv: vi.fn(), send: vi.fn(), accept: vi.fn(), refuse: vi.fn(), viewed: vi.fn(), accessed: vi.fn(), badge: vi.fn(),
  feedback: vi.fn(), buttons: new Map<string, () => Promise<void>>(),
}));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: {
  get current() { return f.owner; }, get generation() { return f.generation; },
} }));
vi.mock("@/store", () => ({ useUserStore: Object.assign((select: any) => select(f.user), { getState: () => f.user }) }));
vi.mock("@/store/user", () => ({ useUserStore: Object.assign((select: any) => select(f.user), { getState: () => f.user }) }));
vi.mock("@/layout/MainContentWrap", () => ({ IMSDK: {
  getFriendApplicationListAsRecipient: f.recv, getFriendApplicationListAsApplicant: f.send,
  acceptFriendApplication: f.accept, refuseFriendApplication: f.refuse,
} }));
vi.mock("@/utils/storage", () => ({ getAccessedFriendApplication: f.viewed,
  getAccessedGroupApplication: f.viewed, setAccessedFriendApplication: f.accessed }));
vi.mock("@/utils/imCommon", () => ({ calcApplicationBadge: f.badge }));
vi.mock("@/utils/common", () => ({ feedbackToast: f.feedback }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("open-im-sdk-wasm", () => ({ ApplicationHandleResult: { Unprocessed: 0, Agree: 1, Reject: -1 } }));
vi.mock("@/components/OIMAvatar", () => ({ default: () => null }));
vi.mock("antd", () => ({
  Button: ({ children, disabled, onClick }: any) => {
    if (typeof children === "string") f.buttons.set(children, onClick);
    return React.createElement("button", { disabled, onClick }, children);
  },
  Spin: ({ children }: any) => React.createElement("div", null, children),
}));
vi.mock("react-virtuoso", () => ({ Virtuoso: ({ data, itemContent }: any) =>
  React.createElement("div", null, ...data.map((item: any, index: number) => itemContent(index, item))) }));
import { useContactStore } from "@/store/contact";
import { NewFriends } from "@/pages/contact/newFriends";

let root: Root, host: HTMLDivElement, disposed: boolean;
let consoleError: ReturnType<typeof vi.spyOn>;
const received = { fromUserID: "peer-in", toUserID: "actor", fromNickname: "同名", reqMsg: "hi", createTime: 1700000000, handleResult: 0 } as any;
const sent = { ...received, fromUserID: "actor", toUserID: "peer-out", toNickname: "同名" } as any;
const state = () => useContactStore.getState();
function deferred() { let resolve!: (v: any) => void, reject!: (v: any) => void;
  const promise = new Promise<any>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); vi.resetAllMocks(); f.buttons.clear();
  f.owner = { userID: "actor", issuer: "A", tenant: { serverID: "A" } }; f.generation = 1; f.user.selfInfo.userID = "actor";
  f.recv.mockResolvedValue({ data: [] }); f.send.mockResolvedValue({ data: [] });
  f.accept.mockResolvedValue({ data: {} }); f.refuse.mockResolvedValue({ data: {} });
  f.viewed.mockResolvedValue([]); f.accessed.mockResolvedValue(undefined); f.badge.mockResolvedValue(undefined);
  state().clearContactStore(); host = document.createElement("div"); document.body.append(host); root = createRoot(host); disposed = false;
  consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
});
afterEach(async () => { if (!disposed) await act(async () => root.unmount()); host.remove(); consoleError.mockRestore(); });
async function render() { await act(async () => root.render(React.createElement(NewFriends))); }
async function read() { await Promise.all([state().getRecvFriendApplicationListByReq(), state().getSendFriendApplicationListByReq()]); }
function text() { return host.textContent ?? ""; }
function retry() { const handler = f.buttons.get("application.retryFriendRequests"); expect(handler).toBeDefined(); return handler!; }
function empty() { expect(text()).toContain("application.noFriendRequests"); }

describe("NewFriends visible read states", () => {
  it("shows a confirmed empty state after both genuine SDK reads succeed", async () => {
    await read(); await render(); empty(); expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(f.recv).toHaveBeenCalledOnce(); expect(f.send).toHaveBeenCalledOnce();
  });
  it("shows loading while one actual direction is still pending, never empty", async () => {
    const slow = deferred(); f.recv.mockReturnValue(slow.promise); const pending = state().getRecvFriendApplicationListByReq();
    await state().getSendFriendApplicationListByReq();
    try { await render(); expect(f.recv).toHaveBeenCalledOnce(); expect(text()).toContain("application.friendRequestsLoading");
      expect(text()).not.toContain("application.noFriendRequests"); }
    finally { await act(async () => { slow.resolve({ data: [] }); await pending; }); }
    empty();
  });
  it("shows direction-specific failure without mislabeling a failed empty read as no applications", async () => {
    f.recv.mockRejectedValueOnce(new Error("synthetic read failure")); await read(); await render();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("application.receivedFriendRequestsFailed");
    expect(text()).not.toContain("application.noFriendRequests"); expect(retry()).toBeTypeOf("function");
  });
  it("keeps received and sent IDs visible when a later one-direction refresh fails", async () => {
    f.recv.mockResolvedValueOnce({ data: [received] }); f.send.mockResolvedValueOnce({ data: [sent] }); await read();
    f.send.mockRejectedValueOnce(new Error("refresh")); await state().getSendFriendApplicationListByReq(); await render();
    expect(text()).toContain("peer-in"); expect(text()).toContain("peer-out");
    expect(text()).toContain("application.sentFriendRequestsFailed"); expect(text()).not.toContain("application.noFriendRequests");
  });
  it("does not show A rows while B's actual reads are loading or fail", async () => {
    f.recv.mockResolvedValueOnce({ data: [received] }); f.send.mockResolvedValueOnce({ data: [sent] });
    await read(); await render(); expect(text()).toContain("peer-in"); expect(text()).toContain("peer-out");
    f.owner = { ...f.owner, issuer: "B", tenant: { serverID: "B" } }; f.generation++;
    const recv = deferred(), send = deferred(); f.recv.mockReturnValueOnce(recv.promise); f.send.mockReturnValueOnce(send.promise);
    let pending!: Promise<void>; await act(async () => { pending = read(); });
    try {
      await render(); expect(f.recv).toHaveBeenCalledTimes(2); expect(f.send).toHaveBeenCalledTimes(2);
      expect(text()).not.toContain("peer-in"); expect(text()).not.toContain("peer-out");
      expect(text()).toContain("application.friendRequestsLoading");
    } finally { await act(async () => {
      recv.reject(new Error("B received read")); send.reject(new Error("B sent read")); await pending;
    }); }
    expect(text()).not.toContain("peer-in"); expect(text()).not.toContain("peer-out");
    expect(text()).toContain("application.receivedFriendRequestsFailed");
    expect(text()).toContain("application.sentFriendRequestsFailed"); expect(text()).not.toContain("application.noFriendRequests");
  });
  it("explicit retry reads only the failed direction once and cannot accept/refuse or send a request", async () => {
    f.recv.mockRejectedValueOnce(new Error("first read")); await read(); await render();
    const slow = deferred(); f.recv.mockReturnValueOnce(slow.promise); const callback = retry(); let one!: Promise<void>;
    try { await act(async () => { one = callback(); await callback(); });
      expect(f.recv).toHaveBeenCalledTimes(2); expect(f.send).toHaveBeenCalledOnce();
      expect(f.accept).not.toHaveBeenCalled(); expect(f.refuse).not.toHaveBeenCalled(); }
    finally { await act(async () => { slow.resolve({ data: [] }); await one; }); }
    empty();
  });
  it("does not replay a captured retry after equal-generation owner replacement", async () => {
    f.recv.mockRejectedValueOnce(new Error("read")); await read(); await render(); const callback = retry();
    f.owner = { ...f.owner }; await callback(); expect(f.recv).toHaveBeenCalledOnce(); expect(f.send).toHaveBeenCalledOnce();
  });
  it("does not replay a captured retry after unmount", async () => {
    f.recv.mockRejectedValueOnce(new Error("read")); await read(); await render(); const callback = retry();
    await act(async () => root.unmount()); disposed = true; await callback(); expect(f.recv).toHaveBeenCalledOnce();
  });
  it("hides another owner's old ready/empty state and never shows that owner as current empty", async () => {
    await read(); f.owner = { ...f.owner }; f.user.selfInfo.userID = "foreign-actor";
    await render(); expect(text()).not.toContain("application.noFriendRequests");
    expect(text()).toContain("application.friendRequestsNotLoaded");
    expect(f.recv).toHaveBeenCalledOnce(); expect(f.send).toHaveBeenCalledOnce();
  });
  it("the initial idle page dispatches both reads through the real getters without retrying an existing loading read", async () => {
    const recv = deferred(), send = deferred(); f.recv.mockReturnValue(recv.promise); f.send.mockReturnValue(send.promise);
    try { await render(); expect(f.recv).toHaveBeenCalledOnce(); expect(f.send).toHaveBeenCalledOnce();
      await render(); expect(f.recv).toHaveBeenCalledOnce(); expect(f.send).toHaveBeenCalledOnce(); }
    finally { await act(async () => { recv.resolve({ data: [] }); send.resolve({ data: [] }); }); }
    empty();
  });
});
