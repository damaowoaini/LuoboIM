/** OpenIM message privacy policy. Does not change the vendor WASM or delete data. */
export type PrivacyInfo = {
  containsPrivate: boolean;
  rootPrivate: boolean;
  inspectionComplete: boolean;
  expiresAt?: number;
};
type RecordValue = Record<string, unknown>;
const record = (value: unknown): value is RecordValue =>
  !!value && typeof value === "object" && !Array.isArray(value);
const integer = (value: unknown): value is number => Number.isSafeInteger(value);
const MAX_TIMESTAMP = 8640000000000000;

export function privateExpiry(readAt: unknown, duration: unknown): number | undefined {
  if (!integer(readAt) || readAt <= 0 || duration != null && (!integer(duration) || duration < 0)) return;
  const time = readAt >= 946684800 && readAt <= 4102444800 ? readAt * 1000 : readAt >= 946684800000 && readAt <= 4102444800000 ? readAt : undefined;
  if (time === undefined) return;
  const seconds = duration == null || duration === 0 ? 30 : duration as number;
  if (time > MAX_TIMESTAMP || seconds > Math.floor((MAX_TIMESTAMP - time) / 1000)) return;
  return time + seconds * 1000;
}

/** Inspect actual metadata and actual quoted/merged children only. A string
 * "true", encryption, chat type, send time and isRead never imply privacy or
 * an expiry. Malformed recognized fields deny retention, not fake expiry. */
export function inspectMessagePrivacy(wire: unknown): PrivacyInfo {
  let inspectionComplete = true, containsPrivate = false, rootPrivate = false;
  let expiresAt: number | undefined, nodes = 0;
  const ancestors = new Set<object>();
  const inspect = (value: unknown, depth: number, root = false) => {
    if (value == null && !root) return;
    if (!record(value) || depth > 32 || ++nodes > 4096 || ancestors.has(value)) {
      inspectionComplete = false; return;
    }
    ancestors.add(value);
    const sources: RecordValue[] = [];
    const encoded = value.attachedInfo;
    if (encoded != null && encoded !== "") {
      if (typeof encoded !== "string" || encoded.length > 65536) inspectionComplete = false;
      else {
        try { const info: unknown = JSON.parse(encoded); if (record(info)) sources.push(info); else if (info !== null) inspectionComplete = false; }
        catch { inspectionComplete = false; }
      }
    }
    if (record(value.attachedInfoElem)) sources.push(value.attachedInfoElem);
    else if (value.attachedInfoElem != null) inspectionComplete = false;
    if (value.hasReadTime != null && !integer(value.hasReadTime)) inspectionComplete = false;
    for (const info of sources) {
      if (info.isPrivateChat != null && typeof info.isPrivateChat !== "boolean") inspectionComplete = false;
      for (const key of ["burnDuration", "hasReadTime"])
        if (info[key] != null && !integer(info[key])) inspectionComplete = false;
      if (info.isPrivateChat !== true) continue;
      containsPrivate = true; if (root) rootPrivate = true;
      // Both fields may carry an authoritative read time. Keep the earliest
      // validated deadline; a later root field cannot reset an attached one.
      for (const readAt of [value.hasReadTime, info.hasReadTime]) {
        const candidate = privateExpiry(readAt, info.burnDuration);
        if (candidate !== undefined && (expiresAt === undefined || candidate < expiresAt)) expiresAt = candidate;
      }
    }
    for (const field of ["quoteElem", "atTextElem"]) {
      if (record(value[field])) inspect(value[field].quoteMessage, depth + 1);
      else if (value[field] != null) inspectionComplete = false;
    }
    if (record(value.mergeElem)) {
      const children = value.mergeElem.multiMessage;
      if (Array.isArray(children)) {
        for (const child of children) {
          if (nodes >= 4096) { inspectionComplete = false; break; }
          inspect(child, depth + 1);
        }
      } else inspectionComplete = false;
    } else if (value.mergeElem != null) inspectionComplete = false;
    ancestors.delete(value);
  };
  inspect(wire, 0, true);
  return { containsPrivate, rootPrivate, inspectionComplete, expiresAt };
}

