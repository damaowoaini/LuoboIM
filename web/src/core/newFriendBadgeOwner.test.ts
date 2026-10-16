// @vitest-environment jsdom
// Actual complete imCommon module's calcApplicationBadge; only storage/session/
// stores and unrelated formatter/presentation dependencies adapted. No SDK/HTTP.
import { beforeEach, describe, expect, it, vi } from "vitest";
const f = vi.hoisted(() => ({ friendViewed: vi.fn(), groupViewed: vi.fn(), friendCount: vi.fn(), groupCount: vi.fn(),
  owner: { userID: "actor", issuer: "A", tenant: { serverID: "A" } } as any, generation: 1,
  actor: "actor", friends: [] as any[], groups: [] as any[] }));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: {
  get current() { return f.owner; }, get generation() { return f.generation; },
} }));
vi.mock("@/utils/storage", () => ({ getAccessedFriendApplication: f.friendViewed, getAccessedGroupApplication: f.groupViewed }));
vi.mock("@/store", () => ({ useUserStore: { getState: () => ({ selfInfo: { userID: f.actor } }) }, useConversationStore: { getState: vi.fn() } }));
vi.mock("@/store/contact", () => ({ useContactStore: { getState: () => ({
  recvFriendApplicationList: f.friends, recvGroupApplicationList: f.groups,
  updateUnHandleFriendApplicationCount: f.friendCount, updateUnHandleGroupApplicationCount: f.groupCount,
}) } }));
vi.mock("@/core/messagePrivacyRuntime", () => ({ messagePrivacyState: vi.fn() }));
vi.mock("@/utils/common", () => ({ escapeHtml: vi.fn(), generateAvatar: vi.fn(), secondsToTime: vi.fn() }));
vi.mock("open-im-sdk-wasm", () => ({ MessageType: {}, SessionType: { Group: 2, WorkingGroup: 3 } }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("date-fns", () => ({ isThisYear: vi.fn() }));
import { calcApplicationBadge } from "@/utils/imCommon";
type Capture = { owner: typeof f.owner; generation: number };
// Before really invokes the old helper; an extra runtime argument is ignored.
const calculate = calcApplicationBadge as unknown as (capture?: Capture) => Promise<void>;
function deferred() { let resolve!: (value: any) => void; const promise = new Promise<any>(yes => { resolve = yes; }); return { promise, resolve }; }
beforeEach(() => {
  vi.clearAllMocks(); f.owner = { userID: "actor", issuer: "A", tenant: { serverID: "A" } }; f.generation = 1; f.actor = "actor";
  f.friends = [{ fromUserID: "f1", createTime: 1, handleResult: 0 },
    { fromUserID: "f2", createTime: 2, handleResult: 0 }, { fromUserID: "done", createTime: 3, handleResult: 1 }];
  f.groups = [{ userID: "g1", reqTime: 10, handleResult: 0 },
    { userID: "g2", reqTime: 20, handleResult: 0 }, { userID: "done", reqTime: 30, handleResult: -1 }];
  f.friendViewed.mockResolvedValue(["f1_1"]); f.groupViewed.mockResolvedValue(["g1_10"]);
});
describe("real application badge helper stays with its captured owner across both awaits", () => {
  it("drops both badge writes after owner switch during actual friend-viewed read", async () => {
    const slow = deferred(); f.friendViewed.mockReturnValue(slow.promise);
    const pending = calculate({ owner: f.owner, generation: 1 }); expect(f.friendViewed).toHaveBeenCalledOnce();
    f.owner = { userID: "next", issuer: "B", tenant: { serverID: "B" } }; f.actor = "next"; f.generation++;
    slow.resolve([]); await pending;
    expect(f.groupViewed).not.toHaveBeenCalled(); expect(f.friendCount).not.toHaveBeenCalled(); expect(f.groupCount).not.toHaveBeenCalled();
  });
  it("drops both badge writes after owner switch during actual group-viewed read", async () => {
    const slow = deferred(); f.groupViewed.mockReturnValue(slow.promise);
    const pending = calculate({ owner: f.owner, generation: 1 });
    await Promise.resolve(); expect(f.friendViewed).toHaveBeenCalledOnce(); expect(f.groupViewed).toHaveBeenCalledOnce();
    f.owner = { userID: "next", issuer: "B", tenant: { serverID: "B" } }; f.actor = "next"; f.generation++;
    slow.resolve([]); await pending; expect(f.friendCount).not.toHaveBeenCalled(); expect(f.groupCount).not.toHaveBeenCalled();
  });
  it("rejects an equal-field owner replacement at unchanged generation during group await", async () => {
    const slow = deferred(); f.groupViewed.mockReturnValue(slow.promise);
    const pending = calculate({ owner: f.owner, generation: 1 }); await Promise.resolve();
    expect(f.groupViewed).toHaveBeenCalledOnce(); f.owner = { ...f.owner };
    slow.resolve([]); await pending; expect(f.friendCount).not.toHaveBeenCalled(); expect(f.groupCount).not.toHaveBeenCalled();
  });
  it("keeps both normal friend and group unseen counts for a valid owner", async () => {
    await calculate({ owner: f.owner, generation: 1 });
    expect(f.friendCount).toHaveBeenCalledExactlyOnceWith(1); expect(f.groupCount).toHaveBeenCalledExactlyOnceWith(1);
  });
  it("preserves legacy no-capture callers and produces zero for fully viewed pending lists", async () => {
    f.friendViewed.mockResolvedValue(["f1_1", "f2_2"]); f.groupViewed.mockResolvedValue(["g1_10", "g2_20"]);
    await calculate(); expect(f.friendCount).toHaveBeenCalledExactlyOnceWith(0); expect(f.groupCount).toHaveBeenCalledExactlyOnceWith(0);
  });
});
