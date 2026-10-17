import { CbEvents, SessionType } from "open-im-sdk-wasm";
import { GroupMessageReceiptInfo, WSEvent } from "open-im-sdk-wasm/lib/types/entity";
import { useEffect, useRef } from "react";

import { applySingleReadReceipts, applyOwnReadTimeConfirmed, OWN_READ_TIME_EVENT, captureMessageReadScope, type MessageReadScope } from "@/core/messageReadReceipt";
import { IMSDK } from "@/layout/MainContentWrap";
import { ExMessageItem, useConversationStore, useMessageStore, useUserStore } from "@/store";

export function useMessageReceipt() {
  const selfUserID = useUserStore(state => state.selfInfo.userID);
  useConversationStore(state => state.currentConversation);
  const scopeRef = useRef<MessageReadScope | undefined>(undefined);
  if (!scopeRef.current?.current()) scopeRef.current = captureMessageReadScope();
  const scope = scopeRef.current;
  useEffect(() => {
    if (!scope || !scope.current()) return;
    let active = true;
    const single = ({ data }: WSEvent<unknown>) => {
      if (active) applySingleReadReceipts(scope, data);
    };
    const own = ({ data }: WSEvent<unknown>) => {
      if (active) applyOwnReadTimeConfirmed(scope, data);
    };
    const group = ({ data }: WSEvent<GroupMessageReceiptInfo>) => {
      if (!active || scope.single || !scope.current() ||
          useConversationStore.getState().currentConversation?.conversationID !== data.conversationID) return;
      data.groupMessageReadInfo.map(receipt => {
        const hasSelfRead = receipt.readMembers?.some(member => member.userID === selfUserID);
        const oldMessage = useMessageStore.getState().historyMessageList.find(message => message.clientMsgID === receipt.clientMsgID);
        if (!oldMessage) return;
        useMessageStore.getState().updateOneMessage({ ...oldMessage,
          isRead: hasSelfRead ? true : oldMessage.isRead,
          attachedInfoElem: { ...oldMessage.attachedInfoElem, groupHasReadInfo: {
            hasReadCount: receipt.hasReadCount, unreadCount: receipt.unreadCount,
          } },
        } as ExMessageItem);
      });
    };
    IMSDK.on(CbEvents.OnRecvC2CReadReceipt, single);
    IMSDK.on(CbEvents.OnRecvGroupReadReceipt, group);
    // A versioned controlled-WASM extension; keep peer/group semantics separate.
    IMSDK.on(OWN_READ_TIME_EVENT as CbEvents, own);
    return () => { active = false; IMSDK.off(CbEvents.OnRecvC2CReadReceipt, single); IMSDK.off(CbEvents.OnRecvGroupReadReceipt, group); IMSDK.off(OWN_READ_TIME_EVENT as CbEvents, own); };
  }, [scope, selfUserID]);
}
