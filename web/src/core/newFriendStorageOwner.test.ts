// @vitest-environment jsdom
// Actual storage function, delayed real Promise and localForage adapter only.
// Legacy before function accepts/ignores the optional runtime argument; this is
// not a fake missing-prop/undefined callback RED fixture.
import { beforeEach, describe, expect, it, vi } from "vitest";
const f = vi.hoisted(() => ({ write: vi.fn(), restore: vi.fn(),
  current: { userID: "A", issuer: "issuer-A", tenant: { serverID: "tenant-A" } } as any, generation: 1 }));
vi.mock("localforage", () => ({ default: { config: vi.fn(), createInstance: () => ({ setItem: f.write }) } }));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: {
  get current() { return f.current; }, get generation() { return f.generation; }, restore: f.restore,
  get namespace() { return `${f.current.issuer}:${f.current.tenant.serverID}:${f.current.userID}`; },
} }));
import { setAccessedFriendApplication } from "@/utils/storage";
type Capture = { owner: typeof f.current; generation: number };
const write = setAccessedFriendApplication as unknown as (list: string[], capture: Capture) => Promise<unknown>;
function deferred() { let resolve!: (value?: any) => void; const promise = new Promise<any>(yes => { resolve = yes; }); return { promise, resolve }; }
beforeEach(() => { vi.clearAllMocks(); f.current = { userID: "A", issuer: "issuer-A", tenant: { serverID: "tenant-A" } };
  f.generation = 1; f.restore.mockResolvedValue(f.current); f.write.mockResolvedValue(undefined); });
describe("friend application viewed markers preserve the captured account", () => {
  it("does not write old markers to the next account after an actual delayed restore", async () => {
    const slow = deferred(); f.restore.mockReturnValue(slow.promise);
    const pending = write(["original-peer_1700000000"], { owner: f.current, generation: f.generation });
    expect(f.restore).toHaveBeenCalledOnce();
    f.current = { userID: "B", issuer: "issuer-B", tenant: { serverID: "tenant-B" } }; f.generation++;
    slow.resolve(f.current); await pending; expect(f.write).not.toHaveBeenCalled();
  });
  it("requires owner object identity even if replacement retains all fields and generation", async () => {
    const slow = deferred(); f.restore.mockReturnValue(slow.promise);
    const pending = write(["peer_1700000000"], { owner: f.current, generation: 1 });
    expect(f.restore).toHaveBeenCalledOnce(); f.current = { ...f.current };
    slow.resolve(f.current); await pending; expect(f.write).not.toHaveBeenCalled();
  });
  it("preserves valid writes to the exact captured namespace", async () => {
    await write(["peer_1700000000"], { owner: f.current, generation: f.generation });
    expect(f.write).toHaveBeenCalledExactlyOnceWith("issuer-A:tenant-A:A:accessedFriendApplications", ["peer_1700000000"]);
  });
  it("rejects an already stale capture before restore or any account write", async () => {
    const captured = { owner: f.current, generation: f.generation }; f.generation++;
    await write(["peer_1700000000"], captured); expect(f.restore).not.toHaveBeenCalled(); expect(f.write).not.toHaveBeenCalled();
  });
});
