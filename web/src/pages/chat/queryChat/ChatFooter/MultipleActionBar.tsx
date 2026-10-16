import { useMessageAccess } from "@/hooks/useMessagePrivacy";
import { messageOwner } from "@/core/messagePrivacyRuntime";
import { captureIndividualForward, INDIVIDUAL_FORWARD_LIMIT } from "@/core/individualForward";
import { captureMessageAccess, captureMessageRetention } from "@/core/messagePrivacyRuntime";
import { Spin } from "antd";
import { t } from "i18next";
import { MessageItem } from "open-im-sdk-wasm/lib/types/entity";
import { useState, useRef } from "react";

import cancel from "@/assets/images/chatFooter/cancel.png";
import forward from "@/assets/images/chatFooter/forward.png";
import remove from "@/assets/images/chatFooter/remove.png";
import { IMSDK } from "@/layout/MainContentWrap";
import { useConversationStore, useMessageStore, useUserStore } from "@/store";
import { feedbackToast } from "@/utils/common";
import emitter from "@/utils/events";
import { formatMessageByType, isGroupSession } from "@/utils/imCommon";

const multipleActionList = [
  {
    title: t("placeholder.mergeForward"),
    icon: forward,
  },
  {
    title: t("placeholder.delete"),
    icon: remove,
  },
  {
    title: t("placeholder.close"),
    icon: cancel,
  },
];

const MultipleActionBar = () => {
  const sourceOwner = useRef(messageOwner()).current;
  const sourceAccess = useMessageAccess("");
  const [loading, setLoading] = useState(false);
  const currentConversation = useConversationStore(
    (state) => state.currentConversation,
  );
  const historyMessageList = useMessageStore((state) => state.historyMessageList);
  const updateCheckMode = useMessageStore((state) => state.updateCheckMode);
  const deleteOneMessage = useMessageStore((state) => state.deleteOneMessage);
  const sourceView = useConversationStore.getState().captureConversationViewScope();
  const checkedCount = historyMessageList.filter(message => message.checked).length;

  const forwardIndividually = () => {
    const sourceCID = currentConversation?.conversationID;
    if (!sourceCID || sourceOwner !== messageOwner() || !sourceView?.current() ||
        sourceView.conversationID !== sourceCID || !sourceAccess.current()) return;
    const checked = getCheckedMessageList();
    if (!checked.length || checked.length > INDIVIDUAL_FORWARD_LIMIT) {
      feedbackToast({ error: new Error(t("placeholder.individualForwardLimit")) }); return;
    }
    if (!captureMessageRetention(checked, sourceCID, sourceOwner).current()) {
      feedbackToast({ error: new Error(t("placeholder.individualForwardInvalidSource")) }); return;
    }
    const selection = captureIndividualForward(sourceCID, sourceOwner, [...checked].reverse().map(message => message.clientMsgID));
    if (!selection) { feedbackToast({ error: new Error(t("placeholder.individualForwardInvalidSource")) }); return; }
    emitter.emit("OPEN_CHOOSE_MODAL", { type: "FORWARD_MESSAGE", sourceConversationID: sourceCID,
      sourceOwner, sourceCurrent: selection.current, individualSelection: selection });
    updateCheckMode(false);
  };

  const actionClick = async (idx: number) => {
    if (!sourceAccess.current()) return;
    switch (idx) {
      case 0:
        if (!captureMessageRetention(getCheckedMessageList(), currentConversation?.conversationID, sourceOwner).current()) { feedbackToast({ error: new Error("阅后即焚、已过期或未能核验的消息不能合并转发") }); return; }
        emitter.emit("OPEN_CHOOSE_MODAL", {
          type: "FORWARD_MESSAGE",
          sourceConversationID: currentConversation?.conversationID, sourceOwner,
          extraData: getMergeMessageOptions(),
        });
        break;
      case 1:
        await batchDeleteMessage();
        break;
      default:
        break;
    }
    updateCheckMode(false);
  };

  const batchDeleteMessage = async () => {
    const access = captureMessageAccess(currentConversation?.conversationID, sourceOwner);
    if (!access.current()) return;
    setLoading(true);
    const messageList = getCheckedMessageList();

    try {
      await Promise.all(
        messageList.map(async (message) => {
          try {
            access.assertCurrent();
            await IMSDK.deleteMessage({
              clientMsgID: message.clientMsgID,
              conversationID: currentConversation!.conversationID,
            });
          } catch (error) {
            access.assertCurrent();
            await IMSDK.deleteMessageFromLocalStorage({
              clientMsgID: message.clientMsgID,
              conversationID: currentConversation!.conversationID,
            });
            throw new Error("deleteMessage error");
          }
        }),
      );
      if (!access.current()) return;
      messageList.forEach((message) => deleteOneMessage(message.clientMsgID));
    } catch (error) {
      if (access.current()) feedbackToast({ error: t("toast.messagesDeleteFailed") });
    }
    setLoading(false);
  };

  const getMergeMessageOptions = () => {
    const messageList = [...getCheckedMessageList()].reverse();
    const summaryList = messageList
      .slice(0, 4)
      .map(
        (message) =>
          `${message.senderNickname}：${formatMessageByType(message as MessageItem)}`,
      );
    return {
      messageList,
      summaryList,
      title: t("placeholder.whosMessageHistory", {
        who: isGroupSession(currentConversation?.conversationType)
          ? t("placeholder.group")
          : t("placeholder.and", {
              someone: useUserStore.getState().selfInfo.nickname,
              otherone: currentConversation?.showName,
            }),
      }),
    };
  };

  const getCheckedMessageList = () =>
    historyMessageList.filter((message) => message.checked);

  return (
    <Spin spinning={loading}>
      <div className="flex flex-wrap gap-3 bg-[var(--chat-bubble)] px-4 py-4">
        <button type="button" disabled={loading} onClick={forwardIndividually}
          aria-label={t("placeholder.individualForward")}
          style={{ minWidth: 44, minHeight: 44 }}
          className="flex min-h-16 flex-col items-center justify-center rounded-[var(--luobo-radius-control)] bg-[var(--luobo-primary-soft)] px-3 py-2 text-[var(--luobo-primary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-600">
          <span>{t("placeholder.individualForward")}</span>
          <span className="text-xs">{t("placeholder.individualForwardSelection")}: {checkedCount}/{INDIVIDUAL_FORWARD_LIMIT}</span>
        </button>
        {multipleActionList.map((action, idx) => (
          <div
            className="flex h-16 w-16 cursor-pointer flex-col items-center justify-center rounded-md bg-white"
            key={action.title}
            onClick={() => actionClick(idx)}
          >
            <img width={24} src={action.icon} className="mb-1.5 mt-2" alt="" />
            <span className="text-xs text-[var(--sub-text)]">{action.title}</span>
          </div>
        ))}
      </div>
    </Spin>
  );
};

export default MultipleActionBar;
