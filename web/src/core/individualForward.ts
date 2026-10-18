import { canForwardLegacyTagMessages } from "./legacyTagMessage";
import type { MessageItem, SearchMessageResult } from "open-im-sdk-wasm/lib/types/entity";
import type { SendReceipt } from "./messageBatch";
import type { SendIntent, SendTarget } from "./messageSendIntent";
import { captureMessageAccess, captureMessageRetention, messageOwner, messagePrivacyState } from "./messagePrivacyRuntime";

export const INDIVIDUAL_FORWARD_LIMIT = 20;
const WAIT_MS = 10000;
const forwardTypes = new Set([101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 114, 115]);
export type IndividualReceiptState = "processing" | "acknowledged" | "unconfirmed" | "rejected" | "not-attempted";
export type IndividualReason = "ready" | "processing" | "stopped" | "invalid-source" | "permission" | "prepare" | "unknown" | "acknowledged";
export type IndividualReceipt = Readonly<{
  target: SendTarget;
  sourceID: string;
  kind: "message" | "additional";
  state: IndividualReceiptState;
  reason: IndividualReason;
}>;
export interface IndividualForwardSelection {
  readonly sourceCID: string;
  readonly owner: string;
  readonly sourceIDs: readonly string[];
  current(): boolean;
  started(): boolean;
  receipts(): readonly IndividualReceipt[];
}
type Operation = { started: boolean; stopped: boolean; rows: IndividualReceipt[] };
const operations = new WeakMap<IndividualForwardSelection, Operation>();
// The issued selection is the one-shot operation. An unknown result locks this
// panel/operation, not a later explicit user reselection with a new intent.
const snapshots = (rows: IndividualReceipt[]) => Object.freeze(rows.map(row => Object.freeze({ ...row, target: Object.freeze({ ...row.target }) })));
const validID = (v: unknown): v is string => typeof v === "string" && v.length > 0 && v.length <= 1024;

/** Called at an admitted toolbar entry, before opening a recipient picker.
 * No message body, nickname, URL or private snapshot enters selection state. */
export function captureIndividualForward(sourceCID: string, owner: string, ids: readonly string[]): IndividualForwardSelection | undefined {
  if (!validID(sourceCID) || !owner || owner !== messageOwner() || ids.length < 1 ||
      ids.length > INDIVIDUAL_FORWARD_LIMIT || ids.some(id => !validID(id)) || new Set(ids).size !== ids.length) return;
  const access = captureMessageAccess(sourceCID, owner);
  if (!access.current()) return;
  const operation: Operation = { started: false, stopped: false, rows: [] };
  const selection: IndividualForwardSelection = Object.freeze({ sourceCID, owner, sourceIDs: Object.freeze([...ids]),
    current: access.current, started: () => operation.started, receipts: () => snapshots(operation.rows) });
  operations.set(selection, operation);
  return selection;
}
export const isIndividualForwardSelection = (value: unknown): value is IndividualForwardSelection =>
  !!value && typeof value === "object" && operations.has(value as IndividualForwardSelection);
export const individualForwardStopped = (selection: IndividualForwardSelection) => operations.get(selection)?.stopped === true;
/** Stop future admission, not the original history/view lease or pending ACK.
 * This never claims that an already-entered native send has been cancelled. */
export function stopIndividualForward(selection: IndividualForwardSelection): boolean {
  const operation = operations.get(selection);
  if (!operation?.started || operation.stopped || !operation.rows.some(row => row.state === "processing" ||
      row.state === "not-attempted" && row.reason === "ready")) return false;
  operation.stopped = true;
  operation.rows = operation.rows.map(row => row.state === "not-attempted" ? { ...row, reason: "stopped" } : row);
  return true;
}

async function bounded<T>(pending: Promise<T>): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([pending, new Promise<never>((_, reject) => { timer = setTimeout(() => reject(new Error("individual-forward-timeout")), WAIT_MS); })]);
  } finally { if (timer !== undefined) clearTimeout(timer); }
}

function exactSource(result: SearchMessageResult, cid: string, id: string): MessageItem {
  const groups = result?.findResultItems;
  if (!Array.isArray(groups) || groups.length !== 1 || groups[0].conversationID !== cid ||
      groups[0].messageCount !== 1 || !Array.isArray(groups[0].messageList) || groups[0].messageList.length !== 1) throw new Error("individual-forward-source");
  const source = groups[0].messageList[0];
  if (source?.clientMsgID !== id || source.status !== 2 || !forwardTypes.has(source.contentType) || !canForwardLegacyTagMessages(source)) throw new Error("individual-forward-source");
  return source;
}

export type IndividualForwardDependencies = {
  isCurrent(): boolean;
  resolve(intent: SendIntent): Promise<SendIntent | undefined>;
  canDeliver(intent: SendIntent): Promise<boolean>;
  find(sourceCID: string, id: string): Promise<SearchMessageResult>;
  create(source: MessageItem): Promise<MessageItem>;
  createAdditional(text: string): Promise<MessageItem>;
  send(message: MessageItem, intent: SendIntent, retention: ReturnType<typeof captureMessageRetention>): Promise<SendReceipt>;
  onProgress?(receipts: readonly IndividualReceipt[]): void;
};

/** One claim, serial tuples, no retries. A delivered receipt is never rewritten
 * by later view/permission changes; a discarded SDK reply is unconfirmed. */
