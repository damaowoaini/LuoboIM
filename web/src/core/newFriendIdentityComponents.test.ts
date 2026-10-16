// @vitest-environment jsdom
// Actual ApplicationItem and NewFriends itemContent/callbacks. Only SDK/store,
// i18n, OS/storage and presentation primitives are adapted; no business network.
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const f = vi.hoisted(() => ({
  accept: vi.fn(), refuse: vi.fn(), feedback: vi.fn(), accessed: vi.fn(), badge: vi.fn(),
  rows: [] as any[], user: { selfInfo: { userID: "actor" } },
  contacts: { recvFriendApplicationList: [] as any[], sendFriendApplicationList: [] as any[],
    getFriendApplicationReadPhase: (_direction: unknown, _expected: unknown) => "ready" },
  session: { current: { userID: "actor", issuer: "A", tenant: { serverID: "A" } } as any, generation: 1 },
}));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: {
  get current() { return f.session.current; }, get generation() { return f.session.generation; },
} }));
vi.mock("@/layout/MainContentWrap", () => ({ IMSDK: {
  acceptFriendApplication: f.accept, refuseFriendApplication: f.refuse,
} }));
vi.mock("@/store", () => ({ useUserStore: Object.assign((select: any) => select(f.user), { getState: () => f.user }) }));
vi.mock("@/store/contact", () => ({ useContactStore: Object.assign((select: any) => select(f.contacts), { getState: () => f.contacts }) }));
vi.mock("@/utils/common", () => ({ feedbackToast: f.feedback }));
vi.mock("@/utils/imCommon", () => ({ calcApplicationBadge: f.badge }));
vi.mock("@/utils/storage", () => ({ setAccessedFriendApplication: f.accessed }));
vi.mock("@/components/OIMAvatar", () => ({ default: () => null }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("open-im-sdk-wasm", () => ({ ApplicationHandleResult: { Unprocessed: 0, Agree: 1, Reject: -1 } }));
vi.mock("ahooks", () => ({ useDeepCompareEffect: React.useEffect }));
vi.mock("antd", () => ({
  Button: ({ children, disabled, onClick }: any) => React.createElement("button", { disabled, onClick }, children),
  Spin: ({ children, spinning }: any) => React.createElement("section", { "data-busy": String(spinning) }, children),
}));
vi.mock("react-virtuoso", () => ({ Virtuoso: ({ data, itemContent }: any) => {
  const elements = data.map((item: any, index: number) => itemContent(index, item));
  // Keep the genuine element's callbacks; do not replace ApplicationItem or
  // fabricate a component-level API. This also permits an actual old closure
  // to be invoked after its portal/page has unmounted.
  f.rows = elements.map((element: React.ReactElement<any>) => element.props);
  return React.createElement("div", null, ...elements);
} }));
import ApplicationItem from "@/components/ApplicationItem";
import { NewFriends } from "@/pages/contact/newFriends";

let root: Root, host: HTMLDivElement, disposed: boolean;
const request = (patch: any = {}) => ({ fromUserID: "peer-one", toUserID: "actor", fromNickname: "同名",
  toNickname: "本人", reqMsg: "original verification", createTime: 1700000000, handleResult: 0, ...patch });
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  vi.clearAllMocks(); f.rows = []; f.user.selfInfo.userID = "actor";
  f.session.current = { userID: "actor", issuer: "A", tenant: { serverID: "A" } }; f.session.generation = 1;
  f.contacts.recvFriendApplicationList = [request()]; f.contacts.sendFriendApplicationList = [];
  f.accept.mockResolvedValue({ data: {} }); f.refuse.mockResolvedValue({ data: {} });
  f.accessed.mockResolvedValue(undefined);
  host = document.createElement("div"); document.body.append(host); root = createRoot(host); disposed = false;
});
afterEach(async () => { if (!disposed) await act(async () => root.unmount()); host.remove(); });
async function render(node: React.ReactNode = React.createElement(NewFriends)) { await act(async () => root.render(node)); }
async function unmount() { await act(async () => root.unmount()); disposed = true; }
function button(key: string) { return [...host.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent === key); }
async function click(key: string) { const item = button(key); expect(item).toBeDefined(); await act(async () => item!.click()); }
function deferred() { let resolve!: (v?: any) => void, reject!: (v: any) => void;
  const promise = new Promise<any>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
function savedRow() { expect(f.rows).toHaveLength(1); return f.rows[0]; }
const item = (source: any, accept = vi.fn().mockResolvedValue(undefined), reject = vi.fn().mockResolvedValue(undefined)) =>
  React.createElement(ApplicationItem, { source, currentUserID: "actor", onAccept: accept, onReject: reject });

describe("actual friend request identity and page mutation boundary", () => {
  it("shows distinct complete peer IDs for incoming rows with the same nickname", async () => {
    f.contacts.recvFriendApplicationList = [request(), request({ fromUserID: "peer-two" })];
    await render(); expect(host.textContent).toContain("peer-one"); expect(host.textContent).toContain("peer-two");
    expect(host.textContent?.match(/同名/g)).toHaveLength(2);
  });
  it("shows the outgoing target UID and full long UID instead of the actor or nickname", async () => {
    const target = "target-" + "1234567890".repeat(12);
    await render(item(request({ fromUserID: "actor", toUserID: target })));
    expect(host.textContent).toContain(target); expect(host.textContent).toContain("placeholder.recipientAccountID");
    expect(button("application.agree")).toBeUndefined(); expect(button("application.refuse")).toBeUndefined();
  });
  it("offers no friend accept/refuse for incomplete, self or nonparticipant requests", async () => {
    for (const source of [request({ fromUserID: undefined }), request({ toUserID: "elsewhere" }),
      request({ fromUserID: "actor", toUserID: "actor" }), request({ createTime: 0 }),
      request({ reqMsg: undefined }), request({ createTime: 1.5 })]) {
      await render(item(source)); expect(button("application.agree")).toBeUndefined();
      expect(button("application.refuse")).toBeUndefined();
    }
  });
  it("preserves normal incoming group application actions", async () => {
    const accept = vi.fn().mockResolvedValue(undefined);
    const group = { groupID: "group-id", groupName: "群", userID: "applicant", nickname: "申请人", handleResult: 0, reqMsg: "join" };
    await render(item(group, accept)); await click("application.agree");
    expect(accept).toHaveBeenCalledExactlyOnceWith(group); expect(host.textContent).toContain("申请人");
  });
  it("synchronously blocks double tap and clears busy after an actual awaited result", async () => {
    const slow = deferred(), accept = vi.fn().mockReturnValue(slow.promise);
    await render(item(request(), accept)); const acceptButton = button("application.agree")!;
    await act(async () => { acceptButton.click(); acceptButton.click(); });
    expect(accept).toHaveBeenCalledOnce(); expect(acceptButton.disabled).toBe(true);
    await act(async () => slow.resolve()); expect(acceptButton.disabled).toBe(false);
  });
  it("releases component busy on callback rejection and permits a later explicit attempt", async () => {
    const accept = vi.fn().mockRejectedValueOnce(new Error("synthetic rejection")).mockResolvedValue(undefined);
    await render(item(request(), accept)); await click("application.agree");
    expect(button("application.agree")!.disabled).toBe(false); expect(f.feedback).toHaveBeenCalledOnce();
    await click("application.agree"); expect(accept).toHaveBeenCalledTimes(2);
  });
  it("dispatches one valid exact incoming accept and preserves the normal refuse path", async () => {
    await render(); const props = savedRow(); await act(async () => props.onAccept(props.source));
    expect(f.accept).toHaveBeenCalledExactlyOnceWith({ toUserID: "peer-one", handleMsg: "" });
    f.contacts.recvFriendApplicationList = [request({ fromUserID: "peer-two", createTime: 1700000001 })];
    await render(); const next = savedRow(); await act(async () => next.onReject(next.source));
    expect(f.refuse).toHaveBeenCalledExactlyOnceWith({ toUserID: "peer-two", handleMsg: "" });
  });
  it("blocks a captured real row callback after owner replacement even at the same generation", async () => {
    await render(); const props = savedRow();
    f.session.current = { ...f.session.current };
    await act(async () => props.onAccept(props.source)); expect(f.accept).not.toHaveBeenCalled();
  });
  it("blocks captured refuse after generation or actor-store changes before any rerender", async () => {
    await render(); const props = savedRow(); f.session.generation++;
    await act(async () => props.onReject(props.source)); expect(f.refuse).not.toHaveBeenCalled();
    f.session.generation--; f.user.selfInfo.userID = "new-actor";
    await act(async () => props.onReject(props.source)); expect(f.refuse).not.toHaveBeenCalled();
  });
  it("blocks a real page callback retained after unmount", async () => {
    await render(); const props = savedRow(); await unmount();
    await props.onAccept(props.source); expect(f.accept).not.toHaveBeenCalled();
  });
  it("rechecks current row time, original verification and unprocessed state before dispatch", async () => {
    await render(); const props = savedRow();
    for (const patch of [{ createTime: 1700000001 }, { reqMsg: "changed verification" }, { handleResult: 1 }]) {
      f.contacts.recvFriendApplicationList = [request(patch)];
      await act(async () => props.onAccept(props.source));
      expect(f.accept).not.toHaveBeenCalled();
    }
  });
  it("rejects duplicate pair rows because the SDK response has no request-time selector", async () => {
    f.contacts.recvFriendApplicationList = [request(), request({ createTime: 1700000001 })];
    await render(); expect(button("application.agree")).toBeUndefined();
    const props = f.rows[0]; await act(async () => props.onAccept(props.source));
    expect(f.accept).not.toHaveBeenCalled();
  });
  it("rejects the same incoming pair copied into the sent list", async () => {
    f.contacts.sendFriendApplicationList = [request()]; await render();
    expect(button("application.agree")).toBeUndefined();
    await act(async () => f.rows[0].onAccept(f.rows[0].source)); expect(f.accept).not.toHaveBeenCalled();
  });
  it("snapshots the rendered original row instead of dispatching a later mutated source object", async () => {
    const original = f.contacts.recvFriendApplicationList[0]; await render(); const props = savedRow();
    original.reqMsg = "mutated after render";
    await act(async () => props.onAccept(props.source)); expect(f.accept).not.toHaveBeenCalled();
  });
  it("shares an in-flight barrier across accept/refuse callbacks for the same row", async () => {
    const slow = deferred(); f.accept.mockReturnValue(slow.promise); await render(); const props = savedRow();
    let first!: Promise<void>;
    await act(async () => { first = props.onAccept(props.source); await props.onReject(props.source); });
    expect(f.accept).toHaveBeenCalledOnce(); expect(f.refuse).not.toHaveBeenCalled();
    await act(async () => { slow.resolve(); await first; });
  });
  it("keeps the same pair locked across a replaced store row and rerender, then permits the new row after settle", async () => {
    const slow = deferred(); f.accept.mockReturnValueOnce(slow.promise);
    await render(); const original = savedRow(); let first!: Promise<void>;
    await act(async () => { first = original.onAccept(original.source); });
    expect(f.accept).toHaveBeenCalledExactlyOnceWith({ toUserID: "peer-one", handleMsg: "" });
    f.contacts.recvFriendApplicationList = [request({ createTime: 1700000010, reqMsg: "new verification" })];
    await render(); const next = savedRow();
    expect(next.source.createTime).toBe(1700000010); expect(next.source.reqMsg).toBe("new verification");
    await act(async () => { await next.onAccept(next.source); await next.onReject(next.source); });
    expect(f.accept).toHaveBeenCalledOnce(); expect(f.refuse).not.toHaveBeenCalled();
    await act(async () => { slow.resolve(); await first; });
    await act(async () => next.onReject(next.source));
    expect(f.refuse).toHaveBeenCalledExactlyOnceWith({ toUserID: "peer-one", handleMsg: "" });
    expect(f.accept).toHaveBeenCalledOnce();
  });
  it("proves request dispatch then drops a late SDK rejection after owner replacement", async () => {
    const slow = deferred(); f.accept.mockReturnValue(slow.promise); await render(); const props = savedRow();
    let pending!: Promise<void>; await act(async () => { pending = props.onAccept(props.source); });
    expect(f.accept).toHaveBeenCalledExactlyOnceWith({ toUserID: "peer-one", handleMsg: "" });
    f.session.current = { ...f.session.current };
    await act(async () => { slow.reject(new Error("late old failure")); await pending; });
    expect(f.feedback).not.toHaveBeenCalled(); expect(f.accept).toHaveBeenCalledOnce();
  });
});
