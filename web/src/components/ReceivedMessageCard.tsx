import { createContext, useContext, useEffect, useMemo, useRef, useSyncExternalStore } from "react";
import { AllowType } from "open-im-sdk-wasm";
import { sessionCoordinator } from "@/core/session/browser";
import { useTranslation } from "react-i18next";
import { captureMessageView, captureMessageRetention, subscribeMessagePrivacy } from "@/core/messagePrivacyRuntime";
import { parseReceivedCard, type MessageCardSource } from "@/core/messageCard";
import { useMessageAccess, useMessagePrivacyRevision } from "@/hooks/useMessagePrivacy";
import { useConversationStore } from "@/store";
import emitter from "@/utils/events";

/** Explicit original-message provenance across a retained merge portal. */
export const MessageCardSourceContext = createContext<MessageCardSource | undefined>(undefined);

// One source-module observer lasts with the Zustand store. It retains only an
// opaque turn, not profile data or credentials. Value-equal SDK/unread clones do
// not revoke; every observed policy/status/self-membership/role change does,
// even if React batches A->B->A into a single render. No timer is an authority.
let groupPolicyTurn: object = Object.freeze({});
let observingGroupPolicy = false;
const groupPolicyListeners = new Set<() => void>();
const policyFields = (state: ReturnType<typeof useConversationStore.getState>) => {
  const group = state.currentGroupInfo, member = state.currentMemberInGroup;
  return [group?.groupID, group?.status, group?.lookMemberInfo, group?.applyMemberFriend,
    member?.groupID, member?.userID, member?.roleLevel, member?.joinTime];
};
const subscribeGroupPolicy = (listener: () => void) => {
  groupPolicyListeners.add(listener);
  return () => { groupPolicyListeners.delete(listener); };
};
const groupPolicyVersion = () => groupPolicyTurn;
function ensureGroupPolicyObserver() {
  if (observingGroupPolicy) return;
  observingGroupPolicy = true;
  useConversationStore.subscribe((state, previous) => {
    const a = policyFields(state), b = policyFields(previous);
    if (a.every((value, index) => Object.is(value, b[index]))) return;
    groupPolicyTurn = Object.freeze({});
    for (const listener of [...groupPolicyListeners]) listener();
  });
}
const subscribeCardSource = (listener: () => void) => {
  const stopPrivacy = subscribeMessagePrivacy(listener), stopPolicy = subscribeGroupPolicy(listener);
  return () => { stopPrivacy(); stopPolicy(); };
};
export function useReceivedMessageView(message: unknown, conversationID?: string, rootMessage?: unknown) {
  const inherited = useContext(MessageCardSourceContext);
  const cid = conversationID ?? inherited?.conversationID ?? "";
  const access = useMessageAccess(cid);
  useMessagePrivacyRevision();
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const snapshot = useMemo(() => {
    const originals = rootMessage ? [rootMessage, message] : message;
    const view = captureMessageView(originals, cid, access.owner);
    const retention = captureMessageRetention(originals, cid, access.owner);
    // Read-only navigation ticket: never consult canSend or cached group roles.
    const navigation = useConversationStore.getState().captureConversationViewScope();
    return { view, retention, navigation, cid, inherited };
  }, [message, rootMessage, cid, inherited, access]);
  const live = useRef(snapshot);
  live.current = snapshot;
  return useMemo<MessageCardSource>(() => {
    const current = () => Boolean(mounted.current && live.current === snapshot && cid && access.current() && snapshot.view.current() &&
      snapshot.navigation?.conversationID === cid && snapshot.navigation.current() &&
      (!snapshot.inherited || snapshot.inherited.conversationID === cid && snapshot.inherited.groupID === snapshot.navigation?.groupID && snapshot.inherited.current()));
    return Object.freeze({ conversationID: cid, groupID: snapshot.navigation?.groupID, current,
      canShare: () => current() && snapshot.retention.current() && (!snapshot.inherited || snapshot.inherited.canShare()) });
  }, [snapshot, access, cid]);
}

export default function ReceivedMessageCard({ message, conversationID, rootMessage, compact = false }: {
  message: { cardElem?: unknown }; conversationID?: string; rootMessage?: unknown; compact?: boolean;
}) {
  const { t } = useTranslation();
  ensureGroupPolicyObserver();
  const policyTurn = useSyncExternalStore(subscribeGroupPolicy, groupPolicyVersion, groupPolicyVersion);
  const source = useReceivedMessageView(message, conversationID, rootMessage);
  const card = parseReceivedCard(message?.cardElem);
  // Only the original conversation scope supplies group identity. The card
  // payload is not a group-policy source. Unknown metadata is retried by the
  // existing group store reader; a privacy tick can present its resolved state.
  const state = useConversationStore.getState();
  const group = state.currentGroupInfo, member = state.currentMemberInGroup;
  const actor = sessionCoordinator.current?.userID;
  const groupID = source.groupID, look = group?.lookMemberInfo, apply = group?.applyMemberFriend, role = member?.roleLevel;
  const groupCurrent = useMemo(() => () => {
    if (!groupID) return true;
    const latest = useConversationStore.getState(), g = latest.currentGroupInfo, self = latest.currentMemberInGroup;
    return Boolean(policyTurn === groupPolicyVersion() && actor && g?.groupID === groupID && [0, 1, 3].includes(g.status) &&
      look === AllowType.Allowed && g.lookMemberInfo === look && g.applyMemberFriend === apply &&
      self?.groupID === groupID && self.userID === actor && self.roleLevel === role && [20, 60, 100].includes(role!));
  }, [groupID, actor, look, apply, role, policyTurn]);
  const current = useMemo(() => () => {
    const now = parseReceivedCard(message?.cardElem);
    return Boolean(card && source.current() && groupCurrent() && now?.userID === card.userID && now.label === card.label);
  }, [source, card?.userID, card?.label, message, groupCurrent]);
  const canShare = useMemo(() => () => current() && source.canShare(), [current, source]);
  if (!card || !current()) return <span role="status">名片无效或来源已失效</span>;
  return <button type="button" aria-label={`查看名片：${card.label}`} title={card.label}
    className="max-w-full rounded-lg border border-[var(--gap-text)] bg-[var(--primary-active)] text-left text-[var(--primary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--primary)]"
    style={{ minHeight: 44, minWidth: 44 }}
    onClick={event => {
      event.stopPropagation();
      if (current()) emitter.emit("OPEN_USER_CARD", { userID: card.userID, groupID, notAdd: Boolean(groupID) && apply !== AllowType.Allowed, sourceCurrent: current, sourceCanShare: canShare, sourceSubscribe: subscribeCardSource });
    }}>
    <span className={`flex min-w-0 items-center ${compact ? "gap-2 p-2" : "gap-3 p-4"}`}>
      <span aria-hidden="true" className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-[var(--primary)] text-white">{Array.from(card.label)[0]}</span>
      <span className="min-w-0 truncate">{card.label}</span>
    </span>
    {!compact && <span className="block px-4 pb-2 text-xs text-[var(--sub-text)]">{t("placeholder.personalCard")}</span>}
  </button>;
}