export type PrivacyState = PrivacyInfo & { expired: boolean; allowsRetention: boolean; remainingSeconds?: number };
type Clock = { wall(): number; monotonic(): number };
type Seen = PrivacyInfo & { hidden: boolean };

/** Session-local metadata/tombstones only, never message text/URLs/credentials.
 * Known expiry cannot move later or reappear after clock rollback, remount,
 * history refill or an SDK update that omits the original private metadata. */
export class PrivateMessageLedger {
  private readonly entries = new Map<string, Seen>();
  private readonly clocks = new Map<string, { floor: number; mono: number }>();
  constructor(private readonly clock: Clock = { wall: () => Date.now(), monotonic: () => performance.now() }) {}
  now(owner: string): number {
    const wall = this.clock.wall(), mono = this.clock.monotonic();
    const old = this.clocks.get(owner);
    const floor = Math.max(wall, old ? old.floor + Math.max(0, mono - old.mono) : wall);
    this.clocks.set(owner, { floor, mono }); return floor;
  }
  private key(owner: string, _conversationID: string, id: string) { return JSON.stringify([owner, id]); }
  observe(owner: string, conversationID: string, message: unknown): PrivacyState {
    const fresh = inspectMessagePrivacy(message);
    const id = record(message) && typeof message.clientMsgID === "string" ? message.clientMsgID : "";
    const key = id ? this.key(owner, conversationID, id) : undefined;
    const old = key ? this.entries.get(key) : undefined;
    const expiresAt = old?.expiresAt === undefined ? fresh.expiresAt : fresh.expiresAt === undefined ? old.expiresAt : Math.min(old.expiresAt, fresh.expiresAt);
    const now = this.now(owner);
    const merged: Seen = {
      containsPrivate: fresh.containsPrivate || old?.containsPrivate === true,
      rootPrivate: fresh.rootPrivate || old?.rootPrivate === true,
      inspectionComplete: fresh.inspectionComplete && old?.inspectionComplete !== false,
      expiresAt, hidden: old?.hidden === true || expiresAt !== undefined && now >= expiresAt,
    };
    if (key) this.entries.set(key, merged);
    return { ...merged, expired: merged.hidden, allowsRetention: merged.inspectionComplete && !merged.containsPrivate && !merged.hidden,
      remainingSeconds: expiresAt === undefined ? undefined : Math.max(0, Math.ceil((expiresAt - now) / 1000)) };
  }
  clear() { this.entries.clear(); this.clocks.clear(); }
  forget(owner: string, conversationID: string, id: string) {
    const key = this.key(owner, conversationID, id), old = this.entries.get(key);
    this.entries.set(key, { containsPrivate: old?.containsPrivate ?? false, rootPrivate: old?.rootPrivate ?? false, inspectionComplete: old?.inspectionComplete ?? true, expiresAt: old?.expiresAt, hidden: true });
  }
}

export type HistoryTicket = { owner: string; revision: number; conversationID: string };
/** Synchronous app-cache barrier. A native SDK operation already dispatched is
 * not cancelled by invalidation; callers must settle it before native clear. */
export class MessageHistoryFence {
  private revision = 0;
  private readonly clearing = new Map<string, symbol>();
  private readonly listeners = new Set<() => void>();
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  version = () => this.revision;
  private publish() { this.revision++; for (const listener of this.listeners) listener(); }
  capture(owner: string, conversationID = ""): HistoryTicket { return { owner, conversationID, revision: this.revision }; }
  current(ticket: HistoryTicket, owner: string): boolean {
    return !!owner && ticket.owner === owner && ticket.revision === this.revision &&
      !this.clearing.has(JSON.stringify([owner, "*"])) && !this.clearing.has(JSON.stringify([owner, ticket.conversationID]));
  }
  invalidate() { this.publish(); }
  beginClear(owner: string, conversationID?: string) {
    const key = JSON.stringify([owner, conversationID ?? "*"]);
    if (!owner || [...this.clearing.keys()].some(active => JSON.parse(active)[0] === owner)) throw new Error("正在清理消息记录，请等待当前操作完成");
    const operation = Symbol(); this.clearing.set(key, operation); this.publish();
    return () => { if (this.clearing.get(key) === operation) { this.clearing.delete(key); this.publish(); } };
  }
}
