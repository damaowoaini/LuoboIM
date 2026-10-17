/** Session-only read evidence. No body, URL, token, clock or expiry is retained.
 * A UI unmount or A -> B -> A does not erase an already observed read ACK.
 * This is not an SDK/server write or a claim that isRead supplies readTime. */
export type ReadIdentity = Readonly<{ owner: string; conversationID: string;
  clientMsgID: string; sendID: string; recvID: string; seq: number }>;
const key = (row: ReadIdentity) => JSON.stringify([row.owner, row.conversationID,
  row.clientMsgID, row.sendID, row.recvID, row.seq]);
const acknowledged = new Set<string>();
const active = new Set<string>();
const listeners = new Set<() => void>();
const publish = () => { for (const listener of listeners) listener(); };
export const readBarrier = {
  subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; },
  acknowledge(rows: ReadIdentity[]) {
    let changed = false;
    for (const row of rows) { const id = key(row); if (!acknowledged.has(id)) { acknowledged.add(id); changed = true; } }
    if (changed) publish();
  },
  acknowledged(row: ReadIdentity) { return acknowledged.has(key(row)); },
  busy(row: ReadIdentity) { return active.has(key(row)); },
  claim(rows: ReadIdentity[]) {
    const keys = rows.map(key);
    if (!keys.length || keys.some(id => active.has(id))) return;
    keys.forEach(id => active.add(id)); publish();
    let ended = false;
    return () => { if (!ended) { ended = true; keys.forEach(id => active.delete(id)); publish(); } };
  },
  clear() { acknowledged.clear(); active.clear(); publish(); },
};

export function readIdentity(owner: string, conversationID: string, value: unknown): ReadIdentity | undefined {
  if (!owner || !conversationID || !value || typeof value !== "object") return;
  const row = value as Record<string, unknown>;
  if (row.sessionType !== 1 || row.groupID || row.conversationID != null && row.conversationID !== conversationID ||
      typeof row.clientMsgID !== "string" || !row.clientMsgID || typeof row.sendID !== "string" || !row.sendID ||
      typeof row.recvID !== "string" || !row.recvID || !Number.isSafeInteger(row.seq) || (row.seq as number) <= 0) return;
  return { owner, conversationID, clientMsgID: row.clientMsgID, sendID: row.sendID, recvID: row.recvID, seq: row.seq as number };
}
