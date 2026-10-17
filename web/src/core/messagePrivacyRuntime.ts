import { readBarrier, readIdentity } from "./messageReadBarrier";
import { sessionCoordinator } from "./session/browser";
import { MessageHistoryFence, PrivateMessageLedger, type PrivacyState } from "./messagePrivacy";

export const messagePrivacyLedger = new PrivateMessageLedger();
export const messageHistoryFence = new MessageHistoryFence();
export function messageOwner() {
  const session = sessionCoordinator.current;
  return session ? JSON.stringify([sessionCoordinator.generation, session.issuer, session.tenant.serverID, session.userID]) : "";
}
let version = 0;
const listeners = new Set<() => void>();
let timer: ReturnType<typeof setInterval> | undefined;
const publish = () => { version++; for (const listener of listeners) listener(); };
export const privacyVersion = () => version;
export function subscribeMessagePrivacy(listener: () => void) {
  listeners.add(listener);
  if (!timer) {
    timer = setInterval(publish, 250);
    document.addEventListener("visibilitychange", publish);
  }
  return () => {
    listeners.delete(listener);
    if (!listeners.size && timer) {
      clearInterval(timer); timer = undefined;
      document.removeEventListener("visibilitychange", publish);
    }
  };
}
messageHistoryFence.subscribe(publish);
readBarrier.subscribe(publish);
let observedOwner = messageOwner();
sessionCoordinator.onChange?.(() => {
  const owner = messageOwner();
  if (owner !== observedOwner) { observedOwner = owner; messagePrivacyLedger.clear(); readBarrier.clear(); }
  messageHistoryFence.invalidate();
});

export function messagePrivacyState(message: unknown, conversationID = "", owner = messageOwner()): PrivacyState & { readTimePending?: boolean } {
  if (!owner || owner !== messageOwner()) return { containsPrivate:false,rootPrivate:false,inspectionComplete:false,expired:true,allowsRetention:false };
  const state = messagePrivacyLedger.observe(owner, conversationID, message);
  const identity = readIdentity(owner, conversationID, message);
  const row = message as { isRead?: unknown; sessionType?: unknown; sendID?: unknown; recvID?: unknown } | null;
  const actor = sessionCoordinator.current?.userID;
  // SDK isRead can establish a conservative view hold on re-entry, never a
  // fabricated deadline or permission to recover. Only a real observed ACK
  // stored below authorizes a recovery lookup. Group/ordinary policy is intact.
  const pending = !!actor && row?.sessionType === 1 && (row.sendID === actor || row.recvID === actor) &&
    state.containsPrivate && !state.expired && state.expiresAt === undefined &&
    (row.isRead === true || !!identity && readBarrier.acknowledged(identity));
  return pending ? { ...state, expired: true, allowsRetention: false, readTimePending: true } : state;
}
export function captureMessageAccess(conversationID = "", owner = messageOwner()) {
  const ticket = messageHistoryFence.capture(owner, conversationID);
  return { owner, current: () => messageHistoryFence.current(ticket, messageOwner()),
    assertCurrent() { if (!this.current()) throw new Error("会话或消息记录已更新，请重新操作"); } };
}
export function captureMessageView(messages: unknown | unknown[], conversationID = "", owner = messageOwner()) {
  const access = captureMessageAccess(conversationID, owner);
  const sources = Array.isArray(messages) ? messages : [messages];
  return { ...access, current: () => access.current() && sources.length > 0 && sources.every(source => !!source && typeof source === "object" && !messagePrivacyState(source, conversationID, access.owner).expired),
    assertCurrent() { if (!this.current()) throw new Error("消息已过期或会话已切换"); },
  };
}
export function captureMessageRetention(messages: unknown | unknown[], conversationID = "", owner = messageOwner()) {
  const access = captureMessageAccess(conversationID, owner);
  const sources = Array.isArray(messages) ? messages : [messages];
  return { ...access,
    current: () => access.current() && sources.length > 0 && sources.every(source => messagePrivacyState(source, conversationID, access.owner).allowsRetention),
    assertCurrent() { if (!this.current()) throw new Error("阅后即焚、已过期或未能核验的消息不能复制、引用、转发或保存"); },
  };
}
export function forgetMessage(conversationID: string, id: string) {
  messagePrivacyLedger.forget(messageOwner(), conversationID, id); publish();
}
export function visibleMessages<T>(messages: T[], conversationID = "") {
  return messages.filter(message => {
    const state = messagePrivacyState(message, conversationID);
    // Keep a placeholder in history; it cannot render/download/copy plaintext.
    return !state.expired || state.readTimePending;
  });
}
