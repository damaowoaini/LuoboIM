import { decodeSendFailure } from "@/core/sendFailure";
import { captureMessageAccess, captureMessageRetention } from "@/core/messagePrivacyRuntime";
import { captureSendOrigin, conversationSendTarget, isSendIntent, knownGroupSendAllowed, sameSendTarget, type SendIntent } from "@/core/messageSendIntent";
import { sessionCoordinator } from "@/core/session/browser";
import type { SendReceipt } from "@/core/messageBatch";
import { MessageStatus } from "open-im-sdk-wasm";
import { useCallback } from "react";
import { IMSDK } from "@/layout/MainContentWrap";
import { ExMessageItem, useConversationStore, useMessageStore } from "@/store";
import emitter from "@/utils/events";

export type SendMessageParams = {
  intent: SendIntent;
  message: ExMessageItem;
  needPush?: boolean;
  isResend?: boolean;
  sourceRetention?: ReturnType<typeof captureMessageRetention>;
};

/** These are fresh SDK CACHE reads, not authoritative HTTP authorization. The
 * server's send ACL remains final. Check identity as well as role/status. */
export async function checkKnownSendPermission(intent: SendIntent): Promise<boolean> {
  if (!isSendIntent(intent) || !intent.current()) return false;
  if (intent.target.kind === "user") return true;
  const groupID = intent.target.id;
  try {
  const { data: groups } = await IMSDK.getSpecifiedGroupsInfo([groupID]);
  if (!intent.current()) return false;
  const { data: members } = await IMSDK.getSpecifiedGroupMembersInfo({ groupID, userIDList: [intent.actor] });
  const state = useConversationStore.getState();
  const active = conversationSendTarget(state.currentConversation);
  if (active && sameSendTarget(active, intent.target) &&
      !knownGroupSendAllowed(state.currentGroupInfo, state.currentMemberInGroup, groupID, intent.actor)) return false;
  return intent.current() && Array.isArray(groups) && groups.length === 1 && Array.isArray(members) && members.length === 1 &&
    knownGroupSendAllowed(groups[0], members[0], groupID, intent.actor);
  } catch { return false; }
}

export function useSendMessage() {
  const generation = sessionCoordinator.generation;
  // Capture at render, before a caller can retain an old modal/async callback.
  // Object refreshes of the SAME conversation do not invalidate its opaque epoch.
  useConversationStore(state => state.currentConversation);
  const origin = captureSendOrigin(useConversationStore.getState().captureConversationSendScope?.());
  const pushNewMessage = useMessageStore(state => state.pushNewMessage);
  const tryAddPreviewImg = useMessageStore(state => state.tryAddPreviewImg);
  const updateOneMessage = useMessageStore(state => state.updateOneMessage);
  const deleteAndPushOneMessage = useMessageStore(state => state.deleteAndPushOneMessage);

  const sendMessage = useCallback(async ({ intent, message, needPush, isResend, sourceRetention }: SendMessageParams): Promise<SendReceipt> => {
    const current = () => generation === sessionCoordinator.generation && isSendIntent(intent) && intent.current() &&
      (!sourceRetention || sourceRetention.current());
    if (!current()) return { state: "stale" };
    let conversationID = intent.target.conversationID;
    try {
      if (!conversationID || intent.mode === "selected") {
        const { data } = await IMSDK.getOneConversation({ sourceID: intent.target.id, sessionType: intent.target.kind === "user" ? 1 : 3 });
        const resolved = conversationSendTarget(data);
        if (!current() || !resolved || !sameSendTarget(resolved, intent.target) ||
            conversationID && resolved.conversationID !== conversationID) return { state: "stale" };
        conversationID = resolved.conversationID;
      }
      // Origin captured the global history revision BEFORE the first async; a
      // new target ticket cannot revive it after clear/navigation invalidation.
      const access = captureMessageAccess(conversationID, intent.ownerKey);
      if (!current() || !access.current() || !(await checkKnownSendPermission(intent))) return { state: "stale" };
      const projection = () => {
        const active = conversationSendTarget(useConversationStore.getState().currentConversation);
        return generation === sessionCoordinator.generation && intent.ownerCurrent() &&
          intent.projectionCurrent() && access.current() && (!sourceRetention || sourceRetention.current()) && !!active &&
          active.conversationID === conversationID && sameSendTarget(active, intent.target);
      };
      if (!current() || !access.current()) return { state: "stale" };
      if (needPush !== false && projection()) { pushNewMessage(message); emitter.emit("CHAT_LIST_SCROLL_TO_BOTTOM", true); }
      // conversationID is local history-fence metadata; the installed wrapper
      // forwards only recvID/groupID/message to the existing wire API.
      const options = { recvID: intent.target.kind === "user" ? intent.target.id : "",
        groupID: intent.target.kind === "group" ? intent.target.id : "", conversationID, message };
      intent.assertCurrent(); access.assertCurrent(); sourceRetention?.assertCurrent();
      let successMessage: ExMessageItem;
      try {
        // Everything after entering this call may already have reached native.
        const { data } = await IMSDK.sendMessage(options);
        if (!data || data.clientMsgID !== message.clientMsgID || data.status !== 2)
          throw new Error("发送回执未能核验");
        successMessage = data as ExMessageItem;
      } catch (error) {
        const sendFailure = decodeSendFailure(error);
        try { if (projection()) updateOneMessage({ ...message, status: MessageStatus.Failed, sendFailure }); } catch { /* UI is not delivery evidence. */ }
        // Includes a reply discarded by the session/history wrapper. It cannot
        // be confirmed, cancelled, or safely replayed by this hook.
        return { state: "unconfirmed" };
      }
      // An observed and validated ACK stays sent even if UI projection fails.
      try {
        if (projection()) {
          if (isResend) deleteAndPushOneMessage(successMessage);
          else { updateOneMessage(successMessage); tryAddPreviewImg([successMessage]); }
        }
      } catch { /* Do not turn an acknowledged send into a retry invitation. */ }
      return { state: "sent" };
    } catch {
      // No sendMessage dispatch occurred (local preparation/read failure).
      return { state: "stale" };
    }
  }, [generation]);

  return { sendMessage, updateOneMessage, beginCurrentSend: origin.currentIntent,
    beginSelectedSend: origin.selectedIntent };
}
