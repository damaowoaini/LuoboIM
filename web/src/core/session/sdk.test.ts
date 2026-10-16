import { it, expect } from "vitest";
import { bindSessionSdk } from "./sdk";
import { SessionCoordinator } from "./session";
const fixture = () =>
  new SessionCoordinator({
    read: async () => undefined,
    write: async () => {},
    clear: async () => {},
  });
it("drops SDK subscriptions from an old generation and unregisters original callback references", async () => {
  const session = fixture();
  let events = 0;
  let listener: () => void = () => {};
  let removed: unknown;
  const sdk = bindSessionSdk(
    {
      on: (_: unknown, cb: () => void) => {
        listener = cb;
      },
      off: (_: unknown, cb: unknown) => {
        removed = cb;
      },
      login: async () => {},
      logout: async () => {},
    },
    session,
  );
  const callback = () => events++;
  sdk.on("message", callback);
  listener();
  await session.clear();
  listener();
  sdk.off("message", callback);
  expect(events).toBe(1);
  expect(removed).toBe(listener);
});
it("rejects a late SDK query result after session clear", async () => {
  const session = fixture();
  let finish: (value: string) => void = () => {};
  const sdk = bindSessionSdk(
    {
      on: () => {},
      off: () => {},
      login: async () => {},
      logout: async () => {},
      query: () =>
        new Promise<string>((resolve) => {
          finish = resolve;
        }),
    },
    session,
  );
  const query = sdk.query();
  await session.clear();
  finish("old-user");
  await expect(query).rejects.toThrow("会话已切换");
});
it("waits for SQL persistence/close before preparing and logging into another database", async () => {
  const session = fixture();
  const order: string[] = [];
  let finishClose!: () => void;
  let enteredClose!: () => void;
  const closing = new Promise<void>((resolve) => {
    finishClose = resolve;
  });
  const started = new Promise<void>((resolve) => {
    enteredClose = resolve;
  });
  const sdk = bindSessionSdk(
    {
      on: () => {},
      off: () => {},
      login: async (..._args: unknown[]) => {
        order.push("login");
      },
      logout: async () => {
        order.push("logout");
      },
    },
    session,
    {
      afterLogout: async () => {
        enteredClose();
        await closing;
        order.push("db-closed");
      },
      prepareLogin: async (args) => {
        order.push("prepared");
        return args;
      },
    },
  );
  const login = sdk.login({ userID: "same-user" });
  await started;
  expect(order).toEqual(["logout"]);
  finishClose();
  await login;
  expect(order).toEqual(["logout", "db-closed", "prepared", "login"]);
});
it("fails closed when the previous SQL worker cannot close", async () => {
  const session = fixture();
  let logins = 0;
  const sdk = bindSessionSdk(
    {
      on: () => {},
      off: () => {},
      login: async () => {
        logins++;
      },
      logout: async () => {
        throw new Error("not logged in");
      },
    },
    session,
    {
      afterLogout: async () => {
        throw new Error("SQL close failed");
      },
    },
  );
  await expect(sdk.login()).rejects.toThrow("SQL close failed");
  expect(logins).toBe(0);
});
