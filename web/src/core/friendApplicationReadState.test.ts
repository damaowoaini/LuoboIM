// @vitest-environment jsdom
// Actual Zustand getters; only SDK transport, actor and local viewed-marker I/O adapt.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const f = vi.hoisted(() => ({
  owner: {} as any, generation: 1, actor: "actor" as string | undefined,
  recv: vi.fn(), send: vi.fn(), viewed: vi.fn(), feedback: vi.fn(),
}));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: {
  get current() { return f.owner; }, get generation() { return f.generation; },
} }));
vi.mock("@/store/user", () => ({ useUserStore: { getState: () => ({ selfInfo: { userID: f.actor } }) } }));
vi.mock("@/layout/MainContentWrap", () => ({ IMSDK: {
  getFriendApplicationListAsRecipient: f.recv, getFriendApplicationListAsApplicant: f.send,
} }));
vi.mock("@/utils/storage", () => ({ getAccessedFriendApplication: f.viewed, getAccessedGroupApplication: f.viewed }));
vi.mock("@/utils/common", () => ({ feedbackToast: f.feedback }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("open-im-sdk-wasm", () => ({ ApplicationHandleResult: { Unprocessed: 0, Agree: 1, Reject: -1 } }));
import { useContactStore } from "@/store/contact";

const state = () => useContactStore.getState();
const expected = () => ({ owner: f.owner, generation: f.generation });
const status = (direction: "recv" | "send") => (state() as any)[`${direction}FriendApplicationReadState`];
// Optional access lets the same assertions load against the genuine before API;
// an absent status surface is an assertion failure, never an undefined-method setup error.
const phase = (direction: "recv" | "send", owner = expected()) =>
  (state() as any).getFriendApplicationReadPhase?.(direction, owner);
const list = (direction: "recv" | "send") => direction === "recv" ? state().recvFriendApplicationList : state().sendFriendApplicationList;
const request = (direction: "recv" | "send", owner?: any) => direction === "recv"
  ? state().getRecvFriendApplicationListByReq(owner) : state().getSendFriendApplicationListByReq(owner);
function deferred() { let resolve!: (v: any) => void, reject!: (v: any) => void;
  const promise = new Promise<any>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
const row = { fromUserID: "peer", toUserID: "actor", reqMsg: "hi", createTime: 1700000000, handleResult: 0 } as any;
let consoleError: ReturnType<typeof vi.spyOn>;
beforeEach(() => {
  vi.resetAllMocks(); f.owner = { userID: "actor", issuer: "issuer-A", tenant: { serverID: "A" },
    chatToken: "synthetic-chat-secret", imToken: "synthetic-im-secret" }; f.generation = 1; f.actor = "actor";
  f.recv.mockResolvedValue({ data: [] }); f.send.mockResolvedValue({ data: [] }); f.viewed.mockResolvedValue([]);
  state().clearContactStore(); consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
});
afterEach(() => consoleError.mockRestore());

describe("owner-bound friend application read state", () => {
  for (const direction of ["recv", "send"] as const) {
    const sdk = () => direction === "recv" ? f.recv : f.send;
    it(`${direction}: dispatches a real list read and distinguishes loading from a confirmed empty result`, async () => {
      const slow = deferred(); sdk().mockReturnValue(slow.promise); const pending = request(direction);
      try { expect(sdk()).toHaveBeenCalledOnce(); expect(status(direction)).toMatchObject({ phase: "loading" });
        expect(phase(direction)).toBe("loading"); }
      finally { slow.resolve({ data: [] }); await pending; }
      expect(status(direction)).toMatchObject({ phase: "ready" }); expect(phase(direction)).toBe("ready"); expect(list(direction)).toEqual([]);
    });
    it(`${direction}: preserves successful rows and records error after a failed refresh`, async () => {
      sdk().mockResolvedValueOnce({ data: [row] }); await request(direction);
      expect(list(direction)).toEqual([row]); sdk().mockRejectedValueOnce(new Error("synthetic read failure"));
      await request(direction); expect(list(direction)).toEqual([row]); expect(phase(direction)).toBe("error");
    });
    it(`${direction}: a new owner's loading or failed read cannot expose the prior owner's rows`, async () => {
      sdk().mockResolvedValueOnce({ data: [row] }); await request(direction); expect(list(direction)).toEqual([row]);
      f.owner = { ...f.owner, issuer: "issuer-B", tenant: { serverID: "B" } }; f.generation++;
      const slow = deferred(); sdk().mockReturnValueOnce(slow.promise); const pending = request(direction);
      try {
        expect(sdk()).toHaveBeenCalledTimes(2); expect(list(direction)).toEqual([]);
        expect(phase(direction)).toBe("loading");
      } finally { slow.reject(new Error("new-owner read failure")); await pending; }
      expect(list(direction)).toEqual([]); expect(phase(direction)).toBe("error");
    });
    it(`${direction}: a malformed result is an error, not an empty ready list`, async () => {
      sdk().mockResolvedValueOnce({ data: null }); await request(direction);
      expect(phase(direction)).toBe("error"); expect(list(direction)).toEqual([]);
    });
    it(`${direction}: the latest admitted read wins and an older rejection cannot replace ready`, async () => {
      const old = deferred(); sdk().mockReturnValueOnce(old.promise).mockResolvedValueOnce({ data: [row] });
      const first = request(direction); expect(sdk()).toHaveBeenCalledOnce(); await request(direction);
      old.reject(new Error("old read")); await first;
      expect(list(direction)).toEqual([row]); expect(phase(direction)).toBe("ready");
    });
    it(`${direction}: clear invalidates an inflight same-owner ticket without refilling rows or status`, async () => {
      const slow = deferred(); sdk().mockReturnValue(slow.promise); const pending = request(direction);
      expect(sdk()).toHaveBeenCalledOnce(); state().clearContactStore(); slow.resolve({ data: [row] }); await pending;
      expect(list(direction)).toEqual([]); expect(phase(direction)).toBe("idle");
    });
    it(`${direction}: the original owner cannot query a status after equal-field owner replacement`, async () => {
      const original = expected(); await request(direction); f.owner = { ...f.owner };
      expect(phase(direction, original)).toBe("idle"); expect(phase(direction)).toBe("idle");
    });
    it(`${direction}: stale expected-owner retry is rejected before the SDK read`, async () => {
      const original = expected(); f.owner = { ...f.owner }; await request(direction, original);
      expect(sdk()).not.toHaveBeenCalled();
    });
  }
  it("does not adopt unbound cached rows as a current owner's loading or failed result", async () => {
    useContactStore.setState({ recvFriendApplicationList: [row], sendFriendApplicationList: [row] });
    const recv = deferred(), send = deferred(); f.recv.mockReturnValueOnce(recv.promise); f.send.mockReturnValueOnce(send.promise);
    const pending = Promise.all([request("recv"), request("send")]);
    try {
      expect(f.recv).toHaveBeenCalledOnce(); expect(f.send).toHaveBeenCalledOnce();
      expect(list("recv")).toEqual([]); expect(list("send")).toEqual([]);
    } finally { recv.reject(new Error("unbound read")); send.reject(new Error("unbound read")); await pending; }
    expect(list("recv")).toEqual([]); expect(list("send")).toEqual([]);
    expect(phase("recv")).toBe("error"); expect(phase("send")).toBe("error");
  });
  it("read status serialization contains no Session, actor, tenant or credentials", async () => {
    await request("recv"); const read = status("recv"); expect(read).toBeDefined();
    expect(Object.keys(read).sort()).toEqual(["phase", "requestID"]);
    const serialized = JSON.stringify(read);
    for (const secret of ["synthetic-chat-secret", "synthetic-im-secret", "chatToken", "imToken", "issuer-A", '"owner"', '"actor"']) expect(serialized).not.toContain(secret);
    expect(phase("recv")).toBe("ready");
  });
  it("bootstrap reads still hydrate to the authenticated actor; direct empty-actor events remain refused", async () => {
    f.actor = undefined; await state().updateRecvFriendApplication(row);
    expect(f.viewed).not.toHaveBeenCalled(); expect(state().recvFriendApplicationList).toEqual([]);
    const slow = deferred(); f.recv.mockReturnValue(slow.promise); const pending = request("recv");
    expect(f.recv).toHaveBeenCalledOnce(); f.actor = "actor"; slow.resolve({ data: [row] }); await pending;
    expect(state().recvFriendApplicationList).toEqual([row]); expect(phase("recv")).toBe("ready");
  });
});
