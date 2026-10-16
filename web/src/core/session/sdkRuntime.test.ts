import { afterEach, expect, it, vi } from "vitest";
import { SessionCoordinator, StaleSessionError } from "./session";
import { SdkRuntimeBoundary } from "./sdkRuntime";
const deferred = () => {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
};
const response = (tenant = "a") => ({
  issuer: "https://route.example",
  userID: "same-user",
  chatToken: "synthetic-chat",
  imToken: "synthetic-im",
  serverInfo: { serverID: tenant, serverName: tenant, domains: ["tenant.example"] },
});
const flush = async () => {
  for (let i = 0; i < 12; i++) await Promise.resolve();
};
afterEach(() => vi.useRealTimers());
it("does not restart on the first authenticated session before Go SDK initialization", async () => {
  const session = new SessionCoordinator({
    read: async () => undefined,
    write: async () => {},
    clear: async () => {},
  });
  const restart = vi.fn();
  const boundary = new SdkRuntimeBoundary(session, restart, async () => {});
  await session.commit(response());
  boundary.beforeLogin();
  await flush();
  expect(restart).not.toHaveBeenCalled();
});
it("persists logout revocation and awaits worker shutdown before restarting the document", async () => {
  const removal = deferred(),
    shutdown = deferred();
  const session = new SessionCoordinator({
    read: async () => undefined,
    write: async () => {},
    clear: () => removal.promise,
  });
  await session.commit(response());
  const restart = vi.fn();
  const boundary = new SdkRuntimeBoundary(session, restart, () => shutdown.promise);
  boundary.beforeLogin();
  const clearing = session.clear();
  await flush();
  expect(restart).not.toHaveBeenCalled();
  removal.resolve();
  await clearing;
  await flush();
  expect(restart).not.toHaveBeenCalled();
  shutdown.resolve();
  await flush();
  expect(restart).toHaveBeenCalledOnce();
});
it("persists replacement credentials, refuses a second Go init and needs no persistent reload marker", async () => {
  const write = deferred();
  let disk: unknown;
  let block = false;
  const session = new SessionCoordinator({
    read: async () => disk,
    write: async (value) => {
      if (block) await write.promise;
      disk = value;
    },
    clear: async () => {
      disk = undefined;
    },
  });
  await session.commit(response());
  const restart = vi.fn();
  const boundary = new SdkRuntimeBoundary(session, restart, async () => {});
  boundary.beforeLogin();
  block = true;
  const switching = session.commit(response("b"));
  await flush();
  expect(restart).not.toHaveBeenCalled();
  expect(() => boundary.beforeLogin()).toThrow(StaleSessionError);
  write.resolve();
  await switching;
  await flush();
  expect(session.current?.tenant.serverID).toBe("b");
  expect(restart).toHaveBeenCalledOnce();
  const fresh = new SessionCoordinator({
    read: async () => disk,
    write: async () => {},
    clear: async () => {},
  });
  const freshRestart = vi.fn();
  const freshBoundary = new SdkRuntimeBoundary(fresh, freshRestart, async () => {});
  await fresh.restore();
  freshBoundary.beforeLogin();
  await flush();
  expect(freshRestart).not.toHaveBeenCalled();
});
it("releases a stuck singleton by restarting after the bounded shutdown timeout", async () => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  const session = new SessionCoordinator({
    read: async () => undefined,
    write: async () => {},
    clear: async () => {},
  });
  await session.commit(response());
  const restart = vi.fn();
  const boundary = new SdkRuntimeBoundary(
    session,
    restart,
    () => new Promise(() => {}),
    50,
  );
  boundary.beforeLogin();
  await session.clear();
  await flush();
  await vi.advanceTimersByTimeAsync(50);
  expect(restart).toHaveBeenCalledOnce();
});
