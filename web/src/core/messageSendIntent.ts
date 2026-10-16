import { sessionCoordinator } from "./session/browser";
import { captureMessageAccess, messageOwner } from "./messagePrivacyRuntime";

export type SendTarget = Readonly<{ kind: "user" | "group"; id: string; conversationID?: string }>;
export interface ConversationSendScope {
  readonly target: SendTarget & { conversationID: string };
  current(): boolean;
  canSend(): boolean;
}
export interface SendIntent {
  readonly target: SendTarget;
  readonly actor: string;
  readonly ownerKey: string;
  readonly mode: "current" | "selected";
  current(): boolean;
  ownerCurrent(): boolean;
  projectionCurrent(): boolean;
  assertCurrent(): void;
}
export interface SendOrigin {
  readonly sourceCID: string;
  readonly ownerKey: string;
  current(): boolean;
  currentIntent(extra?: () => boolean): SendIntent | undefined;
  selectedIntent(target: SendTarget, extra?: () => boolean): SendIntent | undefined;
}
const issued = new WeakSet<object>();
const id = (value: unknown): value is string => typeof value === "string" && value.length > 0;
export const sameSendTarget = (a: SendTarget, b: SendTarget) => a.kind === b.kind && a.id === b.id;
export function conversationSendTarget(value: unknown): (SendTarget & { conversationID: string }) | undefined {
  if (!value || typeof value !== "object") return;
  const v = value as Record<string, unknown>;
  if (!id(v.conversationID)) return;
  // Installed SDK SessionType: Single=1, Group/WorkingGroup=3. Notification=4
  // is not a writable chat. Never compare bare IDs across those namespaces.
  if (v.conversationType === 1 && id(v.userID) && !v.groupID)
    return Object.freeze({ kind: "user", id: v.userID, conversationID: v.conversationID });
  if (v.conversationType === 3 && id(v.groupID) && !v.userID)
    return Object.freeze({ kind: "group", id: v.groupID, conversationID: v.conversationID });
}
export function selectedSendTarget(value: { userID?: string; groupID?: string; conversationID?: string; conversationType?: number }): SendTarget | undefined {
  if (value.conversationType !== undefined) {
    const kind = value.conversationType === 1 && id(value.userID) && !value.groupID ? "user"
      : value.conversationType === 3 && id(value.groupID) && !value.userID ? "group" : undefined;
    if (!kind || value.conversationID !== undefined && !id(value.conversationID)) return;
    return Object.freeze({ kind, id: (kind === "user" ? value.userID : value.groupID)!,
      ...(id(value.conversationID) ? { conversationID: value.conversationID } : {}) });
  }
  // The existing picker displays a member with both IDs as a USER, and the
  // SDK likewise gave recvID precedence. Freeze that explicit kind once here.
  const target = id(value.userID) ? { kind: "user" as const, id: value.userID }
    : id(value.groupID) ? { kind: "group" as const, id: value.groupID } : undefined;
  return target ? Object.freeze({ ...target, ...(id(value.conversationID) ? { conversationID: value.conversationID } : {}) }) : undefined;
}
/** Local/SDK policy only; server send ACL is the final authority. Values match
 * the installed role/status contract and server verify.go UnixMilli rules. */
export function knownGroupSendAllowed(group: unknown, member: unknown, groupID: string, actor: string, now = Date.now()): boolean {
  if (!group || !member || typeof group !== "object" || typeof member !== "object") return false;
  const g = group as Record<string, unknown>, m = member as Record<string, unknown>;
  if (g.groupID !== groupID || m.groupID !== groupID || m.userID !== actor ||
      ![0, 1, 2, 3].includes(g.status as number) || ![20, 60, 100].includes(m.roleLevel as number)) return false;
  if (g.status === 2) return false;
  if (m.roleLevel === 100) return true; // Server owner exemption; no fake unmute.
  const mute = m.muteEndTime;
  if (typeof mute !== "number" || !Number.isSafeInteger(mute) || mute < 0 || mute >= now) return false;
  return g.status !== 3 || m.roleLevel === 60;
}
/** Capture BEFORE modal/async work. This never performs SDK/storage/network I/O.
 * Selected recipients retain their own identity, independently of active chat. */
export function captureSendOrigin(scope?: ConversationSendScope, originalSourceCID?: string): SendOrigin {
  const owner = sessionCoordinator.current, generation = sessionCoordinator.generation;
  const actor = owner?.userID, issuer = owner?.issuer, tenant = owner?.tenant.serverID;
  const ownerKey = messageOwner(), sourceCID = originalSourceCID ?? scope?.target.conversationID ?? "";
  const history = captureMessageAccess(sourceCID, ownerKey);
  const ownerCurrent = () => Boolean(owner && actor && owner === sessionCoordinator.current &&
    generation === sessionCoordinator.generation && owner.userID === actor &&
    owner.issuer === issuer && owner.tenant.serverID === tenant && ownerKey === messageOwner());
  const current = () => ownerCurrent() && history.current();
  const make = (input: SendTarget, mode: "current" | "selected", extra = () => true): SendIntent | undefined => {
    if (!current() || !id(input?.id) || !["user", "group"].includes(input.kind) ||
        input.conversationID !== undefined && !id(input.conversationID) ||
        mode === "current" && (!scope?.current() || !scope.canSend())) return;
    const target = Object.freeze({ ...input });
    const targetHistory = captureMessageAccess(target.conversationID ?? "", ownerKey);
    const admitted = () => current() && targetHistory.current() && extra() &&
      (mode !== "current" || Boolean(scope?.current() && scope.canSend()));
    if (!admitted()) return;
    const intent: SendIntent = Object.freeze({ target, actor: actor!, ownerKey, mode,
      ownerCurrent, current: admitted,
      // Post-dispatch UI settlement is independent of send permission/extra.
      // Owner, original history and monotonic view scope must still match.
      projectionCurrent: () => current() && targetHistory.current() && Boolean(scope?.current() && sameSendTarget(scope.target, target) && (!target.conversationID || target.conversationID === scope.target.conversationID)),
      assertCurrent() { if (!admitted()) throw new Error("聊天已切换或发送条件已变化，请重新明确发送"); },
    });
    issued.add(intent); return intent;
  };
  return Object.freeze({ sourceCID, ownerKey, current,
    currentIntent: (extra?: () => boolean) => scope ? make(scope.target, "current", extra) : undefined,
    selectedIntent: (target: SendTarget, extra?: () => boolean) => make(target, "selected", extra),
  });
}
export const isSendIntent = (value: unknown): value is SendIntent =>
  Boolean(value && typeof value === "object" && issued.has(value));
