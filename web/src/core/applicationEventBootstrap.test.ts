// @vitest-environment jsdom
// Render the real useGlobalEvent hook. Invoke its actually registered SDK
// callbacks and use the real Zustand contact store. Transport, other stores,
// session getters, OS notifications/audio and unrelated helpers are adapters.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
const f = vi.hoisted(() => ({
  owner: { userID: "actor-A", issuer: "issuer-A", tenant: { serverID: "tenant-A" } } as any,
  generation: 1, actor: undefined as string | undefined,
  navigate: vi.fn(), ipc: vi.fn(), feedback: vi.fn(), viewedFriend: vi.fn(), viewedGroup: vi.fn(),
  notices: [] as any[], play: vi.fn(),
  sdk: { on: vi.fn(), off: vi.fn(), login: vi.fn(), logout: vi.fn() },
  user: { updateSelfInfo: vi.fn(), getWorkMomentsUnreadCount: vi.fn(), userLogout: vi.fn(), addImageCache: vi.fn(), getAppConfigByReq: vi.fn() } as any,
}));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: {
  get current() { return f.owner; }, get generation() { return f.generation; },
  // The SDK callback adapter deliberately invokes captured listeners even after
  // epoch changes, so the candidate's own inner fence must provide the defense.
  guard: (callback: any) => callback, onChange: () => () => undefined,
} }));
vi.mock("@/core/session/desktop", () => ({ subscribeSessionEvent: () => () => undefined }));
vi.mock("@/layout/MainContentWrap", () => ({ IMSDK: f.sdk }));
vi.mock("@/store/user", () => {
  const getState = () => ({ ...f.user, selfInfo: { userID: f.actor } });
  const hook = Object.assign((selector: any) => selector(getState()), { getState });
  return { useUserStore: hook, isExplicitLogoutPending: () => false };
});
vi.mock("@/store", async () => {
  const { useUserStore } = await import("@/store/user");
  const state = new Proxy({} as any, { get: () => () => undefined });
  const stub = Object.assign((selector: any) => selector(state), { getState: () => state });
  return { useUserStore, useConversationStore: stub, useMessageStore: stub };
});
vi.mock("@/utils/storage", () => ({
  getIMToken: async () => "synthetic-test-token", getIMUserID: async () => "actor-A",
  clearIMProfile: vi.fn(), getAccessedFriendApplication: f.viewedFriend, getAccessedGroupApplication: f.viewedGroup,
}));
vi.mock("@/utils/common", () => ({ generateAvatar: () => "synthetic-avatar", feedbackToast: f.feedback }));
vi.mock("@/utils/imCommon", () => ({ initStore: vi.fn(), createNotification: vi.fn(), isGroupSession: () => false }));
vi.mock("@/utils/events", () => ({ default: { emit: vi.fn(), on: vi.fn(), off: vi.fn() } }));
vi.mock("@/AntdGlobalComp", () => ({ message: { success: vi.fn(), error: vi.fn() } }));
vi.mock("@/api/login", () => ({ BusinessAllowType: { Allow: 1, NotAllow: 2 } }));
vi.mock("@/config", () => ({ getApiUrl: () => "", getWsUrl: () => "" }));
vi.mock("@/constants", () => ({ SystemMessageTypes: [] }));
vi.mock("react-router-dom", () => ({ useNavigate: () => f.navigate }));
vi.mock("ahooks", () => ({ useLatest: (value: any) => ({ current: value }), useThrottleFn: (run: any) => ({ run }) }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("open-im-sdk-wasm", () => ({
  CbEvents: new Proxy({}, { get: (_target, key) => String(key) }),
  ApplicationHandleResult: { Unprocessed: 0, Agree: 1, Reject: -1 },
  LogLevel: { Error: 3 }, MessageReceiveOptType: {}, MessageType: {}, SessionType: {},
}));
import { useGlobalEvent } from "@/layout/useGlobalEvents";
import { useContactStore } from "@/store/contact";
const contact = () => useContactStore.getState();
const friend = (patch: any = {}) => ({ fromUserID: "peer-B", toUserID: "actor-A", createTime: 1700000000, reqMsg: "synthetic request", handleResult: 0, fromNickname: "Same name", ...patch });
const group = (patch: any = {}) => ({ userID: "peer-B", groupID: "group-C", reqTime: 1700000000, reqMsg: "synthetic group request", handleResult: 0, nickname: "Same name", groupName: "Synthetic group", ...patch });
const eventName = (family: "friend" | "group") => family === "friend" ? "OnFriendApplicationAdded" : "OnGroupApplicationAdded";
const row = (family: "friend" | "group") => family === "friend" ? friend() : group();
const rows = (family: "friend" | "group") => family === "friend" ? contact().recvFriendApplicationList : contact().recvGroupApplicationList;
function callback(family: "friend" | "group") {
  const call = f.sdk.on.mock.calls.find(([event]) => event === eventName(family));
  expect(call, "actual hook must have registered this callback").toBeDefined();
  return call![1] as (event: any) => void;
}
let root: Root | undefined;
let container: HTMLDivElement;
async function flush() { await act(async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); }); }
async function mount(strict = false) {
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  function Probe() { useGlobalEvent(); return null; }
  await act(async () => {
    const probe = React.createElement(Probe);
    root!.render(strict ? React.createElement(React.StrictMode, null, probe) : probe);
  }); await flush();
  expect(f.sdk.login).toHaveBeenCalledTimes(strict ? 2 : 1); // Real hook dispatch, mock pending transport.
}
async function unmount() { if (root) { await act(async () => root!.unmount()); root = undefined; } container?.remove(); }
async function emit(family: "friend" | "group", data = row(family)) {
  await act(async () => { callback(family)({ data }); await Promise.resolve(); }); await flush();
}
function deferred() { let resolve!: (value: string[]) => void; const promise = new Promise<string[]>((yes) => { resolve = yes; }); return { resolve, promise }; }
beforeEach(() => {
  vi.clearAllMocks(); f.owner = { userID: "actor-A", issuer: "issuer-A", tenant: { serverID: "tenant-A" } }; f.generation = 1; f.actor = undefined; f.notices = [];
  f.sdk.login.mockImplementation(() => new Promise(() => undefined));
  f.viewedFriend.mockResolvedValue([]); f.viewedGroup.mockResolvedValue([]);
  f.play.mockResolvedValue(undefined); vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(f.play);
  Object.defineProperty(document, "hidden", { configurable: true, value: true });
  class Notice { static permission = "granted"; onclick?: () => void; close = vi.fn(); constructor(public title: string, public options: any) { f.notices.push(this); } }
  vi.stubGlobal("Notification", Notice); Object.defineProperty(window, "electronAPI", { configurable: true, value: { ipcInvoke: f.ipc, getPlatform: () => 5 } });
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; contact().clearContactStore();
});
afterEach(async () => { await unmount(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("actual application hook uses the trusted registration actor during bootstrap", () => {
  it("StrictMode cleanup and setup reactivates the same valid registration normally", async () => {
    f.actor = "actor-A"; await mount(true);
    expect(f.sdk.on.mock.calls.filter(([event]) => event === eventName("friend"))).toHaveLength(2);
    expect(f.sdk.off.mock.calls.some(([event]) => event === eventName("friend"))).toBe(true);
    await emit("friend"); expect(contact().recvFriendApplicationList).toEqual([friend()]); expect(f.viewedFriend).toHaveBeenCalledOnce();
  });
  it("puts a received friend request in recv, not send, before self-info arrives", async () => {
    await mount(); await emit("friend"); expect(contact().recvFriendApplicationList).toEqual([friend()]); expect(contact().sendFriendApplicationList).toEqual([]); expect(f.viewedFriend).toHaveBeenCalledOnce();
  });
  it("keeps a self-authored friend request outgoing while the UI actor is empty", async () => {
    f.actor = ""; await mount(); const sent = friend({ fromUserID: "actor-A", toUserID: "peer-B" }); await emit("friend", sent);
    expect(contact().sendFriendApplicationList).toEqual([sent]); expect(contact().recvFriendApplicationList).toEqual([]); expect(f.viewedFriend).not.toHaveBeenCalled(); expect(f.notices).toEqual([]);
  });
  it("preserves the authenticated group recipient event while self-info is empty", async () => {
    await mount(); await emit("group"); expect(contact().recvGroupApplicationList).toEqual([group()]); expect(contact().sendGroupApplicationList).toEqual([]);
  });
  it("keeps the self applicant group event outgoing", async () => {
    await mount(); const sent = group({ userID: "actor-A" }); await emit("group", sent); expect(contact().sendGroupApplicationList).toEqual([sent]); expect(contact().recvGroupApplicationList).toEqual([]); expect(f.notices).toEqual([]);
  });
  for (const family of ["friend", "group"] as const) {
    it(`${family}: preserves normal initialized-actor receiving`, async () => {
      f.actor = "actor-A"; await mount(); await emit(family); expect(rows(family)).toEqual([row(family)]);
    });
    it(`${family}: a real admitted marker await permits hydration only to the same actor`, async () => {
      const slow = deferred(); (family === "friend" ? f.viewedFriend : f.viewedGroup).mockReturnValue(slow.promise);
      await mount(); callback(family)({ data: row(family) }); expect(family === "friend" ? f.viewedFriend : f.viewedGroup).toHaveBeenCalledOnce();
      f.actor = "actor-A"; slow.resolve([]); await flush(); expect(rows(family)).toEqual([row(family)]);
    });
    it(`${family}: the admitted bootstrap marker await cannot commit after owner replacement`, async () => {
      const slow = deferred(); (family === "friend" ? f.viewedFriend : f.viewedGroup).mockReturnValue(slow.promise);
      await mount(); callback(family)({ data: row(family) }); expect(family === "friend" ? f.viewedFriend : f.viewedGroup).toHaveBeenCalledOnce();
      f.owner = { ...f.owner }; slow.resolve([]); await flush(); expect(rows(family)).toEqual([]);
    });
    it(`${family}: a foreign actor during the real marker await cannot commit`, async () => {
      const slow = deferred(); (family === "friend" ? f.viewedFriend : f.viewedGroup).mockReturnValue(slow.promise);
      await mount(); callback(family)({ data: row(family) }); expect(family === "friend" ? f.viewedFriend : f.viewedGroup).toHaveBeenCalledOnce();
      f.actor = "foreign"; slow.resolve([]); await flush(); expect(rows(family)).toEqual([]);
    });
  }
});
describe("actual registered callbacks and notifications never adopt another owner", () => {
  for (const change of ["owner", "generation", "foreign-actor"] as const) {
    it(`${change}: old registered callbacks dispatch no application or notification`, async () => {
      f.actor = "actor-A"; await mount(); const oldFriend = callback("friend"), oldGroup = callback("group");
      if (change === "owner") f.owner = { ...f.owner }; if (change === "generation") f.generation++; if (change === "foreign-actor") f.actor = "foreign";
      oldFriend({ data: friend() }); oldGroup({ data: group() }); await flush();
      expect(contact().recvFriendApplicationList).toEqual([]); expect(contact().recvGroupApplicationList).toEqual([]); expect(contact().sendFriendApplicationList).toEqual([]); expect(contact().sendGroupApplicationList).toEqual([]); expect(f.play).not.toHaveBeenCalled(); expect(f.notices).toEqual([]);
    });
  }
  it("an unmounted actual hook cannot dispatch through a saved listener", async () => {
    f.actor = "actor-A"; await mount(); const old = callback("friend"); await unmount(); old({ data: friend() }); await flush(); expect(contact().recvFriendApplicationList).toEqual([]); expect(f.notices).toEqual([]);
  });
  it("no authenticated owner never admits a callback", async () => {
    f.owner = undefined; await mount(); await emit("friend"); await emit("group"); expect(contact().sendFriendApplicationList).toEqual([]); expect(contact().recvGroupApplicationList).toEqual([]); expect(f.notices).toEqual([]);
  });
  it("nonparticipant and malformed friend/group identities have no side effects", async () => {
    f.actor = "actor-A"; await mount();
    for (const data of [friend({ fromUserID: "third", toUserID: "fourth" }), friend({ fromUserID: "actor-A", toUserID: "actor-A" }), friend({ fromUserID: "" })]) await emit("friend", data);
    await emit("group", group({ groupID: "" })); expect(contact().recvFriendApplicationList).toEqual([]); expect(contact().sendFriendApplicationList).toEqual([]); expect(contact().recvGroupApplicationList).toEqual([]); expect(f.notices).toEqual([]);
  });
  for (const family of ["friend", "group"] as const) {
    it(`${family}: the actual OS notification uses its registered contact page`, async () => {
      f.actor = "actor-A"; await mount(); await emit(family); expect(f.notices).toHaveLength(1); f.notices[0].onclick();
      expect(f.navigate).toHaveBeenCalledWith(family === "friend" ? "/contact/newFriends" : "/contact/groupNotifications"); expect(f.ipc).toHaveBeenCalledWith("showMainWindow");
    });
    it(`${family}: an old notification click after equal-field owner replacement never navigates`, async () => {
      f.actor = "actor-A"; await mount(); await emit(family); const notice = f.notices[0]; expect(notice).toBeDefined(); f.owner = { ...f.owner }; notice.onclick();
      expect(f.navigate).not.toHaveBeenCalled(); expect(f.ipc).not.toHaveBeenCalledWith("showMainWindow"); expect(notice.close).toHaveBeenCalledOnce();
    });
  }
  for (const field of ["issuer", "tenant"] as const) {
    it(`${field}: a notification closes without navigation after an in-place owner scope change`, async () => {
      f.actor = "actor-A"; await mount(); await emit("friend"); const notice = f.notices[0]; expect(notice).toBeDefined();
      if (field === "issuer") f.owner.issuer = "issuer-B"; else f.owner.tenant.serverID = "tenant-B";
      notice.onclick(); expect(f.navigate).not.toHaveBeenCalled(); expect(f.ipc).not.toHaveBeenCalledWith("showMainWindow"); expect(notice.close).toHaveBeenCalledOnce();
    });
  }
  for (const change of ["generation", "foreign-actor"] as const) {
    it(`${change}: a previously visible friend notification cannot navigate after losing its owner`, async () => {
      f.actor = "actor-A"; await mount(); await emit("friend"); const notice = f.notices[0]; expect(notice).toBeDefined();
      if (change === "generation") f.generation++; else f.actor = "foreign";
      notice.onclick(); expect(f.navigate).not.toHaveBeenCalled(); expect(f.ipc).not.toHaveBeenCalledWith("showMainWindow"); expect(notice.close).toHaveBeenCalledOnce();
    });
  }
  it("an old notification click after hook unmount only closes", async () => {
    f.actor = "actor-A"; await mount(); await emit("friend"); const notice = f.notices[0]; await unmount(); notice.onclick(); expect(f.navigate).not.toHaveBeenCalled(); expect(f.ipc).not.toHaveBeenCalledWith("showMainWindow");
  });
});
describe("bootstrap capability is narrow at the real store boundary", () => {
  it("a default direct updater still refuses an empty unproven UI actor", async () => {
    await contact().updateRecvFriendApplication(friend() as any); await contact().updateRecvGroupApplication(group() as any);
    expect(f.viewedFriend).not.toHaveBeenCalled(); expect(f.viewedGroup).not.toHaveBeenCalled(); expect(contact().recvFriendApplicationList).toEqual([]);
  });
  it("a foreign owner capture cannot read viewed markers or commit", async () => {
    f.actor = "actor-A";
    const receive = contact().updateRecvFriendApplication as (data: any, expected?: any) => Promise<void>;
    await receive(friend(), { owner: { ...f.owner }, generation: f.generation });
    expect(f.viewedFriend).not.toHaveBeenCalled(); expect(contact().recvFriendApplicationList).toEqual([]);
  });
});
