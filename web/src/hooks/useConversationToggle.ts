import { SessionType } from "open-im-sdk-wasm";
import { ConversationItem } from "open-im-sdk-wasm/lib/types/entity";
import { useCallback } from "react";
import { useNavigate } from "react-router-dom";

import { sessionCoordinator } from "@/core/session/browser";
import { IMSDK } from "@/layout/MainContentWrap";
import { useConversationStore } from "@/store";
import { feedbackToast } from "@/utils/common";

// Source IDs live in different namespaces. The current wrapper defines Single=1,
// Group/WorkingGroup=3 and Notification=4; a matching bare ID is insufficient.
function matchesTarget(item: ConversationItem | undefined, sourceID: string, type: SessionType) {
  if (!item || typeof item.conversationID !== "string" || !item.conversationID || item.conversationType !== type) return false;
  if (type === SessionType.Single || type === SessionType.Notification)
    return item.userID === sourceID;
  if (type === SessionType.WorkingGroup) return item.groupID === sourceID;
  return false;
}

export function useConversationToggle() {
  const navigate = useNavigate();
  const updateCurrentConversation = useConversationStore(
    (state) => state.updateCurrentConversation,
  );

  const getConversation = async ({
    sourceID,
    sessionType,
  }: {
    sourceID: string;
    sessionType: SessionType;
  }, current: () => boolean): Promise<ConversationItem | undefined> => {
    if (!current() || !sourceID || ![SessionType.Single, SessionType.WorkingGroup, SessionType.Notification].includes(sessionType)) return;
    let conversation = useConversationStore
      .getState()
      .conversationList.find(
        (item) => matchesTarget(item, sourceID, sessionType),
      );
    if (!conversation) {
      try {
        conversation = (
          await IMSDK.getOneConversation({
            sourceID,
            sessionType,
          })
        ).data;
        if (!current()) return;
        if (!matchesTarget(conversation, sourceID, sessionType)) {
          feedbackToast({ error: new Error("Conversation target unavailable") });
          return;
        }
      } catch (error) {
        if (current()) feedbackToast({ error });
      }
    }
    return conversation;
  };

  const toSpecifiedConversation = useCallback(
    async (
      data: {
        sourceID: string;
        sessionType: SessionType;
      },
      isJump?: boolean,
      callerCurrent?: () => boolean,
    ) => {
      // This shared hook is called without a caller lease by search, group and
      // message entries too. Even an in-memory cache resolves across an await.
      const owner = sessionCoordinator.current;
      const generation = sessionCoordinator.generation;
      const current = () => Boolean(owner && sessionCoordinator.current === owner &&
        sessionCoordinator.generation === generation && (callerCurrent === undefined || callerCurrent()));
      if (!current()) return false;
      const conversation = await getConversation(data, current);
      if (!current() || !conversation) return false;
      if (useConversationStore.getState().currentConversation?.conversationID === conversation.conversationID) return true;
      updateCurrentConversation({ ...conversation }, isJump);
      navigate(`/chat/${conversation.conversationID}`);
      return true;
    },
    [],
  );

  return {
    toSpecifiedConversation,
  };
}
