// @vitest-environment jsdom
// Real Zustand store + the existing bindSessionSdk wrapper, not a recreated
// reducer. Only transport, UI actor, viewed-marker I/O and notifications adapt.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const f = vi.hoisted(() => ({
  owner: { userID: "same-user", issuer: "issuer-A", tenant: { serverID: "tenant-A" } } as any,
  generation: 1,
  actor: "same-user" as string | undefined,
  viewedFriend: vi.fn(), viewedGroup: vi.fn(), feedback: vi.fn(),
  sdk: {
    getFriendList: vi.fn(), getBlackList: vi.fn(), getJoinedGroupList: vi.fn(),
    getFriendApplicationListAsRecipient: vi.fn(), getFriendApplicationListAsApplicant: vi.fn(),
    getGroupApplicationListAsRecipient: vi.fn(), getGroupApplicationListAsApplicant: vi.fn(),
    on: vi.fn(), off: vi.fn(), login: vi.fn(), logout: vi.fn(),
  },
}));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: {
  get current() { return f.owner; }, get generation() { return f.generation; },
  onChange: () => () => undefined,
} }));
vi.mock("@/store/user", () => ({ useUserStore: { getState: () => ({ selfInfo: { userID: f.actor } }) } }));
vi.mock("@/layout/MainContentWrap", async () => {
  const { bindSessionSdk } = await import("@/core/session/sdk");
  const { sessionCoordinator } = await import("@/core/session/browser");
  return { IMSDK: bindSessionSdk(f.sdk, sessionCoordinator) };
});
vi.mock("@/utils/storage", () => ({ getAccessedFriendApplication: f.viewedFriend, getAccessedGroupApplication: f.viewedGroup }));
vi.mock("@/utils/common", () => ({ feedbackToast: f.feedback }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("open-im-sdk-wasm", () => ({ ApplicationHandleResult: { Unprocessed: 0, Agree: 1, Reject: -1 } }));
import { useContactStore } from "@/store/contact";

function deferred<T = any>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const state = () => useContactStore.getState();
const friend = (id = "peer-one", patch: any = {}) => ({ fromUserID: id, toUserID: "same-user", createTime: 1700000000, reqMsg: "hello", handleResult: 0, ...patch });
const group = (id = "applicant-one", patch: any = {}) => ({ userID: id, groupID: "group-one", reqTime: 1700000000, reqMsg: "join", handleResult: 0, ...patch });
function changeTenant() {
  f.owner = { userID: "same-user", issuer: "issuer-B", tenant: { serverID: "tenant-B" } };
  f.generation++;
  f.actor = "same-user";
  state().clearContactStore();
}
const lists = [
  ["getFriendListByReq", "getFriendList", "friendList", [{ friendInfo: { userID: "friend" }, remark: "remark" }]],
  ["getBlackListByReq", "getBlackList", "blackList", [{ userID: "blocked" }]],
  ["getGroupListByReq", "getJoinedGroupList", "groupList", [{ groupID: "joined" }]],
  ["getRecvFriendApplicationListByReq", "getFriendApplicationListAsRecipient", "recvFriendApplicationList", [friend()]],
  ["getSendFriendApplicationListByReq", "getFriendApplicationListAsApplicant", "sendFriendApplicationList", [friend("same-user", { toUserID: "peer" })]],
  ["getRecvGroupApplicationListByReq", "getGroupApplicationListAsRecipient", "recvGroupApplicationList", [group()]],
  ["getSendGroupApplicationListByReq", "getGroupApplicationListAsApplicant", "sendGroupApplicationList", [group("same-user")]],
] as const;
let consoleError: ReturnType<typeof vi.spyOn>;
beforeEach(() => {
  vi.clearAllMocks();
  f.owner = { userID: "same-user", issuer: "issuer-A", tenant: { serverID: "tenant-A" } };
  f.generation = 1; f.actor = "same-user";
  f.viewedFriend.mockResolvedValue([]); f.viewedGroup.mockResolvedValue([]);
  state().clearContactStore();
  consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
});
afterEach(() => consoleError.mockRestore());

describe("real contact lists retain the admitted owner without regressing bootstrap", () => {
  for (const [method, sdk, field, rows] of lists) {
    it(`${method}: preserves a normal successful SDK list`, async () => {
      (f.sdk[sdk] as any).mockResolvedValue({ data: rows });
      await (state()[method] as any)();
      expect(f.sdk[sdk]).toHaveBeenCalledOnce();
      expect((state() as any)[field]).toHaveLength(1);
      if (field === "friendList") expect(state().friendList[0]).toMatchObject({ userID: "friend", remark: "remark" });
      expect(f.feedback).not.toHaveBeenCalled();
    });
    it(`${method}: allows empty UI self-info to hydrate to the same session actor`, async () => {
      f.actor = undefined;
      const slow = deferred(); (f.sdk[sdk] as any).mockReturnValue(slow.promise);
      const pending = (state()[method] as any)();
      expect(f.sdk[sdk]).toHaveBeenCalledOnce(); f.actor = "same-user";
      slow.resolve({ data: rows }); await pending;
      expect((state() as any)[field]).toHaveLength(1);
    });
    it(`${method}: real pre-existing SDK wrapper already rejects cross-generation results`, async () => {
      const slow = deferred(); (f.sdk[sdk] as any).mockReturnValue(slow.promise);
      const pending = (state()[method] as any)(); expect(f.sdk[sdk]).toHaveBeenCalledOnce();
      changeTenant(); slow.resolve({ data: rows }); await pending;
      expect((state() as any)[field]).toEqual([]);
      // This is an existing-wrapper positive control, not a claim that all
      // seven legacy SDK list methods lack result-generation protection.
    });
    it(`${method}: store boundary also rejects same-generation owner-object replacement`, async () => {
      const slow = deferred(); (f.sdk[sdk] as any).mockReturnValue(slow.promise);
      const pending = (state()[method] as any)(); expect(f.sdk[sdk]).toHaveBeenCalledOnce();
      f.owner = { ...f.owner }; slow.resolve({ data: rows }); await pending;
      expect((state() as any)[field]).toEqual([]); expect(f.feedback).not.toHaveBeenCalled();
    });
  }
  it("refuses foreign UI actors before any of the seven SDK list dispatches", async () => {
    f.actor = "foreign-actor";
    for (const [method, sdk] of lists) { await (state()[method] as any)(); expect(f.sdk[sdk]).not.toHaveBeenCalled(); }
  });
  it("rejects foreign self-info hydration after an admitted bootstrap list", async () => {
    f.actor = ""; const slow = deferred(); f.sdk.getBlackList.mockReturnValue(slow.promise);
    const pending = state().getBlackListByReq(); expect(f.sdk.getBlackList).toHaveBeenCalledOnce();
    f.actor = "foreign-actor"; slow.resolve({ data: [{ userID: "blocked" }] }); await pending;
    expect(state().blackList).toEqual([]);
  });
});

describe("already admitted recv events fence the real viewed-marker await", () => {
  for (const family of ["friend", "group"] as const) {
    const update = (row: any) => family === "friend" ? state().updateRecvFriendApplication(row) : state().updateRecvGroupApplication(row);
    const rows = () => family === "friend" ? state().recvFriendApplicationList : state().recvGroupApplicationList;
    const make = family === "friend" ? friend : group;
    const getter = () => family === "friend" ? f.viewedFriend : f.viewedGroup;
    const unread = () => family === "friend" ? state().unHandleFriendApplicationCount : state().unHandleGroupApplicationCount;
    it(`${family}: preserves a normal incoming event and unread count`, async () => {
      await update(make()); expect(getter()).toHaveBeenCalledOnce(); expect(rows()).toEqual([make()]); expect(unread()).toBe(1);
    });
    it(`${family}: drops an admitted event after a real delayed getter crosses tenant at the same UID`, async () => {
      const slow = deferred(); getter().mockReturnValue(slow.promise);
      const pending = update(make()); expect(getter()).toHaveBeenCalledOnce();
      changeTenant(); slow.resolve([]); await pending;
      expect(rows()).toEqual([]); expect(unread()).toBe(0); expect(f.feedback).not.toHaveBeenCalled();
    });
    it(`${family}: rejects equal-field owner replacement without a generation change`, async () => {
      const slow = deferred(); getter().mockReturnValue(slow.promise);
      const pending = update(make()); expect(getter()).toHaveBeenCalledOnce();
      f.owner = { ...f.owner }; slow.resolve([]); await pending;
      expect(rows()).toEqual([]); expect(unread()).toBe(0);
    });
    it(`${family}: merges distinct concurrent events against current state instead of an old snapshot`, async () => {
      const first = deferred(), second = deferred(); getter().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
      const one = update(make("first")), two = update(make("second")); expect(getter()).toHaveBeenCalledTimes(2);
      second.resolve([]); await two; first.resolve([]); await one;
      expect(rows()).toHaveLength(2); expect(unread()).toBe(2);
      expect(rows()).toEqual(expect.arrayContaining([make("first"), make("second")]));
    });
    it(`${family}: an older getter cannot regress the latest admitted row status`, async () => {
      const first = deferred(), second = deferred(); getter().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
      const old = update(make("peer", { handleResult: 0 }));
      const latest = update(make("peer", { handleResult: 1 })); expect(getter()).toHaveBeenCalledTimes(2);
      second.resolve([]); await latest; first.resolve([]); await old;
      expect(rows()).toEqual([make("peer", { handleResult: 1 })]); expect(unread()).toBe(0);
    });
    it(`${family}: freezes the admitted event fields across the getter await`, async () => {
      const slow = deferred(); getter().mockReturnValue(slow.promise);
      const event = make(); const admitted = { ...event }; const pending = update(event);
      expect(getter()).toHaveBeenCalledOnce(); event.reqMsg = "mutated later"; slow.resolve([]); await pending;
      expect(rows()).toEqual([admitted]);
    });
    it(`${family}: stale getter rejection does not touch the next owner's store or feedback`, async () => {
      const slow = deferred(); getter().mockReturnValue(slow.promise);
      const pending = update(make()); expect(getter()).toHaveBeenCalledOnce();
      // Attach before rejection so the old unhandled path cannot be confused
      // with a fixture/module-loader failure when the before source is run.
      const settled = Promise.resolve(pending).catch(() => undefined);
      changeTenant(); slow.reject(new Error("synthetic delayed local read failure")); await settled;
      expect(rows()).toEqual([]); expect(f.feedback).not.toHaveBeenCalled();
    });
    it(`${family}: events require an initialized matching actor before reading markers`, async () => {
      f.actor = undefined; await update(make()); expect(getter()).not.toHaveBeenCalled(); expect(rows()).toEqual([]);
    });
  }
  it("preserves synchronous outgoing friend and group application updates", () => {
    const outgoingFriend = friend("same-user", { toUserID: "peer-out" });
    const outgoingGroup = group("same-user", { groupID: "outgoing-group" });
    state().updateSendFriendApplication(outgoingFriend); state().updateSendGroupApplication(outgoingGroup);
    expect(state().sendFriendApplicationList).toEqual([outgoingFriend]);
    expect(state().sendGroupApplicationList).toEqual([outgoingGroup]);
    expect(f.viewedFriend).not.toHaveBeenCalled(); expect(f.viewedGroup).not.toHaveBeenCalled();
  });
  it("preserves the existing synchronous completed-group-row update contract", async () => {
    useContactStore.setState({ recvGroupApplicationList: [group()], unHandleGroupApplicationCount: 7 });
    await state().updateRecvGroupApplication(group("applicant-one", { handleResult: 1 }));
    expect(f.viewedGroup).not.toHaveBeenCalled(); expect(state().recvGroupApplicationList[0].handleResult).toBe(1);
    expect(state().unHandleGroupApplicationCount).toBe(7);
  });
});
