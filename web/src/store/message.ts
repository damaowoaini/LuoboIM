import { captureMessageAccess, forgetMessage, messageHistoryFence, messagePrivacyState, visibleMessages } from "@/core/messagePrivacyRuntime";
import { t } from "i18next";
import { MessageType, SessionType } from "open-im-sdk-wasm";
import { MessageItem, SearchMessageResult } from "open-im-sdk-wasm/lib/types/entity";
import { create } from "zustand";

import { IMSDK } from "@/layout/MainContentWrap";
import { feedbackToast } from "@/utils/common";

import { useContactStore } from "./contact";
import { useConversationStore } from "./conversation";
import { DownloadData, MessageStore, UpdateMessaggeBaseInfoParams } from "./type";
import { useUserStore } from "./user";

const applyFriendRemarkToMessages = (messages: ExMessageItem[]): ExMessageItem[] => {
  const friendList = useContactStore.getState().friendList;
  if (friendList.length === 0) return messages;
  const friendMap = new Map(friendList.map((f) => [f.userID, f]));
  return messages.map((msg) => {
    if (msg.groupID && msg.sendID) {
      const friend = friendMap.get(msg.sendID);
      if (friend?.remark) {
        return { ...msg, senderNickname: friend.remark };
      }
    }
    return msg;
  });
};

const GET_HISTORY_MESSAGE_COUNT = 20;

export interface ExType {
  checked?: boolean;
  isAppend?: boolean;
  gapTime?: boolean;
  jump?: boolean;
  errCode?: number;
}

export type ExMessageItem = MessageItem & ExType;

interface IAdvancedMessageResponse {
  lastMinSeq: number;
  isEnd: boolean;
  messageList: ExMessageItem[];
}

