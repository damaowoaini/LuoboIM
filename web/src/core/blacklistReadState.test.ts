// @vitest-environment jsdom
// Real contact store + real SDK generation wrapper. Only SDK transport,
// session authority, UI actor, viewed-marker IO and toast presentation adapt.
import { beforeEach, describe, expect, it, vi } from "vitest";

const f = vi.hoisted(() => ({ owner: {} as any, generation: 1, actor: "actor" as string | undefined,
  read: vi.fn(), remove: vi.fn(), viewed: vi.fn(), feedback: vi.fn() }));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: {
  get current() { return f.owner; }, get generation() { return f.generation; },
  onChange: () => () => undefined,
} }));
vi.mock("@/store/user", () => ({ useUserStore: { getState: () => ({ selfInfo: { userID: f.actor } }) } }));
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
import { useContactStore } from "@/store/contact";

const state = () => useContactStore.getState();
const expected = () => ({ owner: f.owner, generation: f.generation });
// The unchanged before API ignores the new optional argument. An absent new
// status accessor produces an assertion failure, never an undefined-call load error.
const phase = (scope = expected()) => (state() as any).getBlackListReadPhase?.(scope);
const read = (scope?: any) => (state().getBlackListByReq as (scope?: any) => Promise<void>)(scope);
const one = { userID: "blocked-one", nickname: "同名", faceURL: "" } as any;
const two = { ...one, userID: "blocked-two" } as any;
function deferred() { let resolve!: (v: any) => void, reject!: (v: any) => void;
  const promise = new Promise<any>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
beforeEach(() => {
  vi.resetAllMocks(); f.generation = 1; f.actor = "actor";
  f.owner = { userID: "actor", issuer: "issuer-A", tenant: { serverID: "tenant-A" },
    chatToken: "synthetic-chat-secret", imToken: "synthetic-im-secret" };
  f.read.mockResolvedValue({ data: [] }); f.viewed.mockResolvedValue([]);
  state().clearContactStore();
});

describe("blacklist owner-bound actual read state", () => {
  it("keeps the normal genuine SDK list result", async () => {
    f.read.mockResolvedValueOnce({ data: [one, two] }); await read();
    expect(f.read).toHaveBeenCalledOnce(); expect(state().blackList).toEqual([one, two]);
  });
  it("distinguishes an actual pending read from successful empty", async () => {
    const slow = deferred(); f.read.mockReturnValueOnce(slow.promise); const pending = read();
    try { expect(f.read).toHaveBeenCalledOnce(); expect(phase()).toBe("loading"); }
    finally { slow.resolve({ data: [] }); await pending; }
    expect(phase()).toBe("ready"); expect(state().blackList).toEqual([]);
  });
  it("records a failed initial read as error instead of confirmed empty", async () => {
    f.read.mockRejectedValueOnce(new Error("synthetic read")); await read();
    expect(phase()).toBe("error"); expect(state().blackList).toEqual([]);
  });
  it("keeps this owner's successful rows when an actual refresh fails", async () => {
    f.read.mockResolvedValueOnce({ data: [one] }); await read();
    f.read.mockRejectedValueOnce(new Error("refresh")); await read();
    expect(state().blackList).toEqual([one]); expect(phase()).toBe("error");
  });
  it("does not let an older successful read replace the newer ready list", async () => {
    const slow = deferred(); f.read.mockReturnValueOnce(slow.promise); const old = read();
    f.read.mockResolvedValueOnce({ data: [two] }); await read();
    slow.resolve({ data: [one] }); await old;
    expect(state().blackList).toEqual([two]); expect(phase()).toBe("ready");
  });
  it("does not let an older rejection replace the newer ready state", async () => {
    const slow = deferred(); f.read.mockReturnValueOnce(slow.promise); const old = read();
    f.read.mockResolvedValueOnce({ data: [two] }); await read();
    slow.reject(new Error("late")); await old;
    expect(state().blackList).toEqual([two]); expect(phase()).toBe("ready");
  });
  it("clears A row provenance atomically when B starts and preserves none on B error", async () => {
    f.read.mockResolvedValueOnce({ data: [one] }); await read();
    f.owner = { ...f.owner, issuer: "issuer-B", tenant: { serverID: "tenant-B" } }; f.generation++;
    const slow = deferred(); f.read.mockReturnValueOnce(slow.promise); const pending = read();
    try { expect(f.read).toHaveBeenCalledTimes(2); expect(state().blackList).toEqual([]);
      expect(phase()).toBe("loading"); }
    finally { slow.reject(new Error("B read")); await pending; }
    expect(state().blackList).toEqual([]); expect(phase()).toBe("error");
  });
  it("retains the existing SDK/store protection against a generation change", async () => {
    const slow = deferred(); f.read.mockReturnValueOnce(slow.promise); const pending = read();
    expect(f.read).toHaveBeenCalledOnce(); f.generation++; slow.resolve({ data: [one] }); await pending;
    expect(state().blackList).toEqual([]); expect(f.feedback).not.toHaveBeenCalled();
  });
  it("retains the existing store protection against equal-field owner replacement", async () => {
    const slow = deferred(); f.read.mockReturnValueOnce(slow.promise); const pending = read();
    f.owner = { ...f.owner }; slow.resolve({ data: [one] }); await pending;
    expect(state().blackList).toEqual([]); expect(f.feedback).not.toHaveBeenCalled();
  });
  it("retains issuer and tenant mutation rejection for admitted reads", async () => {
    const slow = deferred(); f.read.mockReturnValueOnce(slow.promise); const pending = read();
    f.owner.issuer = "issuer-B"; f.owner.tenant.serverID = "tenant-B";
    slow.resolve({ data: [one] }); await pending; expect(state().blackList).toEqual([]);
  });
  it("does not dispatch for a foreign UI actor", async () => {
    f.actor = "foreign"; await read(); expect(f.read).not.toHaveBeenCalled();
  });
  it("preserves authenticated bootstrap reads with initially empty self-info", async () => {
    f.actor = undefined; const slow = deferred(); f.read.mockReturnValueOnce(slow.promise);
    const pending = read(); expect(f.read).toHaveBeenCalledOnce(); f.actor = "actor";
    slow.resolve({ data: [one] }); await pending; expect(state().blackList).toEqual([one]);
  });
  it("treats malformed SDK rows as error rather than ready data", async () => {
    f.read.mockResolvedValueOnce({ data: [{ nickname: "missing ID" }] }); await read();
    expect(phase()).toBe("error"); expect(state().blackList).toEqual([]);
  });
  it("exposes only a phase and opaque request number without credentials", async () => {
    await read(); const ticket = (state() as any).blackListReadState;
    expect(ticket).toBeDefined(); expect(Object.keys(ticket).sort()).toEqual(["phase", "requestID"]);
    expect(JSON.stringify(ticket)).not.toMatch(/secret|chatToken|imToken|issuer|tenant|owner|actor/);
  });
  it("does not refill a cleared ticket from its earlier same-owner read", async () => {
    const slow = deferred(); f.read.mockReturnValueOnce(slow.promise); const pending = read();
    state().clearContactStore(); slow.resolve({ data: [one] }); await pending;
    expect(state().blackList).toEqual([]); expect((state() as any).blackListReadState).toBeUndefined();
  });
  it("refuses the new expected-owner contract before dispatch", async () => {
    await read({ owner: { ...f.owner }, generation: f.generation }); expect(f.read).not.toHaveBeenCalled();
  });
});
