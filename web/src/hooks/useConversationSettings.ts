import { App } from "antd";
import { t } from "i18next";
import { MessageReceiveOptType } from "open-im-sdk-wasm";
import { useCallback, useEffect, useRef } from "react";

import { sessionCoordinator } from "@/core/session/browser";
import type { ConversationSendScope } from "@/core/messageSendIntent";

import { IMSDK } from "@/layout/MainContentWrap";
import { useConversationStore, useUserStore } from "@/store";
import { feedbackToast } from "@/utils/common";
import { isGroupSession } from "@/utils/imCommon";

export function useConversationSettings(isActive?: () => boolean) {
  const { modal } = App.useApp();

  const currentConversation = useConversationStore(
    (state) => state.currentConversation,
  );
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const scopeRef = useRef<ConversationSendScope | undefined>(undefined);
  if (!scopeRef.current?.current()) scopeRef.current = useConversationStore.getState().captureConversationSendScope();
  const scope = scopeRef.current, actor = sessionCoordinator.current?.userID;
  const clearing = useRef(new Set<string>());

  const updateConversationPin = useCallback(
    async (isPinned: boolean) => {
      if (!currentConversation) return;

      try {
        await IMSDK.pinConversation({
          conversationID: currentConversation.conversationID,
          isPinned,
        });
      } catch (error) {
        feedbackToast({ error, msg: t("toast.pinConversationFailed") });
      }
    },
    [currentConversation?.conversationID],
  );

  const updateConversationMessageRemind = useCallback(
    async (checked: boolean, option: MessageReceiveOptType) => {
      if (!currentConversation) return;

      try {
        await IMSDK.setConversationRecvMessageOpt({
          conversationID: currentConversation.conversationID,
          opt: checked ? option : MessageReceiveOptType.Nomal,
        });
      } catch (error) {
        feedbackToast({ error, msg: t("toast.setConversationRecvMessageOptFailed") });
      }
    },
    [currentConversation?.conversationID],
  );

  const updateConversationPrivateState = useCallback(async () => {
    if (!currentConversation) return;

    try {
      await IMSDK.setConversationPrivateChat({
        conversationID: currentConversation.conversationID,
        isPrivate: !currentConversation.isPrivateChat,
      });
    } catch (error) {
      feedbackToast({ error, msg: t("toast.updateConversationPrivateStateFailed") });
    }
  }, [currentConversation?.conversationID, currentConversation?.isPrivateChat]);

  const updateBurnDuration = useCallback(
    async (seconds: number) => {
      if (!currentConversation) return;

      try {
        await IMSDK.setConversationBurnDuration({
          conversationID: currentConversation.conversationID,
          burnDuration: seconds,
        });
      } catch (error) {
        feedbackToast({ error, msg: t("toast.updateConversationPrivateStateFailed") });
      }
    },
    [currentConversation?.conversationID],
  );

  const updateConversationMsgDestructState = useCallback(async () => {
    if (!currentConversation) return;

    try {
      await IMSDK.setConversationIsMsgDestruct({
        conversationID: currentConversation.conversationID,
        isMsgDestruct: !currentConversation.isMsgDestruct,
      });
    } catch (error) {
      feedbackToast({ error });
    }
  }, [currentConversation?.conversationID, currentConversation?.isMsgDestruct]);

  const updateDestructDuration = useCallback(
    async (seconds: number) => {
      if (!currentConversation) return;

      try {
        await IMSDK.setConversationMsgDestructTime({
          conversationID: currentConversation.conversationID,
          msgDestructTime: seconds,
        });
      } catch (error) {
        feedbackToast({ error });
      }
    },
    [currentConversation?.conversationID],
  );

  const clearConversationMessages = useCallback(() => {
    const conversationID = currentConversation?.conversationID;
    const current = () => Boolean(mounted.current && (!isActive || isActive()) && scope?.current() && actor &&
      useUserStore.getState().selfInfo.userID === actor && scope.target.conversationID === conversationID);
    if (!conversationID || !current()) return;
    modal.confirm({
      title: t("toast.clearChatHistory"),
      content: t("toast.confirmClearChatHistory"),
      onOk: async () => {
        if (!current() || clearing.current.has(conversationID)) return false;
        clearing.current.add(conversationID);
        try {
          if (!current()) return false;
          await IMSDK.clearConversationAndDeleteAllMsg(conversationID);
          // The real SDK history wrapper already invalidates at begin/end.
          // A third global invalidation here could erase a newly entered view.
          // The clear invalidates its own history ticket, so only owner/nav
          // scope (never a pre-clear history ticket) gates successful UI settle.
          return current();
        } catch (error) {
          if (current()) feedbackToast({ error, msg: t("toast.clearConversationMessagesFailed") });
          return false;
        } finally { clearing.current.delete(conversationID); }
      },
    });
  }, [scope, actor, isActive, currentConversation?.conversationID]);

  return {
    currentConversation,
    updateBurnDuration,
    updateDestructDuration,
    updateConversationPin,
    updateConversationMessageRemind,
    updateConversationPrivateState,
    updateConversationMsgDestructState,
    clearConversationMessages,
  };
}
