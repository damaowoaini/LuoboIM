import { readBarrier, readIdentity } from "./messageReadBarrier";
import { privateExpiry, inspectMessagePrivacy } from "./messagePrivacy";
import { captureMessageAccess, messageOwner, messagePrivacyState } from "./messagePrivacyRuntime";
import { sessionCoordinator } from "./session/browser";
import { useConversationStore, useMessageStore } from "@/store";
import { useUserStore } from "@/store/user";
import type { ExMessageItem } from "@/store/message";

type RecordValue = Record<string, unknown>;
const record = (value: unknown): value is RecordValue => !!value && typeof value === "object" && !Array.isArray(value);
export type MessageReadScope = Readonly<{ conversationID: string; actor: string; peer: string;
  single: boolean; owner: string; ownerCurrent(): boolean; current(): boolean }>;
/** A read ticket has no canSend/role permission. Its view epoch cannot revive
 * after A -> B -> A, owner replacement or a message-history clear. */
export function captureMessageReadScope(): MessageReadScope | undefined {
  const conversation = useConversationStore.getState().currentConversation;
  const view = useConversationStore.getState().captureConversationViewScope();
  const owner = sessionCoordinator.current, generation = sessionCoordinator.generation;
  const actor = owner?.userID, issuer = owner?.issuer, tenant = owner?.tenant.serverID;
  const conversationID = conversation?.conversationID, peer = conversation?.userID ?? "";
  if (!owner || !actor || !conversationID || !view || view.conversationID !== conversationID) return;
  const key = messageOwner(), access = captureMessageAccess(conversationID, key);
  const single = conversation.conversationType === 1;
  const ownerCurrent = () => owner === sessionCoordinator.current && generation === sessionCoordinator.generation &&
      owner.userID === actor && owner.issuer === issuer && owner.tenant.serverID === tenant &&
      key === messageOwner() && useUserStore.getState().selfInfo?.userID === actor;
  return Object.freeze({ conversationID, actor, peer, single, owner: key, ownerCurrent, current: () => {
    const now = useConversationStore.getState().currentConversation;
    return ownerCurrent() && view.current() && access.current() && now?.conversationID === conversationID &&
      now.conversationType === conversation.conversationType && (now.userID ?? "") === peer;
  } });
}
const readMillis = (value: unknown): number | undefined => {
  const deadline = privateExpiry(value, 1);
  return deadline === undefined ? undefined : deadline - 1000;
};
function readTimes(message: ExMessageItem): number[] {
  const root = message as unknown as RecordValue, values: unknown[] = [root.hasReadTime];
  if (record(root.attachedInfoElem)) values.push(root.attachedInfoElem.hasReadTime);
  if (typeof root.attachedInfo === "string" && root.attachedInfo && root.attachedInfo.length <= 65536) {
    try { const parsed: unknown = JSON.parse(root.attachedInfo); if (record(parsed)) values.push(parsed.hasReadTime); }
    catch { /* A malformed policy stays malformed; it is not repaired by a receipt. */ }
  }
  return values.map(readMillis).filter((value): value is number => value !== undefined);
}
function belongs(message: ExMessageItem, scope: MessageReadScope): boolean {
  const row = message as unknown as RecordValue;
  return typeof message.clientMsgID === "string" && !!message.clientMsgID && message.sessionType === 1 &&
    !message.groupID && (row.conversationID == null || row.conversationID === scope.conversationID) &&
    ((message.sendID === scope.actor && message.recvID === scope.peer) ||
      (message.sendID === scope.peer && message.recvID === scope.actor));
}
function setReadFlag(source: ExMessageItem, scope: MessageReadScope): boolean {
  if (!scope.current()) return false;
  const current = useMessageStore.getState().historyMessageList.find(row => row.clientMsgID === source.clientMsgID);
  if (!current || !belongs(current, scope) || current.sendID !== source.sendID || current.recvID !== source.recvID || current.seq !== source.seq) return false;
  useMessageStore.getState().updateOneMessage({ clientMsgID: source.clientMsgID, isRead: true } as ExMessageItem);
  return true;
}
function updateReadTime(source: ExMessageItem, scope: MessageReadScope, time: unknown): boolean {
  const incoming = readMillis(time);
  if (incoming === undefined || !scope.current() || !belongs(source, scope) ||
      !inspectMessagePrivacy(source).inspectionComplete) return false;
  const current = useMessageStore.getState().historyMessageList.find(row => row.clientMsgID === source.clientMsgID);
  if (!current || !belongs(current, scope) || current.sendID !== source.sendID || current.recvID !== source.recvID ||
      current.seq !== source.seq || !inspectMessagePrivacy(current).inspectionComplete) return false;
  // Preserve original policy/body. Only validated read time is projected; an
  // existing earlier time and the privacy ledger's earlier expiry win.
  const hasReadTime = Math.min(incoming, ...readTimes(current));
  useMessageStore.getState().updateOneMessage({ ...current, isRead: true, hasReadTime } as ExMessageItem);
  return true;
}
export function applySingleReadReceipts(scope: MessageReadScope, receipts: unknown): number {
  if (!scope.current() || !scope.single || !scope.peer || scope.peer === scope.actor || !Array.isArray(receipts)) return 0;
  let applied = 0;
  for (const receipt of receipts) {
    if (!record(receipt) || receipt.userID !== scope.peer || receipt.sessionType !== 1 || receipt.groupID ||
        !Array.isArray(receipt.msgIDList)) continue;
    const ids = new Set(receipt.msgIDList.filter((id): id is string => typeof id === "string" && !!id));
    for (const message of useMessageStore.getState().historyMessageList) {
      if (ids.has(message.clientMsgID) && message.sendID === scope.actor && belongs(message, scope)) {
        // A valid peer ACK still marks read when the SDK omits a usable time;
        // it does not invent an expiry or repair malformed privacy metadata.
        const identity = readIdentity(scope.owner, scope.conversationID, message);
        if (identity) readBarrier.acknowledge([identity]);
        if (updateReadTime(message, scope, receipt.readTime)) applied++;
        else setReadFlag(message, scope);
      }
    }
  }
  return applied;
}
export type ReadSyncResult = { state: "applied" | "acknowledged" | "failed" | "unconfirmed" | "stale";
  acknowledged: boolean; projected: number };
