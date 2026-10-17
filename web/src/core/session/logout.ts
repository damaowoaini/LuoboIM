import { Session, SessionCoordinator } from "./session";
import { browserEndpoints } from "./devEndpoints";

export type LogoutState = "closed" | "pending" | "failed" | "local";
export interface LogoutResult { state: LogoutState; cleared: boolean }
export type RemoteLogout = (session: Session, signal: AbortSignal) => Promise<"closed" | "pending">;
export type SessionLogout = ((force?: boolean) => Promise<LogoutResult>) & { isPending(): boolean };

// Capture both tokens and the tenant before awaiting anything. A late response
// must never revoke the local state of a newly logged-in account.
export function createSessionLogout(
  coordinator: SessionCoordinator,
  remote: RemoteLogout,
  clear: (state: LogoutState) => void | Promise<void>,
): SessionLogout {
  const pending = new Map<string, Promise<LogoutResult>>();
  const logout = (force = false): Promise<LogoutResult> => {
    const session = coordinator.current;
    const generation = coordinator.generation;
    const key = `${generation}:${force}`;
    const existing = pending.get(key);
    if (existing) return existing;
    const operation = (async (): Promise<LogoutResult> => {
      let state: LogoutState = "local";
      if (session && !force) {
        const lease = coordinator.lease();
        try { state = await remote(session, lease.signal); }
        catch { state = "failed"; }
        finally { lease.release(); }
      }
      if (coordinator.generation !== generation || coordinator.current !== session)
        return { state, cleared: false };
      // clear must invalidate synchronously, before returning its promise.
      // The SDK lifecycle binding handles logout/worker close/document restart.
      await clear(state);
      return { state, cleared: true };
    })();
    pending.set(key, operation);
    void operation.finally(() => pending.delete(key)).catch(() => undefined);
    return operation;
  };
  logout.isPending = () => pending.has(`${coordinator.generation}:false`);
  return logout;
}

export async function revokeCurrentSession(
  session: Session,
  signal: AbortSignal,
  send: typeof fetch = fetch,
  timeoutMs = 10000,
): Promise<"closed" | "pending"> {
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal.addEventListener("abort", abort, { once: true });
  if (signal.aborted) abort();
  const timeout = setTimeout(abort, timeoutMs);
  try {
    const response = await send(`${browserEndpoints(session).chat}/account/logout`, {
      method: "POST",
      headers: { "Content-Type": "application/json", token: session.chatToken, operationID: crypto.randomUUID() },
      body: JSON.stringify({ imToken: session.imToken }),
      signal: controller.signal,
      credentials: "omit",
      redirect: "error",
      cache: "no-store",
    });
    if (!response.ok) throw new Error("注销请求未确认");
    const body = await response.json();
    if (body?.errCode !== 0 || body?.data?.revoked !== true)
      throw new Error("注销请求未确认");
    if (body.data.sessionClosed === true && body.data.pending === false) return "closed";
    if (body.data.sessionClosed === false && body.data.pending === true) return "pending";
    throw new Error("注销响应格式错误");
  } finally {
    clearTimeout(timeout);
    signal.removeEventListener("abort", abort);
  }
}