export async function runIndividualForward(selection: IndividualForwardSelection, intents: readonly SendIntent[], additional: string,
  deps: IndividualForwardDependencies): Promise<readonly IndividualReceipt[]> {
  const operation = operations.get(selection);
  if (!operation) return [];
  if (operation.started) return selection.receipts();
  operation.started = true;
  const seen = new Set<string>();
  const targets = intents.filter(intent => {
    const key = JSON.stringify([intent.target.kind, intent.target.id]);
    if (seen.has(key)) return false;
    seen.add(key); return true;
  });
  operation.rows = targets.flatMap(intent => [
    ...selection.sourceIDs.map(sourceID => ({ target: intent.target, sourceID, kind: "message" as const, state: "not-attempted" as const, reason: "ready" as const })),
    ...(additional ? [{ target: intent.target, sourceID: "", kind: "additional" as const, state: "not-attempted" as const, reason: "ready" as const }] : []),
  ]);
  const publish = () => { const rows = selection.receipts(); try { deps.onProgress?.(rows); } catch { /* Presentation cannot change delivery facts. */ } return rows; };
  const current = () => selection.current() && selection.owner === messageOwner() && deps.isCurrent();
  const admitted = () => current() && !operation.stopped;
  publish();
  for (let i = 0; i < operation.rows.length; ++i) {
    let row = operation.rows[i];
    if (!admitted()) break;
    const original = targets.find(intent => intent.target.kind === row.target.kind && intent.target.id === row.target.id)!;
    let intent: SendIntent | undefined;
    let retention: ReturnType<typeof captureMessageRetention>;
    let outgoing: MessageItem;
    try {
      intent = await bounded(deps.resolve(original));
      if (!admitted()) break;
      if (!intent?.current()) { operation.rows[i] = { ...row, state: "rejected", reason: "permission" }; break; }
      if (intent.target.kind !== original.target.kind || intent.target.id !== original.target.id || !validID(intent.target.conversationID)) throw new Error("individual-forward-target");
      row = operation.rows[i] = { ...row, target: intent.target };
      const allowed = await bounded(deps.canDeliver(intent));
      if (!admitted()) break;
      if (!allowed) { operation.rows[i] = { ...row, state: "rejected", reason: "permission" }; break; }
      if (!intent.current()) break;
      if (row.kind === "message") {
        // Exactly one source is read and retained only in this tuple's frame.
        // Explicit source-CID access is needed: findMessageList takes an array.
        const result = await bounded(deps.find(selection.sourceCID, row.sourceID));
        if (!admitted() || !intent.current()) break;
        const source = exactSource(result, selection.sourceCID, row.sourceID);
        const originalRetention = captureMessageRetention(source, selection.sourceCID, selection.owner);
        retention = { ...originalRetention,
          current: () => originalRetention.current() && canForwardLegacyTagMessages(source),
          assertCurrent() { originalRetention.assertCurrent(); if (!canForwardLegacyTagMessages(source)) throw new Error("individual-forward-source"); } };
        if (!retention.current()) { operation.rows[i] = { ...row, state: "rejected", reason: "invalid-source" }; break; }
        outgoing = await bounded(deps.create(source));
      } else {
        // Additional text is allowed only after this recipient's sources ACK.
        // Re-read all sources to honor privacy changes since earlier ACKs.
        for (const id of selection.sourceIDs) {
          const result = await bounded(deps.find(selection.sourceCID, id));
          if (!admitted() || !intent.current()) throw new Error("individual-forward-stopped");
          const source = exactSource(result, selection.sourceCID, id);
          if (!captureMessageRetention(source, selection.sourceCID, selection.owner).current()) throw new Error("individual-forward-source");
        }
        const access = captureMessageAccess(selection.sourceCID, selection.owner);
        // The re-reads above populate the existing metadata ledger. Keep only
        // IDs for the final asynchronous sender gate, not source-body closures.
        retention = { ...access, current: () => access.current() && selection.sourceIDs.every(id =>
          messagePrivacyState({ clientMsgID: id }, selection.sourceCID, selection.owner).allowsRetention),
          assertCurrent() { if (!this.current()) throw new Error("individual-forward-source"); } };
        outgoing = await bounded(deps.createAdditional(additional));
      }
      if (!admitted() || !intent.current() || !retention.current()) break;
      if (!validID(outgoing?.clientMsgID)) throw new Error("individual-forward-created");
    } catch {
      if (admitted()) operation.rows[i] = { ...row, state: "rejected", reason: "prepare" };
      break;
    }
    operation.rows[i] = { ...row, state: "processing", reason: "processing" };
    publish();
    let receipt: SendReceipt;
    try { receipt = await bounded(deps.send(outgoing, intent, retention)); }
    catch { receipt = { state: "unconfirmed" }; }
    if (receipt.state === "sent") {
      operation.rows[i] = { ...row, state: "acknowledged", reason: "acknowledged" };
      publish();
      continue;
    }
    if (receipt.state === "stale") {
      // Sender proves it never entered native; this operation still stays used.
      operation.rows[i] = operation.stopped ? { ...row, state: "not-attempted", reason: "stopped" }
        : { ...row, state: "rejected", reason: "permission" };
    } else operation.rows[i] = { ...row, state: "unconfirmed", reason: "unknown" };
    break;
  }
  operation.rows = operation.rows.map(row => row.state === "not-attempted" ? { ...row, reason: "stopped" } : row);
  return publish();
}
