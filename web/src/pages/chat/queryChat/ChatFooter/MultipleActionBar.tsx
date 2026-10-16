import { useMessageAccess } from "@/hooks/useMessagePrivacy";
import { messageOwner } from "@/core/messagePrivacyRuntime";
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
      <div className="flex bg-[var(--chat-bubble)] px-16 py-4">
        {multipleActionList.map((action, idx) => (
          <div
            className="mr-8 flex h-16 w-16 cursor-pointer flex-col items-center justify-center rounded-md bg-white last:mr-0"
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
