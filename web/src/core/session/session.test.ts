import { describe, it, expect } from "vitest";
import { endpoints, Session, SessionCoordinator, SessionStorage, StaleSessionError } from "./session";
const response = (id = "u", tenant = "tenant") => ({ issuer: "https://route.example", userID: id, imToken: `im-${id}`, chatToken: `chat-${id}`, serverInfo: { serverID: tenant, serverName: "萝卜", domains: ["main.example", "backup.example"] } });
const fixture = () => {
  let disk: unknown;
  const storage: SessionStorage = { read: async () => disk, write: async value => { disk = structuredClone(value); }, clear: async () => { disk = undefined; } };
  return { coordinator: new SessionCoordinator(storage), storage, readDisk: () => disk as Session };
};
describe("Tenant session lifecycle", () => {
  it("does not resurrect credentials when logout happens during an IndexedDB commit", async () => {
    const { coordinator, storage } = fixture();
    let finish: () => void = () => {};
    storage.write = () => new Promise<void>(resolve => { finish = resolve; });
    const committing = coordinator.commit(response());
    await Promise.resolve();
    const clearing = coordinator.clear();
    finish();
    await expect(committing).rejects.toThrow(StaleSessionError);
    await clearing;
    expect(coordinator.current).toBeUndefined();
  });

  it("commits both credentials and tenant as one record before callers proceed", async () => {
    const { coordinator, readDisk } = fixture();
    await coordinator.commit(response());
    expect(readDisk().tenant.serverID).toBe("tenant");
    expect(readDisk().chatToken).toBe("chat-u");
    expect(endpoints(coordinator.current!).im).toBe("https://main.example/api");
  });
  it("does not retain a partial active session if persistence fails", async () => {
    const { coordinator, storage } = fixture();
    storage.write = async () => { throw new Error("disk full"); };
    await expect(coordinator.commit(response())).rejects.toThrow("disk full");
    expect(coordinator.current).toBeUndefined();
  });
  it("cancels requests and rejects late HTTP and SDK callbacks after logout", async () => {
    const { coordinator } = fixture(); await coordinator.commit(response());
    const lease = coordinator.lease(); let events = 0; const callback = coordinator.guard(() => events++);
    await coordinator.clear();
    expect(lease.signal.aborted).toBe(true);
    expect(() => lease.assertCurrent()).toThrow(StaleSessionError);
    callback(); expect(events).toBe(0);
  });
  it("rejects an older concurrent login instead of overwriting a newer session", async () => {
    const { coordinator } = fixture(); const generation = coordinator.generation;
    await coordinator.commit(response("newer"), generation);
    await expect(coordinator.commit(response("older"), generation)).rejects.toThrow(StaleSessionError);
    expect(coordinator.current!.userID).toBe("newer");
  });
  it("rolls back a failed line reconnect and leaves persisted credentials unchanged", async () => {
    const { coordinator, readDisk } = fixture(); await coordinator.commit(response());
    let calls = 0;
    await expect(coordinator.switchLine(1, async () => { if (++calls === 1) throw new Error("offline"); })).rejects.toThrow("offline");
    expect(coordinator.current!.tenant.activeLine).toBe(0);
    expect(readDisk().tenant.activeLine).toBe(0);
    expect(calls).toBe(2);
  });
  it("rejects credentials without tenant identity or with an untrusted URL", async () => {
    const { coordinator } = fixture();
    for (const domains of [["https://good.example@evil.example"], ["host.example/path"], ["host.example?token=x"], []]) {
      await expect(coordinator.commit({ ...response(), serverInfo: { serverID: "t", serverName: "x", domains } })).rejects.toThrow();
    }
    await expect(coordinator.commit({ userID: "u", chatToken: "c", imToken: "i" })).rejects.toThrow("租户");
  });
  it("namespaces equal user IDs from different tenants independently", async () => {
    const { coordinator } = fixture(); await coordinator.commit(response("u", "a")); const first = coordinator.namespace;
    await coordinator.commit(response("u", "b")); expect(coordinator.namespace).not.toBe(first);
  });
  it("ignores a late disk restore after explicit logout", async () => {
    let resolve: (value: unknown) => void = () => {};
    const disk = new Promise(value => { resolve = value; });
    const coordinator = new SessionCoordinator({ read: () => disk, write: async () => {}, clear: async () => {} });
    const restoring = coordinator.restore(); await coordinator.clear();
    const saved = fixture(); await saved.coordinator.commit(response()); resolve(saved.readDisk()); await restoring;
    expect(coordinator.current).toBeUndefined();
  });
});