type ReadSDK = {
  markConversationMessageAsRead(conversationID: string): Promise<unknown>;
  findMessageList(params: { conversationID: string; clientMsgIDList: string[] }[]): Promise<{ data: unknown }>;
};
/** Only messages already rendered in this exact single chat are looked up.
 * Recovery never invokes mark-read. All replies validate the original exact
 * IDs/seq/peer, original owner and mounted navigation ticket before projection. */
async function hydrateReadTimes(scope: MessageReadScope, sdk: ReadSDK,
  originals: Map<string, ExMessageItem>): Promise<ReadSyncResult> {
  if (!scope.current()) return { state: "stale", acknowledged: true, projected: 0 };
  const identities = [...originals.values()].map(message => readIdentity(scope.owner, scope.conversationID, message));
  if (!identities.length || identities.some(value => !value || !readBarrier.acknowledged(value)))
    return { state: "unconfirmed", acknowledged: true, projected: 0 };
  const release = readBarrier.claim(identities.filter((value): value is NonNullable<typeof value> => !!value));
  if (!release) return { state: "unconfirmed", acknowledged: true, projected: 0 };
  try {
    const { data } = await sdk.findMessageList([{ conversationID: scope.conversationID, clientMsgIDList: [...originals.keys()] }]);
    if (!scope.current()) return { state: "stale", acknowledged: true, projected: 0 };
    if (!record(data) || !Number.isSafeInteger(data.totalCount) || (data.totalCount as number) < 0) throw new Error("invalid exact lookup");
    const items = data.findResultItems ?? data.searchResultItems ?? (data.totalCount === 0 ? [] : undefined);
    if (!Array.isArray(items)) throw new Error("missing exact lookup buckets");
    const found = new Map<string, ExMessageItem>();
    for (const bucket of items) {
      if (!record(bucket) || bucket.conversationID !== scope.conversationID || bucket.conversationType !== 1 ||
          !Array.isArray(bucket.messageList) || bucket.messageCount !== bucket.messageList.length) throw new Error("foreign exact lookup bucket");
      for (const raw of bucket.messageList) {
        if (!record(raw) || typeof raw.clientMsgID !== "string" || !originals.has(raw.clientMsgID) || found.has(raw.clientMsgID))
          throw new Error("foreign exact lookup ID");
        const message = raw as unknown as ExMessageItem, original = originals.get(raw.clientMsgID)!;
        if (!belongs(message, scope) || message.sendID !== original.sendID || message.recvID !== original.recvID || message.seq !== original.seq)
          throw new Error("foreign exact lookup identity");
        found.set(raw.clientMsgID, message);
      }
    }
    if (data.totalCount !== found.size) throw new Error("inconsistent exact lookup count");
    // Validate the entire reply before any projection. A missing timestamp is
    // acknowledged-but-pending evidence, never Date.now()/isRead/sendTime.
    let projected = 0;
    for (const [id, message] of found) {
      if (!inspectMessagePrivacy(message).inspectionComplete) continue;
      // The exact SDK snapshot may strengthen a known private policy/deadline,
      // but cannot relax the ledger or replace the original displayed body.
      messagePrivacyState(message, scope.conversationID);
      const times = readTimes(message);
      if (times.length && updateReadTime(originals.get(id)!, scope, Math.min(...times))) projected++;
    }
    return { state: projected ? "applied" : "acknowledged", acknowledged: true, projected };
  } catch {
    return { state: scope.current() ? "unconfirmed" : "stale", acknowledged: true, projected: 0 };
  } finally { release(); }
}

export function pendingReadTimes(scope: MessageReadScope | undefined) {
  if (!scope?.single || !scope.current()) return { count: 0, canRecover: false, busy: false };
  const rows = (useMessageStore.getState().historyMessageList ?? []).filter(message =>
    belongs(message, scope) && messagePrivacyState(message, scope.conversationID).readTimePending);
  const identities = rows.map(message => readIdentity(scope.owner, scope.conversationID, message));
  return { count: rows.length,
    canRecover: identities.some(value => !!value && readBarrier.acknowledged(value)),
    busy: identities.some(value => !!value && readBarrier.busy(value)) };
}

