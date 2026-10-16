import { conversationSendTarget, knownGroupSendAllowed, sameSendTarget } from "@/core/messageSendIntent";
import { t } from "i18next";
import { GroupStatus, MessageType } from "open-im-sdk-wasm";
import {
  ConversationItem,
  GroupItem,
  GroupMemberItem,
  MessageItem,
} from "open-im-sdk-wasm/lib/types/entity";
import { create } from "zustand";
import { sessionCoordinator } from "@/core/session/browser";

import { IMSDK } from "@/layout/MainContentWrap";
import { feedbackToast } from "@/utils/common";
import { conversationSort, isGroupSession } from "@/utils/imCommon";

import { useContactStore } from "./contact";
import { useMessageStore } from "./message";
import {
  ConversationListUpdateType,
  ConversationStore,
  RevokeMessageData,
} from "./type";
import { useUserStore } from "./user";

const CONVERSATION_SPLIT_COUNT = 500;

const isGroupInfo = (value: GroupItem | undefined, groupID: string) =>
  value?.groupID === groupID &&
  [GroupStatus.Nomal, GroupStatus.Baned, GroupStatus.Dismissed, GroupStatus.Muted]
    .includes(value.status);

const calcUnreadCount = (list: ConversationItem[]) =>
  list.reduce((acc, cur) => acc + (cur.latestMsg ? (cur.unreadCount || 0) : 0), 0);

const setWithUnread = (
  set: (fn: (state: any) => any) => void,
  updates: Record<string, any>,
) => {
  set((state: any) => {
    const next = { ...state, ...updates };
    if ("conversationList" in updates) {
      next.unReadCount = calcUnreadCount(updates.conversationList);
      window.electronAPI?.ipcInvoke("updateUnreadCount", next.unReadCount);
    }
    return next;
  });
};

