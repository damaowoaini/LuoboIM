import { SessionCoordinator, StaleSessionError } from "./session";

type SdkLike = {
  on(event: unknown, callback: (...args: any[]) => any): unknown;
  off(event: unknown, callback: (...args: any[]) => any): unknown;
  login(...args: any[]): Promise<unknown>;
  logout(...args: any[]): Promise<unknown>;
};
// One SDK object is shared by all historical screens. Central binding guards
// every subscription and asynchronous result, including calls made outside the
// main event hook, so old-account events cannot populate the next account.
export interface SdkLifecycleHooks {
  prepareLogin?(args: unknown[]): Promise<unknown[]>;
  afterLogout?(): Promise<void>;
}
export function bindSessionSdk<T extends object>(
  original: T,
  session: SessionCoordinator,
  hooks: SdkLifecycleHooks = {},
): T {
  const sdk = original as unknown as SdkLike;
  const logout = async (args: unknown[] = [], allowUnauthenticated = false) => {
    try {
      return await sdk.logout(...args);
    } catch (error) {
      if (!allowUnauthenticated) throw error;
    } finally {
      await hooks.afterLogout?.();
    }
  };
  const subscriptions = new Map<unknown, Map<Function, Function[]>>();
  let lifecycle: Promise<unknown> = Promise.resolve();
  const enqueue = <R>(work: () => Promise<R>): Promise<R> => {
    const result = lifecycle.then(work, work);
    lifecycle = result.then(
      () => undefined,
      () => undefined,
    );
    return result;
  };
  let previous = session.current;
  let generation = session.generation;
  session.onChange(() => {
    const next = session.current;
    if (generation !== session.generation) {
      for (const [event, callbacks] of subscriptions)
        for (const wrappers of callbacks.values())
          for (const callback of wrappers)
            sdk.off(event, callback as (...args: any[]) => any);
      subscriptions.clear();
      generation = session.generation;
    }
    if (previous && !next) void enqueue(() => logout()).catch(() => {});
    previous = next;
  });
  return new Proxy(original, {
    get(target, property, receiver) {
      if (property === "on")
        return (event: unknown, callback: (...args: any[]) => any) => {
          const guarded = session.guard(callback);
          const callbacks = subscriptions.get(event) ?? new Map();
          callbacks.set(callback, [...(callbacks.get(callback) ?? []), guarded]);
          subscriptions.set(event, callbacks);
          return sdk.on(event, guarded);
        };
      if (property === "off")
        return (event: unknown, callback: (...args: any[]) => any) => {
          const callbacks = subscriptions.get(event);
          for (const guarded of callbacks?.get(callback) ?? [])
            sdk.off(event, guarded as (...args: any[]) => any);
          callbacks?.delete(callback);
        };
      const value = Reflect.get(target, property, receiver);
      if (typeof value !== "function") return value;
      if (property === "logout")
        return (...args: unknown[]) => enqueue(() => logout(args));
      if (property === "login")
        return (...args: unknown[]) => {
          const generation = session.generation;
          return enqueue(async () => {
            if (generation !== session.generation) throw new StaleSessionError();
            // Ignore only the SDK's unauthenticated logout error. A worker
            // close failure must stop the next account from opening its DB.
            await logout([], true);
            const prepared = hooks.prepareLogin ? await hooks.prepareLogin(args) : args;
            if (generation !== session.generation) throw new StaleSessionError();
            const result = await sdk.login(...prepared);
            if (generation !== session.generation) {
              try {
                await logout();
              } catch {
                /* Best effort after obsolete login. */
              }
              throw new StaleSessionError();
            }
            return result;
          });
        };
      return (...args: unknown[]) => {
        const generation = session.generation;
        const result = value.apply(target, args);
        if (!result || typeof result.then !== "function") return result;
        return result.then((data: unknown) => {
          if (generation !== session.generation) throw new StaleSessionError();
          return data;
        });
      };
    },
  });
}