export const useMessageStore = create<MessageStore>()((set, get) => ({
  historyMessageList: [],
  previewImgList: [],
  jumpLoading: false,
  jumpClientMsgID: undefined,
  lastMinSeq: 0,
  hasMore: true,
  laterHasMore: false,
  isCheckMode: false,
  downloadMap: {},
  getHistoryMessageListByReq: async (loadMore = false) => {
    const conversationID =
      useConversationStore.getState().currentConversation?.conversationID;
    if (!conversationID) return;
    const access = captureMessageAccess(conversationID);
    if (!access.current()) return;
    try {
      const prevList = [...get().historyMessageList];
      const { data } = await IMSDK.getAdvancedHistoryMessageList({
        userID: "",
        groupID: "",
        count: GET_HISTORY_MESSAGE_COUNT,
        lastMinSeq: loadMore ? get().lastMinSeq : 0,
        startClientMsgID: loadMore ? prevList[prevList.length - 1]?.clientMsgID : "",
        conversationID,
      });

      if (
        !access.current() || conversationID !==
        useConversationStore.getState().currentConversation?.conversationID
      )
        return;

      (data.messageList as ExMessageItem[]).map((message, idx) => {
        if (!idx) return;
        const prevTime = data.messageList[idx - 1]?.sendTime;
        if (message.sessionType === SessionType.Notification) {
          (data.messageList[idx - 1] as ExMessageItem).gapTime =
            message.sendTime - prevTime > 300000;
        } else {
          message.gapTime = message.sendTime - prevTime > 300000;
        }
      });
      const nextList = applyFriendRemarkToMessages(
        [...(loadMore ? prevList : []), ...data.messageList.reverse()],
      );

      set(() => ({
        lastMinSeq: data.lastMinSeq,
        hasMore: data.messageList.length !== 0,
        historyMessageList: visibleMessages(nextList, conversationID),
      }));
    } catch (error) {
      if (!access.current()) return;
      feedbackToast({ error, msg: t("toast.getHistoryMessageFailed") });
      set(() => ({
        lastMinSeq: 0,
        hasMore: false,
        historyMessageList: [],
      }));
    }
  },
  getHistoryMessageListReverseByReq: async () => {
    const conversationID =
      useConversationStore.getState().currentConversation?.conversationID;
    if (!conversationID) return;
    const access = captureMessageAccess(conversationID);
    if (!access.current()) return;
    try {
      const prevList = [...get().historyMessageList];
      const lastMinSeq = prevList.find((message) => Boolean(message.seq))?.seq ?? 0;
      const { data } = await IMSDK.getAdvancedHistoryMessageListReverse({
        userID: "",
        groupID: "",
        count: GET_HISTORY_MESSAGE_COUNT,
        lastMinSeq,
        startClientMsgID: prevList[0]?.clientMsgID ?? "",
        conversationID,
      });

      if (
        !access.current() || conversationID !==
        useConversationStore.getState().currentConversation?.conversationID
      )
        return;

      (data.messageList as ExMessageItem[]).map((message, idx) => {
        if (!idx) return;
        const prevTime = data.messageList[idx - 1]?.sendTime;
        if (message.sessionType === SessionType.Notification) {
          (data.messageList[idx - 1] as ExMessageItem).gapTime =
            message.sendTime - prevTime > 300000;
        } else {
          message.gapTime = message.sendTime - prevTime > 300000;
        }
      });
      const nextList = applyFriendRemarkToMessages(
        [...data.messageList.reverse(), ...prevList],
      );

      set(() => ({
        laterHasMore: data.messageList.length !== 0,
        historyMessageList: visibleMessages(nextList, conversationID),
      }));
    } catch (error) {
      if (!access.current()) return;
      feedbackToast({ error, msg: t("toast.getHistoryMessageFailed") });
      set(() => ({
        laterHasMore: false,
        historyMessageList: [],
      }));
    }
  },
  getTwoWayHistoryMessage: async ({ message, conversationID }) => {
    const access = captureMessageAccess(conversationID);
    if (!access.current() || messagePrivacyState(message, conversationID).expired) return;
    set(() => ({ jumpLoading: true }));
    const loadOptions = {
      userID: "",
      groupID: "",
      count: GET_HISTORY_MESSAGE_COUNT,
      lastMinSeq: message.seq,
      startClientMsgID: message.clientMsgID,
      conversationID,
    };
    const [{ data: earlierData }, { data: laterData }] = await Promise.all([
      IMSDK.getAdvancedHistoryMessageList(loadOptions),
      IMSDK.getAdvancedHistoryMessageListReverse(loadOptions),
    ]);
    if (!access.current() || conversationID !== useConversationStore.getState().currentConversation?.conversationID) return;
    const totalList = [
      ...earlierData.messageList,
      message,
      ...laterData.messageList,
    ].reverse();
    totalList.map((message, idx) => {
      if (!idx) return;
      const prevTime = totalList[idx - 1]?.sendTime;
      (totalList[idx - 1] as ExMessageItem).gapTime =
        message.sendTime - prevTime > 300000;
    });

    set(() => ({
      lastMinSeq: earlierData.lastMinSeq,
      jumpLoading: false,
      jumpClientMsgID: message.clientMsgID,
      hasMore: earlierData.messageList.length !== 0,
      laterHasMore: laterData.messageList.length !== 0,
      historyMessageList: visibleMessages(applyFriendRemarkToMessages(totalList), conversationID),
    }));
  },
  clearAppendState: () => {
    set((state) => ({
      historyMessageList: state.historyMessageList.map((message) => ({
        ...message,
        isAppend: false,
      })),
    }));
  },
  updateJumpClientMsgID: (jumpClientMsgID?: string) => set(() => ({ jumpClientMsgID })),
  pushNewMessage: (message: ExMessageItem) => {
    if (messagePrivacyState(message, useConversationStore.getState().currentConversation?.conversationID).expired) return;
    const [patched] = applyFriendRemarkToMessages([message]);
    set((state) => ({
      historyMessageList: [patched, ...state.historyMessageList],
    }));
  },
  updateOneMessage: (message: ExMessageItem, fromMediaDownload = false) => {
    // update download state
    if (fromMediaDownload) {
      const tmpPreviewList = [...get().previewImgList];
      const previewIdx = tmpPreviewList.findIndex(
        (item) => item.clientMsgID === message.clientMsgID,
      );
      if (previewIdx > -1) {
        const field = tmpPreviewList[previewIdx].videoUrl ? "videoUrl" : "url";
        tmpPreviewList[previewIdx][field] = `file://${message.localEx}`;
        set(() => ({ previewImgList: tmpPreviewList }));
      }
    }

    const tmpList = [...get().historyMessageList];
    const idx = tmpList.findIndex((msg) => msg.clientMsgID === message.clientMsgID);
    if (idx < 0) {
      return;
    }
    tmpList[idx] = { ...tmpList[idx], ...message };
    messagePrivacyState(tmpList[idx], useConversationStore.getState().currentConversation?.conversationID);

    set(() => ({ historyMessageList: tmpList }));
  },
  deleteOneMessage: (clientMsgID: string) => {
    forgetMessage(useConversationStore.getState().currentConversation?.conversationID ?? "", clientMsgID);
    const tmpList = get().historyMessageList;
    const idx = tmpList.findIndex((msg) => msg.clientMsgID === clientMsgID);
    if (idx < 0) {
      return;
    }
    tmpList.splice(idx, 1);
    set(() => ({ historyMessageList: [...tmpList] }));
  },
  deleteAndPushOneMessage: (message: ExMessageItem) => {
    const tmpList = get().historyMessageList;
    const idx = tmpList.findIndex((msg) => msg.clientMsgID === message.clientMsgID);
    if (idx < 0) {
      return;
    }
    tmpList.splice(idx, 1);
    set(() => ({ historyMessageList: [message, ...tmpList] }));
  },
  updateMessageNicknameAndFaceUrl: ({
    sendID,
    senderFaceUrl,
    senderNickname,
  }: UpdateMessaggeBaseInfoParams) => {
    const tmpList = [...get().historyMessageList].map((message) => {
      if (message.sendID === sendID) {
        message.senderFaceUrl = senderFaceUrl;
        message.senderNickname = senderNickname;
      }
      return message;
    });
    set(() => ({ historyMessageList: tmpList }));
  },
  clearHistoryMessage: () => { messageHistoryFence.invalidate(); },
  updateCheckMode: (isCheckMode: boolean) => {
    if (!isCheckMode) {
      const tmpList = [...get().historyMessageList].map((message) => {
        message.checked = false;
        return message;
      });
      set(() => ({ historyMessageList: tmpList }));
    }
    set(() => ({ isCheckMode }));
  },
  getConversationPreviewImgList: async () => {
    const conversationID =
      useConversationStore.getState().currentConversation?.conversationID;

    if (!conversationID) return;
    const access = captureMessageAccess(conversationID);
    if (!access.current()) return;
    const {
      data: { searchResultItems },
    } = await IMSDK.searchLocalMessages({
      conversationID,
      keywordList: [],
      keywordListMatchType: 0,
      senderUserIDList: [],
      messageTypeList: [MessageType.PictureMessage, MessageType.VideoMessage],
      searchTimePosition: 0,
      searchTimePeriod: 0,
      pageIndex: 1,
      count: 200,
    });
    if (!access.current() || !searchResultItems?.[0].messageCount) return;
    const newPreviewImgList = searchResultItems[0].messageList.filter(item => !messagePrivacyState(item, conversationID).expired).map((item) => ({
      sourceMessage: item,
      url: getImageMessageSourceUrl(item) ?? "",
      clientMsgID: item.clientMsgID,
      videoUrl: getVideoMessageSourceUrl(item),
      thumbUrl: item.pictureElem?.snapshotPicture.url ?? "",
    }));
    set(() => ({ previewImgList: [...newPreviewImgList] }));
  },
  tryAddPreviewImg: (mesageList: ExMessageItem[]) => {
    const previews = mesageList
      .filter((message) => MediaMessageTypes.includes(message.contentType) && !messagePrivacyState(message, useConversationStore.getState().currentConversation?.conversationID).expired)
      .map((message) => ({
        sourceMessage: message,
        url: getImageMessageSourceUrl(message) ?? "",
        clientMsgID: message.clientMsgID,
        videoUrl: getVideoMessageSourceUrl(message),
        thumbUrl: message.pictureElem?.snapshotPicture.url ?? "",
      }));
    if (previews.length === 0) return;
    set((state) => ({
      previewImgList: [...previews, ...state.previewImgList],
    }));
  },
  addDownloadTask: (url: string, data: DownloadData) => {
    set((state) => ({
      downloadMap: { ...state.downloadMap, [url]: { ...data } },
    }));
  },
  updateDownloadTask: (url: string, data: DownloadData) => {
    const tmpMap = { ...get().downloadMap };
    tmpMap[url] = {
      ...tmpMap[url],
      ...data,
    };
    set(() => ({ downloadMap: tmpMap }));
  },
  removeDownloadTask: (url: string) => {
    const tmpMap = { ...get().downloadMap };
    if (!tmpMap[url]) return;
    delete tmpMap[url];
    set(() => ({ downloadMap: tmpMap }));
  },
}));