export async function recoverReadTimes(scope: MessageReadScope, sdk: ReadSDK): Promise<ReadSyncResult> {
  if (!scope.current()) return { state: "stale", acknowledged: false, projected: 0 };
  const originals = new Map((useMessageStore.getState().historyMessageList ?? []).filter(message => {
    const identity = readIdentity(scope.owner, scope.conversationID, message);
    return belongs(message, scope) && messagePrivacyState(message, scope.conversationID).readTimePending &&
      !!identity && readBarrier.acknowledged(identity);
  }).map(message => [message.clientMsgID, message]));
  if (!originals.size) return { state: "unconfirmed", acknowledged: false, projected: 0 };
  return hydrateReadTimes(scope, sdk, originals);
}

export async function markReadAndHydrate(scope: MessageReadScope, sdk: ReadSDK,
  onAcknowledged: () => void): Promise<ReadSyncResult> {
  if (!scope.current()) return { state: "stale", acknowledged: false, projected: 0 };
  const originals = new Map((useMessageStore.getState().historyMessageList ?? [])
    .filter(message => scope.single && message.sendID === scope.peer && message.sendID !== scope.actor &&
      belongs(message, scope) && (message.isRead !== true ||
        inspectMessagePrivacy(message).containsPrivate && !readTimes(message).length))
    .map(message => [message.clientMsgID, message]));
  let acknowledged = false;
  try {
    await sdk.markConversationMessageAsRead(scope.conversationID);
    acknowledged = true;
    // Even a late known ACK may retain identity-only evidence for this same
    // owner. It cannot project data or borrow a new navigation ticket.
    if (scope.ownerCurrent()) readBarrier.acknowledge([...originals.values()]
      .map(message => readIdentity(scope.owner, scope.conversationID, message))
      .filter((value): value is NonNullable<typeof value> => !!value));
    if (!scope.current()) return { state: "stale", acknowledged, projected: 0 };
    onAcknowledged();
    for (const message of originals.values()) setReadFlag(message, scope);
    if (!scope.single || !originals.size) return { state: "acknowledged", acknowledged, projected: 0 };
    return hydrateReadTimes(scope, sdk, originals);
  } catch {
    return { state: !scope.current() ? "stale" : acknowledged ? "unconfirmed" : "failed", acknowledged, projected: 0 };
  }
}

/** Emitted only by the controlled WASM own-receipt producer after the exact
 * SQLite/IndexedDB commit. Subscribe through IMSDK, never a DOM/custom event.
 * The old opaque WASM has no such producer and cannot establish this contract. */
export const OWN_READ_TIME_EVENT = "OnOwnC2CReadTimeConfirmed";
export function applyOwnReadTimeConfirmed(scope: MessageReadScope, event: unknown): number {
  if (!scope.current() || !scope.single || !scope.peer || scope.peer === scope.actor || !record(event) ||
      event.schemaVersion !== 1 || event.ownerID !== scope.actor || event.peerID !== scope.peer ||
      event.conversationID !== scope.conversationID || event.sessionType !== 1 ||
      typeof event.receiptID !== "string" || !event.receiptID || !Array.isArray(event.committedRows) ||
      !event.committedRows.length) return 0;
  const loaded = new Map(useMessageStore.getState().historyMessageList.map(message => [message.clientMsgID, message]));
  const ids = new Set<string>(), seqs = new Set<number>();
  const originals: { message: ExMessageItem; readTime: number }[] = [];
  // Check the entire batch before projecting anything. Unknown loaded rows are
  // not looked up or acknowledged; their persisted SDK snapshot can load later.
  for (const raw of event.committedRows) {
    if (!record(raw) || typeof raw.clientMsgID !== "string" || !raw.clientMsgID ||
        !Number.isSafeInteger(raw.seq) || (raw.seq as number) <= 0 || raw.sendID !== scope.peer ||
        raw.recvID !== scope.actor || !Number.isSafeInteger(raw.readTime) ||
        readMillis(raw.readTime) !== raw.readTime || ids.has(raw.clientMsgID) || seqs.has(raw.seq as number)) return 0;
    ids.add(raw.clientMsgID); seqs.add(raw.seq as number);
    const message = loaded.get(raw.clientMsgID);
    if (!message) continue;
    if (!belongs(message, scope) || message.sendID !== raw.sendID || message.recvID !== raw.recvID || message.seq !== raw.seq) return 0;
    originals.push({ message, readTime: raw.readTime as number });
  }
  if (!scope.current()) return 0;
  const identities = originals.map(({ message }) => readIdentity(scope.owner, scope.conversationID, message));
  if (identities.some(value => !value)) return 0;
  readBarrier.acknowledge(identities.filter((value): value is NonNullable<typeof value> => !!value));
  let projected = 0;
  for (const { message, readTime } of originals) if (updateReadTime(message, scope, readTime)) projected++;
  return projected;
}
