import type { AdminSession, AdminSessions } from './adminSession';

export async function logoutCurrentAdmin(
  owner: AdminSessions,
  revoke: (session: AdminSession, signal: AbortSignal) => Promise<{ revoked: boolean }>,
  timeoutMs = 10_000,
): Promise<'revoked' | 'unconfirmed' | 'stale'> {
  const generation = owner.generation;
  const session = owner.current;
  const controller = new AbortController();
  let timer: ReturnType<typeof setTimeout> | undefined;
  let confirmed = false;
  try {
    if (session) {
      const timeout = new Promise<never>((_, reject) => {
        timer = setTimeout(() => { controller.abort(); reject(new Error('logout timeout')); }, timeoutMs);
      });
      confirmed = (await Promise.race([revoke({ ...session }, controller.signal), timeout])).revoked === true;
    }
  } catch { /* A network failure does not prevent local credential destruction. */ }
  finally {
    if (timer) clearTimeout(timer);
  }
  if (owner.generation !== generation || owner.current?.adminToken !== session?.adminToken) return 'stale';
  try { owner.clear(); } catch { /* clear revokes in memory before storage access. */ }
  return confirmed ? 'revoked' : 'unconfirmed';
}