// Clear view caches synchronously at both ends of a native clear and on session rotation.
messageHistoryFence.subscribe(() => {
  useMessageStore.setState({ historyMessageList: [], previewImgList: [], jumpLoading: false, jumpClientMsgID: undefined, hasMore: false, laterHasMore: false, isCheckMode: false });
  useConversationStore.setState({ quoteMessage: undefined, revokeMap: {} });
});

const MediaMessageTypes = [MessageType.PictureMessage, MessageType.VideoMessage];

export const getImageMessageSourceUrl = (message: ExMessageItem) => {
  if (message.contentType === MessageType.VideoMessage) {
    const snapshotPath = message.videoElem.snapshotPath;
    if (snapshotPath && window.electronAPI?.fileExists(snapshotPath)) {
      return `file://${snapshotPath}`;
    }
    const snapshotUrl = message.videoElem.snapshotUrl;
    const cachePath = useUserStore.getState().imageCache[snapshotUrl];
    if (cachePath && window.electronAPI?.fileExists(cachePath)) {
      return `file://${cachePath}`;
    }
    return snapshotUrl;
  }

  if (message.localEx && window.electronAPI?.fileExists(message.localEx)) {
    return `file://${message.localEx}`;
  }
  if (window.electronAPI?.fileExists(message.pictureElem.sourcePath)) {
    return `file://${message.pictureElem.sourcePath}`;
  }
  return message.pictureElem.sourcePicture.url;
};

export const getVideoMessageSourceUrl = (message: ExMessageItem) => {
  if (message.contentType !== MessageType.VideoMessage) return undefined;
  if (message.localEx && window.electronAPI?.fileExists(message.localEx)) {
    return `file://${message.localEx}`;
  }
  if (window.electronAPI?.fileExists(message.videoElem.videoPath)) {
    return `file://${message.videoElem.videoPath}`;
  }
  return message.videoElem.videoUrl;
};
