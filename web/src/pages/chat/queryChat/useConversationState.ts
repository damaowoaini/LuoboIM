import { useThrottleFn } from "ahooks";
import { GroupAtType, GroupStatus } from "open-im-sdk-wasm";
import { useEffect, useRef } from "react";

import { sessionCoordinator } from "@/core/session/browser";
import { useCurrentMemberRole } from "@/hooks/useCurrentMemberRole";
import { IMSDK } from "@/layout/MainContentWrap";
import { useConversationStore } from "@/store";

export default function useConversationState() {
  // Effect cleanup and throttled trailing calls can outlive Router teardown.
  // Never acquire a replacement account's SDK lease from an old mounted hook.
  const source = useRef({
    owner: sessionCoordinator.current,
    generation: sessionCoordinator.generation,
    actor: sessionCoordinator.current?.userID,
    issuer: sessionCoordinator.current?.issuer,
    tenant: sessionCoordinator.current?.tenant.serverID,
  }).current;
  const currentConversation = useConversationStore(
    (state) => state.currentConversation,
  );
  const currentGroupInfo = useConversationStore(
    (state) => state.currentGroupInfo,
  );
  const isMutedGroup = currentGroupInfo?.status === GroupStatus.Muted;

  const { isJoinGroup, isNomal, isAdmin, isOwner, currentIsMuted, currentMemberInGroup } =
    useCurrentMemberRole();
  const isCurrent = () => {
    const owner = sessionCoordinator.current;
    return Boolean(source.owner && source.actor && owner && owner === source.owner &&
      sessionCoordinator.generation === source.generation && owner.userID === source.actor &&
      owner.issuer === source.issuer && owner.tenant.serverID === source.tenant &&
      currentConversation?.conversationID &&
      useConversationStore.getState().currentConversation?.conversationID === currentConversation.conversationID);
  };

  useEffect(() => {
    checkConversationState();
    return () => {
      checkConversationState();
    };
  }, [
    currentConversation?.conversationID,
    currentConversation?.groupAtType,
    currentConversation?.unreadCount,
  ]);

  const { run: checkConversationState } = useThrottleFn(
    () => {
      if (!currentConversation || !isCurrent()) return;

      if (currentConversation.unreadCount > 0) {
        const cleared = { ...currentConversation, unreadCount: 0 };
        useConversationStore.getState().updateCurrentConversation(cleared);
        if (!isCurrent()) return;
        useConversationStore.getState().updateConversationList([cleared], "filter");
        if (!isCurrent()) return;
        void IMSDK.markConversationMessageAsRead(currentConversation.conversationID).catch(() => undefined);
      }
      if (
        currentConversation.groupAtType !== GroupAtType.AtNormal &&
        currentConversation.groupAtType !== GroupAtType.AtGroupNotice
      ) {
        if (!isCurrent()) return;
        void IMSDK.resetConversationGroupAtType(currentConversation.conversationID).catch(() => undefined);
      }
    },
    { wait: 2000 },
  );

  const getIsCanSendMessage = () => {
    if (!isCurrent()) return false;
    if (currentConversation?.userID) {
      return true;
    }

    const groupID = currentConversation?.groupID;
    const current = useConversationStore.getState();
    if (current.currentGroupInfo !== currentGroupInfo ||
        current.currentMemberInGroup !== currentMemberInGroup) return false;
    if (!groupID || !isJoinGroup || !currentGroupInfo || !currentMemberInGroup ||
        currentGroupInfo.groupID !== groupID || currentMemberInGroup.groupID !== groupID ||
        !sessionCoordinator.current?.userID ||
        currentMemberInGroup.userID !== sessionCoordinator.current.userID ||
        !(isNomal || isAdmin || isOwner) ||
        ![GroupStatus.Nomal, GroupStatus.Baned, GroupStatus.Dismissed, GroupStatus.Muted]
          .includes(currentGroupInfo.status) ||
        currentGroupInfo.status === GroupStatus.Dismissed) {
      return false;
    }

    if (isMutedGroup && isNomal) {
      return false;
    }

    return !currentIsMuted;
  };

  return { getIsCanSendMessage, currentIsMuted, isMutedGroup, currentConversation };
}