export const useConversationStore = create<ConversationStore>()((set, get) => {
  // Navigation/read tickets are private, not serializable session credentials
  // in Zustand. SDK generation guards already exist; these also fence A→B→A
  // within the same authenticated session and current events over older reads.
  let navigationEpoch = 0;
  let groupRead = 0;
  let memberRead = 0;
  let viewedOwner: typeof sessionCoordinator.current;
  const captureGroup = (groupID: string) => {
    const conversation = get().currentConversation;
    const owner = sessionCoordinator.current;
    if (!owner?.userID || typeof groupID !== "string" || !groupID ||
        !conversation || !isGroupSession(conversation.conversationType) ||
        conversation.groupID !== groupID || viewedOwner !== owner) return;
    const generation = sessionCoordinator.generation;
    const actor = owner.userID;
    const issuer = owner.issuer;
    const tenant = owner.tenant.serverID;
    const initialActor = useUserStore.getState().selfInfo.userID;
    const bootstrap = initialActor === undefined || initialActor === "";
    if (!bootstrap && initialActor !== actor) return;
    const epoch = navigationEpoch;
    const conversationID = conversation.conversationID;
    return { actor, current: () => {
      const current = get().currentConversation;
      const uiActor = useUserStore.getState().selfInfo.userID;
      return navigationEpoch === epoch && viewedOwner === owner &&
        sessionCoordinator.current === owner && sessionCoordinator.generation === generation &&
        owner.userID === actor && owner.issuer === issuer && owner.tenant.serverID === tenant &&
        current?.conversationID === conversationID && current.groupID === groupID &&
        isGroupSession(current.conversationType) &&
        (uiActor === actor || (bootstrap && (uiActor === undefined || uiActor === "")));
    } };
  };
  return {
    // A read ticket covers every conversation kind, including notification
    // cards. It conveys no send or relationship permission.
    captureConversationViewScope: () => {
      const conversation = get().currentConversation;
      const conversationID = conversation?.conversationID;
      const groupID = isGroupSession(conversation?.conversationType) ? conversation?.groupID : undefined;
      if (isGroupSession(conversation?.conversationType) && !groupID) return;
      const owner = sessionCoordinator.current;
      if (typeof conversationID !== "string" || !conversationID || !owner || viewedOwner !== owner) return;
      const epoch = navigationEpoch, generation = sessionCoordinator.generation;
      const actor = owner.userID, issuer = owner.issuer, tenant = owner.tenant.serverID;
      return Object.freeze({ conversationID, groupID, current: () => navigationEpoch === epoch &&
        viewedOwner === owner && sessionCoordinator.current === owner && sessionCoordinator.generation === generation &&
        owner.userID === actor && owner.issuer === issuer && owner.tenant.serverID === tenant &&
        get().currentConversation?.conversationID === conversationID &&
        get().currentConversation?.conversationType === conversation?.conversationType &&
        (isGroupSession(get().currentConversation?.conversationType) ? get().currentConversation?.groupID : undefined) === groupID });
    },
    captureConversationSendScope: () => {
      const target = conversationSendTarget(get().currentConversation);
      const view = get().captureConversationViewScope();
      const actor = sessionCoordinator.current?.userID;
      if (!target || !view || view.conversationID !== target.conversationID || !actor) return;
      const current = () => {
        const now = conversationSendTarget(get().currentConversation);
        return view.current() && !!now && now.conversationID === target.conversationID && sameSendTarget(now, target);
      };
      return Object.freeze({ target, current, canSend: () => current() && (target.kind === "user" ||
        knownGroupSendAllowed(get().currentGroupInfo, get().currentMemberInGroup, target.id, actor)) });
    },
    conversationList: [],
    currentConversation: undefined,
    unReadCount: 0,
    currentGroupInfo: undefined,
    currentMemberInGroup: undefined,
    quoteMessage: undefined,
    revokeMap: {} as Record<string, RevokeMessageData>,
    getConversationListByReq: async (isOffset?: boolean) => {
      let tmpConversationList = [] as ConversationItem[];
      try {
        const { data } = await IMSDK.getConversationListSplit({
          offset: isOffset ? get().conversationList.length : 0,
          count: CONVERSATION_SPLIT_COUNT,
        });
        tmpConversationList = data;

        const friendList = useContactStore.getState().friendList;
        tmpConversationList = tmpConversationList.map((conversation) => {
          if (conversation.userID && !conversation.groupID) {
            const friend = friendList.find((f) => f.userID === conversation.userID);
            if (friend?.remark) {
              return { ...conversation, showName: friend.remark };
            }
          }
          return conversation;
        });
      } catch (error) {
        feedbackToast({ error, msg: t("toast.getConversationFailed") });
        return true;
      }
      const newList = [
        ...(isOffset ? get().conversationList : []),
        ...tmpConversationList,
      ];
      setWithUnread(set, { conversationList: newList });
      return tmpConversationList.length === CONVERSATION_SPLIT_COUNT;
    },
    updateConversationList: (
      list: ConversationItem[],
      type: ConversationListUpdateType,
    ) => {
      const currentConv = get().currentConversation;
      const idx = list.findIndex(
        (c) => c.conversationID === currentConv?.conversationID,
      );
      if (idx > -1) {
        const incoming = list[idx];
        if (currentConv?.unreadCount === 0 && incoming.unreadCount > 0) {
          list[idx] = { ...incoming, unreadCount: 0 };
        }
        get().updateCurrentConversation(list[idx]);
      }

      if (type === "filter") {
        const newList = conversationSort([...list, ...get().conversationList]);
        setWithUnread(set, { conversationList: newList });
        return;
      }
      const chids = list.map((ch) => ch.conversationID);
      const filterArr = get().conversationList.filter(
        (tc) => !chids.includes(tc.conversationID),
      );

      setWithUnread(set, { conversationList: conversationSort([...list, ...filterArr]) });
    },
    delConversationByCID: (conversationID: string) => {
      const tmpConversationList = get().conversationList;
      const idx = tmpConversationList.findIndex(
        (cve) => cve.conversationID === conversationID,
      );
      if (idx < 0) {
        return;
      }
      tmpConversationList.splice(idx, 1);
      setWithUnread(set, { conversationList: [...tmpConversationList] });
    },
    updateCurrentConversation: (conversation?: ConversationItem, isJump?: boolean) => {
      if (!conversation) {
        ++navigationEpoch; ++groupRead; ++memberRead;
        viewedOwner = undefined;
        set(() => ({
          currentConversation: undefined,
          quoteMessage: undefined,
          currentGroupInfo: undefined,
          currentMemberInGroup: undefined,
        }));
        return;
      }
      const prevConversation = get().currentConversation;

      const toggleNewConversation =
        conversation.conversationID !== prevConversation?.conversationID;
      const groupBoundary = toggleNewConversation ||
        conversation.groupID !== prevConversation?.groupID ||
        conversation.userID !== prevConversation?.userID ||
        conversation.conversationType !== prevConversation?.conversationType ||
        isGroupSession(conversation.conversationType) !== isGroupSession(prevConversation?.conversationType) ||
        viewedOwner !== sessionCoordinator.current;
      if (groupBoundary) {
        ++navigationEpoch; ++groupRead; ++memberRead;
      }
      viewedOwner = sessionCoordinator.current;
      // Publish the identity/empty group state before dispatch, including SDK
      // cache hits; a new entry must never expose the previous group's authority.
      set(() => ({ currentConversation: { ...conversation },
        ...(groupBoundary ? { currentGroupInfo: undefined, currentMemberInGroup: undefined } : {}),
      }));
      if (toggleNewConversation && !isJump) {
        useMessageStore.getState().updateJumpClientMsgID();
      }
      if (groupBoundary && isGroupSession(conversation.conversationType)) {
        void get().getCurrentGroupInfoByReq(conversation.groupID);
        void get().getCurrentMemberInGroupByReq(conversation.groupID);
      }
    },
    getUnReadCountByReq: async () => {
      const count = calcUnreadCount(get().conversationList);
      set(() => ({ unReadCount: count }));
      return count;
    },
    updateUnReadCount: (count: number) => {
      set(() => ({ unReadCount: count }));
    },
    getCurrentGroupInfoByReq: async (groupID: string) => {
      const access = captureGroup(groupID);
      if (!access) return;
      const read = ++groupRead;
      const current = () => access.current() && read === groupRead;
      try {
        const { data } = await IMSDK.getSpecifiedGroupsInfo([groupID]);
        if (!current()) return;
        if (Array.isArray(data) && data.length === 0) {
          set({ currentGroupInfo: undefined });
          return;
        }
        if (!Array.isArray(data) || data.length !== 1 || !isGroupInfo(data[0], groupID)) {
          // Navigation already cleared old-group state. A failed same-group
          // refresh must not erase a known whole-group mute/dismissal policy.
          feedbackToast({ msg: t("toast.getGroupInfoFailed") });
          return;
        }
        set({ currentGroupInfo: { ...data[0] } });
      } catch (error) {
        if (!current()) return;
        feedbackToast({ error, msg: t("toast.getGroupInfoFailed") });
      }
    },
    updateCurrentGroupInfo: (groupInfo: GroupItem) => {
      const access = captureGroup(groupInfo?.groupID);
      if (!access?.current() || !isGroupInfo(groupInfo, groupInfo.groupID)) return;
      ++groupRead;
      set(() => ({ currentGroupInfo: { ...groupInfo } }));
    },
    getCurrentMemberInGroupByReq: async (groupID: string) => {
      const access = captureGroup(groupID);
      if (!access) return;
      const read = ++memberRead;
      const current = () => access.current() && read === memberRead;
      try {
        const { data } = await IMSDK.getSpecifiedGroupMembersInfo({
          groupID,
          userIDList: [access.actor],
        });
        if (!current()) return;
        if (Array.isArray(data) && data.length === 0) {
          set({ currentMemberInGroup: undefined });
          return;
        }
        if (!Array.isArray(data) || data.length !== 1 || data[0]?.groupID !== groupID ||
            data[0]?.userID !== access.actor) {
          set({ currentMemberInGroup: undefined });
          feedbackToast({ msg: t("toast.getGroupMemberFailed") });
          return;
        }
        set({ currentMemberInGroup: { ...data[0] } });
      } catch (error) {
        if (!current()) return;
        set({ currentMemberInGroup: undefined });
        feedbackToast({ error, msg: t("toast.getGroupMemberFailed") });
      }
    },
    tryUpdateCurrentMemberInGroup: (member: GroupMemberItem) => {
      const access = captureGroup(member?.groupID);
      if (!access?.current() || member.userID !== access.actor) return;
      ++memberRead;
      set(() => ({ currentMemberInGroup: { ...member } }));
    },
    updateQuoteMessage: (message?: MessageItem) => {
      set(() => ({ quoteMessage: message }));
    },
    addRevokedMessage: (message: MessageItem, quoteMessage?: MessageItem) => {
      set((state) => ({
        revokeMap: {
          ...state.revokeMap,
          [message.clientMsgID]: {
            text: getMessageText(message),
            quoteMessage,
          },
        },
      }));
    },
    clearConversationStore: () => {
      ++navigationEpoch; ++groupRead; ++memberRead;
      viewedOwner = undefined;
      set(() => ({
        conversationList: [],
        currentConversation: undefined,
        unReadCount: 0,
        currentGroupInfo: undefined,
        currentMemberInGroup: undefined,
        quoteMessage: undefined,
      }));
    },
  };
});

const getMessageText = (message: MessageItem) => {
  if (message.contentType === MessageType.AtTextMessage) {
    return message.atTextElem.text;
  }
  if (message.contentType === MessageType.QuoteMessage) {
    return message.quoteElem.text;
  }
  return message.textElem.content;
};
