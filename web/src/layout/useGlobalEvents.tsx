import { sessionCoordinator } from "@/core/session/browser";
import { createApplicationEventHandlers } from "@/core/applicationEventHandlers";
import { subscribeSessionEvent } from "@/core/session/desktop";
import { StaleSessionError } from "@/core/session/session";
import { useLatest, useThrottleFn } from "ahooks";
import { t } from "i18next";
import { CbEvents } from "open-im-sdk-wasm";
import {
  LogLevel,
  MessageReceiveOptType,
  MessageType,
  SessionType,
} from "open-im-sdk-wasm";
import {
  BlackUserItem,
  ConversationItem,
  FriendApplicationItem,
  FriendUserItem,
  GroupApplicationItem,
  GroupItem,
  GroupMemberItem,
  RevokedInfo,
  SelfUserInfo,
  WSEvent,
  WsResponse,
} from "open-im-sdk-wasm/lib/types/entity";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { generateAvatar } from "@/utils/common";
import { message as antdMessage } from "@/AntdGlobalComp";
import { BusinessAllowType } from "@/api/login";
import messageRing from "@/assets/audio/newMsg.mp3";
import { getApiUrl, getWsUrl } from "@/config";
import { SystemMessageTypes } from "@/constants";
import {
  ExMessageItem,
  useConversationStore,
  useMessageStore,
  useUserStore,
} from "@/store";
import { useContactStore } from "@/store/contact";
import { isExplicitLogoutPending } from "@/store/user";
import { feedbackToast } from "@/utils/common";
import emitter from "@/utils/events";
import { createNotification, initStore, isGroupSession } from "@/utils/imCommon";
import { clearIMProfile, getIMToken, getIMUserID } from "@/utils/storage";
import { IMSDK } from "./MainContentWrap";
export function useGlobalEvent() {
  const navigate = useNavigate();
  const [connectState, setConnectState] = useState({
    isSyncing: false,
    isLogining: false,
    isConnecting: true,
    isConnected: false,
  });
  const latestConnectState = useLatest(connectState);
  // user
  const updateSelfInfo = useUserStore((state) => state.updateSelfInfo);
  const getWorkMomentsUnreadCount = useUserStore(
    (state) => state.getWorkMomentsUnreadCount,
  );
  const userLogout = useUserStore((state) => state.userLogout);
  const addImageCache = useUserStore((state) => state.addImageCache);
  // conversation
  const updateConversationList = useConversationStore(
    (state) => state.updateConversationList,
  );
  const updateCurrentConversation = useConversationStore(
    (state) => state.updateCurrentConversation,
  );
  const updateUnReadCount = useConversationStore((state) => state.updateUnReadCount);
  const updateCurrentGroupInfo = useConversationStore(
    (state) => state.updateCurrentGroupInfo,
  );
  const getCurrentGroupInfoByReq = useConversationStore(
    (state) => state.getCurrentGroupInfoByReq,
  );
  const getCurrentMemberInGroupByReq = useConversationStore(
    (state) => state.getCurrentMemberInGroupByReq,
  );
  const tryUpdateCurrentMemberInGroup = useConversationStore(
    (state) => state.tryUpdateCurrentMemberInGroup,
  );
  // message
  const pushNewMessage = useMessageStore((state) => state.pushNewMessage);
  const tryAddPreviewImg = useMessageStore((state) => state.tryAddPreviewImg);
  const updateOneMessage = useMessageStore((state) => state.updateOneMessage);
  const updateMessageNicknameAndFaceUrl = useMessageStore(
    (state) => state.updateMessageNicknameAndFaceUrl,
  );
  const updateDownloadTask = useMessageStore((state) => state.updateDownloadTask);
  const removeDownloadTask = useMessageStore((state) => state.removeDownloadTask);
  // contact
  const updateFriend = useContactStore((state) => state.updateFriend);
  const pushNewFriend = useContactStore((state) => state.pushNewFriend);
  const updateBlack = useContactStore((state) => state.updateBlack);
  const pushNewBlack = useContactStore((state) => state.pushNewBlack);
  const updateGroup = useContactStore((state) => state.updateGroup);
  const pushNewGroup = useContactStore((state) => state.pushNewGroup);
  const updateRecvFriendApplication = useContactStore(
    (state) => state.updateRecvFriendApplication,
  );
  const updateSendFriendApplication = useContactStore(
    (state) => state.updateSendFriendApplication,
  );
  const updateRecvGroupApplication = useContactStore(
    (state) => state.updateRecvGroupApplication,
  );
  const updateSendGroupApplication = useContactStore(
    (state) => state.updateSendGroupApplication,
  );
  let cacheConversationList = [] as ConversationItem[];
  let audioEl: HTMLAudioElement | null = null;
  const [applicationEvents] = useState(() => createApplicationEventHandlers({
    coordinator: sessionCoordinator,
    actor: () => useUserStore.getState().selfInfo.userID,
    receivedFriend: (data, expected) => updateRecvFriendApplication(data, expected),
    sentFriend: updateSendFriendApplication,
    receivedGroup: (data, expected) => updateRecvGroupApplication(data, expected),
    sentGroup: updateSendGroupApplication,
    notifyFriend: (data, current) => notifyFriendApplication(data, current),
    notifyGroup: (data, current) => notifyGroupApplication(data, current),
  }));
  useEffect(() => {
    loginCheck();
    cacheConversationList = [];
    applicationEvents.activate();
    setIMListener();
    return () => {
      applicationEvents.dispose();
      disposeIMListener();
    };
  }, []);
  useEffect(() => {
    const getContactStoreDataHandler = (key: string) => {
      window.electronAPI?.ipcInvoke("transferContactStoreData", {
        key,
        data: JSON.stringify({
          friendList: useContactStore.getState().friendList,
          groupList: useContactStore.getState().groupList,
        }),
      });
    };
    const downloadSuccessHandler = (url: string, savePath: string) => {
      const task = useMessageStore.getState().downloadMap[url];
      if (!task) return;
      const { clientMsgID, conversationID, originUrl, isMediaMessage, isThumb } =
        task;
      if (isThumb && originUrl) {
        addImageCache(originUrl, savePath);
      }
      setTimeout(sessionCoordinator.guard(() => removeDownloadTask(url)), 2000);
      if (!clientMsgID || !conversationID) return;
      IMSDK.setMessageLocalEx({
        clientMsgID,
        conversationID,
        localEx: savePath,
      }).then(sessionCoordinator.guard(() =>
        updateOneMessage(
          {
            clientMsgID,
            localEx: savePath,
          } as ExMessageItem,
          isMediaMessage,
        ),
      )).catch(() => undefined);
    };
    const unsubscribeContactStoreData = subscribeSessionEvent(
      "getContactStoreData",
      getContactStoreDataHandler,
    );
    const unsubscribeDownloadSuccess = subscribeSessionEvent(
      "downloadSuccess",
      downloadSuccessHandler,
    );
    return () => {
      unsubscribeContactStoreData?.();
      unsubscribeDownloadSuccess?.();
    };
  }, []);
  useEffect(() => {
    const downloadProgressHandler = (url: string, progress: number) => {
      const task = useMessageStore.getState().downloadMap[url];
      if (!task) return;
      updateDownloadTask(url, {
        progress,
      });
    };
    const downloadSuccessHandler = (url: string, filePath: string) => {
      const task = useMessageStore.getState().downloadMap[url];
      if (!task) return;
      updateDownloadTask(url, {
        progress: 0,
        downloadState: "finish",
      });
    };
    const downloadCancelHandler = (url: string) => {
      const task = useMessageStore.getState().downloadMap[url];
      if (!task) return;
      removeDownloadTask(url);
    };
    const downloadFailedHandler = (url: string) => {
      const task = useMessageStore.getState().downloadMap[url];
      if (!task) return;
      removeDownloadTask(url);
      if (task.showError) antdMessage.error(t("toast.applyDownloadFailed"));
    };
    const unsubscribeProgress = subscribeSessionEvent(
      "downloadProgress",
      downloadProgressHandler,
    );
    const unsubscribeSuccess = subscribeSessionEvent(
      "downloadSuccess",
      downloadSuccessHandler,
    );
    const unsubscribeCancel = subscribeSessionEvent(
      "downloadCancel",
      downloadCancelHandler,
    );
    const unsubscribeFailed = subscribeSessionEvent(
      "downloadFailed",
      downloadFailedHandler,
    );
    return () => {
      unsubscribeProgress?.();
      unsubscribeSuccess?.();
      unsubscribeCancel?.();
      unsubscribeFailed?.();
    };
  }, []);
  const loginCheck = async () => {
    const IMToken = (await getIMToken()) as string;
    const IMUserID = (await getIMUserID()) as string;
    if (!IMToken || !IMUserID) {
      await clearIMProfile();
      navigate("/login");
      return;
    }
    tryLogin();
  };
  const tryLogin = async () => {
    setConnectState((state) => ({
      ...state,
      isLogining: true,
    }));
    const IMToken = (await getIMToken()) as string;
    const IMUserID = (await getIMUserID()) as string;
    try {
      await IMSDK.login({
        userID: IMUserID,
        token: IMToken,
        platformID: window.electronAPI?.getPlatform() ?? 5,
        apiAddr: getApiUrl(),
        wsAddr: getWsUrl(),
        logLevel: LogLevel.Error,
      });
      await useUserStore.getState().getAppConfigByReq();
      await initStore();
    } catch (error) {
      if (error instanceof StaleSessionError) return;
      const failureCode = (error as WsResponse)?.errCode;
      const safeFailureCode = Number.isSafeInteger(failureCode) ? String(failureCode) : "unknown";
      console.error("SDK登录失败", safeFailureCode);
      if ((error as WsResponse).errCode !== 10102) {
        await clearIMProfile();
        navigate(`/login?imError=${safeFailureCode}`, { replace: true });
      } else {
      }
    }
    setConnectState((state) => ({
      ...state,
      isLogining: false,
    }));
  };
  const setIMListener = () => {
    // account
    IMSDK.on(CbEvents.OnSelfInfoUpdated, selfUpdateHandler);
    IMSDK.on(CbEvents.OnConnecting, connectingHandler);
    IMSDK.on(CbEvents.OnConnectFailed, connectFailedHandler);
    IMSDK.on(CbEvents.OnConnectSuccess, connectSuccessHandler);
    IMSDK.on(CbEvents.OnKickedOffline, kickHandler);
    IMSDK.on(CbEvents.OnUserTokenExpired, expiredHandler);
    // sync
    IMSDK.on(CbEvents.OnSyncServerStart, syncStartHandler);
    IMSDK.on(CbEvents.OnSyncServerFinish, syncFinishHandler);
    IMSDK.on(CbEvents.OnSyncServerFailed, syncFailedHandler);
    // message
    IMSDK.on(CbEvents.OnRecvNewMessage, newMessageHandler);
    IMSDK.on(CbEvents.OnRecvNewMessages, newMessageHandler);
    IMSDK.on(CbEvents.OnNewRecvMessageRevoked, revokedMessageHandler);
    // conversation
    IMSDK.on(CbEvents.OnConversationChanged, conversationChnageHandler);
    IMSDK.on(CbEvents.OnNewConversation, newConversationHandler);
    IMSDK.on(CbEvents.OnTotalUnreadMessageCountChanged, totalUnreadChangeHandler);
    // friend
    IMSDK.on(CbEvents.OnFriendInfoChanged, friednInfoChangeHandler);
    IMSDK.on(CbEvents.OnFriendAdded, friednAddedHandler);
    IMSDK.on(CbEvents.OnFriendDeleted, friednDeletedHandler);
    // blacklist
    IMSDK.on(CbEvents.OnBlackAdded, blackAddedHandler);
    IMSDK.on(CbEvents.OnBlackDeleted, blackDeletedHandler);
    // group
    IMSDK.on(CbEvents.OnJoinedGroupAdded, joinedGroupAddedHandler);
    IMSDK.on(CbEvents.OnJoinedGroupDeleted, joinedGroupDeletedHandler);
    IMSDK.on(CbEvents.OnGroupDismissed, joinedGroupDismissHandler);
    IMSDK.on(CbEvents.OnGroupInfoChanged, groupInfoChangedHandler);
    IMSDK.on(CbEvents.OnGroupMemberAdded, groupMemberAddedHandler);
    IMSDK.on(CbEvents.OnGroupMemberDeleted, groupMemberDeletedHandler);
    IMSDK.on(CbEvents.OnGroupMemberInfoChanged, groupMemberInfoChangedHandler);
    // application
    IMSDK.on(CbEvents.OnFriendApplicationAdded, friendApplicationProcessedHandler);
    IMSDK.on(CbEvents.OnFriendApplicationAccepted, friendApplicationProcessedHandler);
    IMSDK.on(CbEvents.OnFriendApplicationRejected, friendApplicationProcessedHandler);
    IMSDK.on(CbEvents.OnGroupApplicationAdded, groupApplicationProcessedHandler);
    IMSDK.on(CbEvents.OnGroupApplicationAccepted, groupApplicationProcessedHandler);
    IMSDK.on(CbEvents.OnGroupApplicationRejected, groupApplicationProcessedHandler);
    // custom
    IMSDK.on(CbEvents.OnRecvCustomBusinessMessage, customMessageHandler);
  };
  const selfUpdateHandler = ({ data }: WSEvent<SelfUserInfo>) => {
    updateSelfInfo(data);
  };
  const connectingHandler = () => setConnectState((state) => ({ ...state, isConnecting: true, isConnected: false }));
  const connectFailedHandler = ({ errCode, errMsg }: WSEvent) => {
    setConnectState((state) => ({ ...state, isConnecting: false, isConnected: false }));
    console.error("消息服务连接失败", Number.isSafeInteger(errCode) ? errCode : "unknown");
    if (errCode === 705) {
      tryOut(t("toast.loginExpiration"));
    }
  };
  const connectSuccessHandler = () => setConnectState((state) => ({ ...state, isConnecting: false, isConnected: true }));
  // Our exact logout intentionally closes this socket. It is not evidence
  // that another device signed in, and the in-flight HTTP result owns cleanup.
  const kickHandler = () => { if (!isExplicitLogoutPending()) tryOut(t("toast.accountKicked")); };
  const expiredHandler = () => { if (!isExplicitLogoutPending()) tryOut(t("toast.loginExpiration")); };
  const tryOut = (msg: string) =>
    feedbackToast({
      msg,
      error: msg,
      onClose: sessionCoordinator.guard(() => {
        userLogout(true);
      }),
    });

  // sync
  const syncStartHandler = () => {
    setConnectState((state) => ({
      ...state,
      isSyncing: true,
    }));
  };
  const syncFinishHandler = async () => {
    setConnectState((state) => ({
      ...state,
      isSyncing: false,
    }));
    try {
      await useConversationStore.getState().getConversationListByReq();
    } catch (e) {
      console.error("syncFinishHandler refresh error", e);
    }
  };
  const syncFailedHandler = () => {
    feedbackToast({
      msg: t("toast.syncFailed"),
      error: t("toast.syncFailed"),
    });
    setConnectState((state) => ({
      ...state,
      isSyncing: false,
    }));
  };

  // message
  const newMessageHandler = ({ data }: WSEvent<ExMessageItem[]>) => {
    if (latestConnectState.current.isSyncing) {
      if (data.some((message) => inCurrentConversation(message))) {
        emitter.emit("REFRESH_CHAT_LIST");
      }
      return;
    }
    data.map((message) => handleNewMessage(message));
  };
  const revokedMessageHandler = ({ data }: WSEvent<RevokedInfo>) => {
    updateOneMessage({
      clientMsgID: data.clientMsgID,
      contentType: MessageType.RevokeMessage,
      isAppend: true,
      notificationElem: {
        detail: JSON.stringify(data),
      },
    } as ExMessageItem);
  };
  const newMessageNotify = async (newServerMsg: ExMessageItem) => {
    if (latestConnectState.current.isSyncing) {
      return;
    }
    const selfInfo = useUserStore.getState().selfInfo;
    if (
      selfInfo.allowBeep === BusinessAllowType.NotAllow ||
      selfInfo.globalRecvMsgOpt !== MessageReceiveOptType.Nomal
    ) {
      return;
    }
    let cveItem = [
      ...useConversationStore.getState().conversationList,
      ...cacheConversationList,
    ].find((conversation) => {
      if (isGroupSession(newServerMsg.sessionType)) {
        return newServerMsg.groupID === conversation.groupID;
      }
      return newServerMsg.sendID === conversation.userID;
    });
    if (!cveItem) {
      try {
        const { data } = await IMSDK.getOneConversation({
          sessionType: newServerMsg.sessionType,
          sourceID: newServerMsg.groupID || newServerMsg.sendID,
        });
        cveItem = data;
        cacheConversationList = [
          ...cacheConversationList,
          {
            ...cveItem,
          },
        ];
      } catch (e) {
        return;
      }
    }
    if (cveItem.recvMsgOpt !== MessageReceiveOptType.Nomal) {
      return;
    }
    createNotification({
      message: newServerMsg,
      conversation: cveItem,
      callback: (conversation) => {
        if (
          useConversationStore.getState().currentConversation?.conversationID ===
          conversation.conversationID
        )
          return;
        updateCurrentConversation({
          ...conversation,
          unreadCount: 1,
        });
        navigate(`/chat/${conversation.conversationID}`);
      },
    });
    if (!audioEl) {
      audioEl = document.createElement("audio");
    }
    audioEl.src = messageRing;
    audioEl.play();
  };
  const { run: checkOnline } = useThrottleFn(() => emitter.emit("ONLINE_STATE_CHECK"), {
    wait: 2000,
  });
  const { run: checkTyping } = useThrottleFn(() => emitter.emit("TYPING_UPDATE"), {
    wait: 2000,
  });
  const { run: newMessageNotification } = useThrottleFn(newMessageNotify, {
    wait: 2000,
  });
  const notPushType = [MessageType.TypingMessage, MessageType.RevokeMessage];
  const handleNewMessage = (newServerMsg: ExMessageItem) => {
    if (!inCurrentConversation(newServerMsg)) {
      const needNotification =
        !notPushType.includes(newServerMsg.contentType) &&
        newServerMsg.sendID !== useUserStore.getState().selfInfo.userID;
      if (needNotification) {
        newMessageNotification(newServerMsg);
      }
      return;
    }
    const isSingleMessage = newServerMsg.sessionType === SessionType.Single;
    if (isSingleMessage) {
      if (newServerMsg.contentType === MessageType.TypingMessage) {
        checkTyping();
      }
      checkOnline();
    }
    if (!notPushType.includes(newServerMsg.contentType)) {
      const needAppend =
        newServerMsg.sendID !== useUserStore.getState().selfInfo.userID ||
        SystemMessageTypes.includes(newServerMsg.contentType);
      if (useMessageStore.getState().jumpClientMsgID) {
        if (needAppend) {
          emitter.emit("ADD_NEW_MESSAGE_COUNT");
        }
        return;
      }
      newServerMsg.isAppend = needAppend;
      pushNewMessage(newServerMsg);
      tryAddPreviewImg([newServerMsg]);
      emitter.emit("CHAT_LIST_SCROLL_TO_BOTTOM", false);
    }
  };
  const inCurrentConversation = (newServerMsg: ExMessageItem) => {
    switch (newServerMsg.sessionType) {
      case SessionType.Single:
        return (
          newServerMsg.sendID ===
            useConversationStore.getState().currentConversation?.userID ||
          (newServerMsg.sendID === useUserStore.getState().selfInfo.userID &&
            newServerMsg.recvID ===
              useConversationStore.getState().currentConversation?.userID)
        );
      case SessionType.Group:
      case SessionType.WorkingGroup:
        return (
          newServerMsg.groupID ===
          useConversationStore.getState().currentConversation?.groupID
        );
      case SessionType.Notification:
        return (
          newServerMsg.sendID ===
          useConversationStore.getState().currentConversation?.userID
        );
      default:
        return false;
    }
  };

  // Helper function to update conversation showName with friend remark
  const applyFriendRemarkToConversations = (
    conversations: ConversationItem[],
  ): ConversationItem[] => {
    const friendList = useContactStore.getState().friendList;
    return conversations.map((conversation) => {
      // Only process single chat (not group)
      if (conversation.userID && !conversation.groupID) {
        const friend = friendList.find((f) => f.userID === conversation.userID);
        if (friend?.remark) {
          return {
            ...conversation,
            showName: friend.remark,
          };
        }
      }
      return conversation;
    });
  };

  // conversation
  const conversationChnageHandler = ({ data }: WSEvent<ConversationItem[]>) => {
    // Skip during sync to avoid overwriting remark set in getConversationListByReq
    if (latestConnectState.current.isSyncing) {
      return;
    }
    const updatedData = applyFriendRemarkToConversations(data);
    updateConversationList(updatedData, "filter");
  };
  const newConversationHandler = ({ data }: WSEvent<ConversationItem[]>) => {
    // Skip during sync to avoid overwriting remark set in getConversationListByReq
    if (latestConnectState.current.isSyncing) {
      return;
    }
    const updatedData = applyFriendRemarkToConversations(data);
    updateConversationList(updatedData, "push");
  };
  const totalUnreadChangeHandler = () => {
    if (latestConnectState.current.isSyncing) {
      return;
    }
  };

  // friend
  const friednInfoChangeHandler = ({ data }: WSEvent<FriendUserItem>) => {
    const currentConversation = useConversationStore.getState().currentConversation;
    if (currentConversation?.userID === data.userID || currentConversation?.groupID) {
      updateMessageNicknameAndFaceUrl({
        sendID: data.userID,
        senderNickname: data.remark || data.nickname,
        senderFaceUrl: data.faceURL,
      });
    }
    updateFriend(data);

    // Update conversation showName when friend info changes (especially remark)
    const conversationList = useConversationStore.getState().conversationList;
    const targetConversation = conversationList.find(
      (c) => c.userID === data.userID && !c.groupID,
    );
    if (targetConversation) {
      const updatedConversation = {
        ...targetConversation,
        showName: data.remark || data.nickname,
        faceURL: data.faceURL,
      };
      updateConversationList([updatedConversation], "filter");
    }
  };
  const friednAddedHandler = ({ data }: WSEvent<FriendUserItem>) => {
    pushNewFriend(data);
  };
  const friednDeletedHandler = ({ data }: WSEvent<FriendUserItem>) => {
    updateFriend(data, true);
  };

  // blacklist
  const blackAddedHandler = ({ data }: WSEvent<BlackUserItem>) => {
    pushNewBlack(data);
  };
  const blackDeletedHandler = ({ data }: WSEvent<BlackUserItem>) => {
    updateBlack(data, true);
  };

  // group
  const joinedGroupAddedHandler = ({ data }: WSEvent<GroupItem>) => {
    if (data.groupID === useConversationStore.getState().currentConversation?.groupID) {
      updateCurrentGroupInfo(data);
      // getCurrentMemberInGroupByReq(group.groupID);
    }
    pushNewGroup(data);
  };
  const joinedGroupDeletedHandler = ({ data }: WSEvent<GroupItem>) => {
    if (data.groupID === useConversationStore.getState().currentConversation?.groupID) {
      getCurrentGroupInfoByReq(data.groupID);
      // getCurrentMemberInGroupByReq(data.groupID);
    }
    updateGroup(data, true);
  };
  const joinedGroupDismissHandler = ({ data }: WSEvent<GroupItem>) => {
    if (data.groupID === useConversationStore.getState().currentConversation?.groupID) {
      getCurrentMemberInGroupByReq(data.groupID);
    }
  };
  const groupInfoChangedHandler = ({ data }: WSEvent<GroupItem>) => {
    updateGroup(data);
    if (data.groupID === useConversationStore.getState().currentConversation?.groupID) {
      updateCurrentGroupInfo(data);
    }
  };
  const groupMemberAddedHandler = ({ data }: WSEvent<GroupMemberItem>) => {
    if (
      data.groupID === useConversationStore.getState().currentConversation?.groupID &&
      data.userID === useUserStore.getState().selfInfo.userID
    ) {
      getCurrentMemberInGroupByReq(data.groupID);
    }
  };
  const groupMemberDeletedHandler = ({ data }: WSEvent<GroupMemberItem>) => {
    if (
      data.groupID === useConversationStore.getState().currentConversation?.groupID &&
      data.userID === useUserStore.getState().selfInfo.userID
    ) {
      getCurrentMemberInGroupByReq(data.groupID);
    }
  };
  const groupMemberInfoChangedHandler = ({ data }: WSEvent<GroupMemberItem>) => {
    if (data.groupID === useConversationStore.getState().currentConversation?.groupID) {
      const friend = useContactStore
        .getState()
        .friendList.find((f) => f.userID === data.userID);
      updateMessageNicknameAndFaceUrl({
        sendID: data.userID,
        senderNickname: friend?.remark || data.nickname,
        senderFaceUrl: data.faceURL,
      });
      tryUpdateCurrentMemberInGroup(data);
    }
  };

  //application
  function notifyFriendApplication(data: FriendApplicationItem, current: () => boolean) {
    if (!current()) return;
    if (!audioEl) audioEl = document.createElement("audio");
    audioEl.src = messageRing;
    audioEl.play();
    if (window.Notification && window.Notification.permission === "granted" && document.hidden) {
      const notification = new Notification(t("toast.newFriendApplication"), {
        dir: "auto",
        tag: `friend_app_${data.fromUserID}`,
        icon: data.fromFaceURL || generateAvatar(data.fromNickname),
        body: `${data.fromNickname} ${t("toast.requestAddFriend")}`,
        requireInteraction: true,
      } as NotificationOptions);
      notification.onclick = () => {
        if (current()) {
          window.electronAPI?.ipcInvoke("showMainWindow");
          navigate("/contact/newFriends");
        }
        notification.close();
      };
    }
  }
  function notifyGroupApplication(data: GroupApplicationItem, current: () => boolean) {
    if (!current()) return;
    if (!audioEl) audioEl = document.createElement("audio");
    audioEl.src = messageRing;
    audioEl.play();
    if (window.Notification && window.Notification.permission === "granted" && document.hidden) {
      const notification = new Notification(t("toast.newGroupApplication"), {
        dir: "auto",
        tag: `group_app_${data.userID}_${data.groupID}`,
        icon: data.userFaceURL || generateAvatar(data.nickname),
        body: `${data.nickname} ${t("toast.requestJoinGroup")} ${data.groupName}`,
        requireInteraction: true,
      } as NotificationOptions);
      notification.onclick = () => {
        if (current()) {
          window.electronAPI?.ipcInvoke("showMainWindow");
          navigate("/contact/groupNotifications");
        }
        notification.close();
      };
    }
  }
  const friendApplicationProcessedHandler = applicationEvents.friend;
  const groupApplicationProcessedHandler = applicationEvents.group;

  // custom
  const customMessageHandler = ({
    data: { key },
  }: WSEvent<{
    key: string;
    data: string;
  }>) => {
    if (key.includes("wm_")) {
      getWorkMomentsUnreadCount();
    }
  };
  const disposeIMListener = () => {
    IMSDK.off(CbEvents.OnSelfInfoUpdated, selfUpdateHandler);
    IMSDK.off(CbEvents.OnConnecting, connectingHandler);
    IMSDK.off(CbEvents.OnConnectFailed, connectFailedHandler);
    IMSDK.off(CbEvents.OnConnectSuccess, connectSuccessHandler);
    IMSDK.off(CbEvents.OnKickedOffline, kickHandler);
    IMSDK.off(CbEvents.OnUserTokenExpired, expiredHandler);
    // sync
    IMSDK.off(CbEvents.OnSyncServerStart, syncStartHandler);
    IMSDK.off(CbEvents.OnSyncServerFinish, syncFinishHandler);
    IMSDK.off(CbEvents.OnSyncServerFailed, syncFailedHandler);
    // message
    IMSDK.off(CbEvents.OnRecvNewMessage, newMessageHandler);
    IMSDK.off(CbEvents.OnRecvNewMessages, newMessageHandler);
    // conversation
    IMSDK.off(CbEvents.OnConversationChanged, conversationChnageHandler);
    IMSDK.off(CbEvents.OnNewConversation, newConversationHandler);
    IMSDK.off(CbEvents.OnTotalUnreadMessageCountChanged, totalUnreadChangeHandler);
    // friend
    IMSDK.off(CbEvents.OnFriendInfoChanged, friednInfoChangeHandler);
    IMSDK.off(CbEvents.OnFriendAdded, friednAddedHandler);
    IMSDK.off(CbEvents.OnFriendDeleted, friednDeletedHandler);
    // blacklist
    IMSDK.off(CbEvents.OnBlackAdded, blackAddedHandler);
    IMSDK.off(CbEvents.OnBlackDeleted, blackDeletedHandler);
    // group
    IMSDK.off(CbEvents.OnJoinedGroupAdded, joinedGroupAddedHandler);
    IMSDK.off(CbEvents.OnJoinedGroupDeleted, joinedGroupDeletedHandler);
    IMSDK.off(CbEvents.OnGroupDismissed, joinedGroupDismissHandler);
    IMSDK.off(CbEvents.OnGroupInfoChanged, groupInfoChangedHandler);
    IMSDK.off(CbEvents.OnGroupMemberAdded, groupMemberAddedHandler);
    IMSDK.off(CbEvents.OnGroupMemberDeleted, groupMemberDeletedHandler);
    IMSDK.off(CbEvents.OnGroupMemberInfoChanged, groupMemberInfoChangedHandler);
    // application
    IMSDK.off(CbEvents.OnFriendApplicationAdded, friendApplicationProcessedHandler);
    IMSDK.off(CbEvents.OnFriendApplicationAccepted, friendApplicationProcessedHandler);
    IMSDK.off(CbEvents.OnFriendApplicationRejected, friendApplicationProcessedHandler);
    IMSDK.off(CbEvents.OnGroupApplicationAdded, groupApplicationProcessedHandler);
    IMSDK.off(CbEvents.OnGroupApplicationAccepted, groupApplicationProcessedHandler);
    IMSDK.off(CbEvents.OnGroupApplicationRejected, groupApplicationProcessedHandler);
    // custom
    IMSDK.off(CbEvents.OnRecvCustomBusinessMessage, customMessageHandler);
  };
  return [connectState];
}
