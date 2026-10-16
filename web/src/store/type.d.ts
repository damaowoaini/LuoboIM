import {
  BlackUserItem,
  ConversationItem,
  FriendApplicationItem,
  FriendUserItem,
  GroupApplicationItem,
  GroupItem,
  GroupMemberItem,
  MessageItem,
  SelfUserInfo,
} from "open-im-sdk-wasm/lib/types/entity";

import { BusinessUserInfo } from "@/api/login";
import type { Session } from "@/core/session/session";

import { ExMessageItem } from "./message";

export interface UserStore {
  selfInfo: BusinessUserInfo;
  appConfig: AppConfig;
  appConfigStatus: "idle" | "loading" | "ready" | "error";
  appSettings: AppSettings;
  imageCache: Record<string, string>;
  workMomentsUnreadCount: number;
  updateSelfInfo: (info: Partial<BusinessUserInfo>) => void;
  getSelfInfoByReq: () => void;
  getAppConfigByReq: () => Promise<void>;
  updateAppSettings: (settings: Partial<AppSettings>) => void;
  userLogout: (force?: boolean) => Promise<void>;
  getWorkMomentsUnreadCount: () => Promise<void>;
  updateWorkMomentsUnreadCount: (count?: number) => void;
  initImageCache: (cache: Record<string, string>) => void;
  addImageCache: (url: string, path: string) => void;
  clearImageCache: () => void;
}

export interface AppConfig {
  needVerificationCodeRegister?: string | number | boolean;
  discoverPageURL: string;
  ordinaryUserAddFriend: number;
  allowSendMsgNotFriend: number;
  needInvitationCodeRegister: number;
}

export interface UpdateMessaggeBaseInfoParams {
  sendID: string;
  senderNickname: string;
  senderFaceUrl: string;
}

export interface AppSettings {
  locale: LocaleString;
  closeAction: "miniSize" | "quit";
}

export type LocaleString = "zh-CN" | "en-US";

export type ConversationListUpdateType = "push" | "filter";

export type RevokeMessageData = {
  quoteMessage?: MessageItem;
  text: string;
};

export interface ConversationStore {
  captureConversationSendScope: () => import("@/core/messageSendIntent").ConversationSendScope | undefined;
  conversationList: ConversationItem[];
  currentConversation?: ConversationItem;
  unReadCount: number;
  currentGroupInfo?: GroupItem;
  currentMemberInGroup?: GroupMemberItem;
  quoteMessage?: MessageItem;
  revokeMap: Record<string, RevokeMessageData>;
  getConversationListByReq: (isOffset?: boolean) => Promise<boolean>;
  updateConversationList: (
    list: ConversationItem[],
    type: ConversationListUpdateType,
  ) => void;
  delConversationByCID: (conversationID: string) => void;
  // getCurrentConversationByReq: (conversationID?: string) => Promise<void>;
  updateCurrentConversation: (
    conversation?: ConversationItem,
    isJump?: boolean,
  ) => void;
  getUnReadCountByReq: () => Promise<number>;
  updateUnReadCount: (count: number) => void;
  getCurrentGroupInfoByReq: (groupID: string) => Promise<void>;
  updateCurrentGroupInfo: (groupInfo: GroupItem) => void;
  getCurrentMemberInGroupByReq: (groupID: string) => Promise<void>;
  tryUpdateCurrentMemberInGroup: (member: GroupMemberItem) => void;
  updateQuoteMessage: (message?: MessageItem) => void;
  addRevokedMessage: (message: MessageItem, quoteMessage?: MessageItem) => void;
  clearConversationStore: () => void;
}

export type PreviewGroupItem = {
  url: string;
  thumbUrl?: string;
  videoUrl?: string;
  sourceMessage?: ExMessageItem;
  clientMsgID: string;
};

export type DownloadState = "downloading" | "pause" | "resume" | "cancel" | "finish";

export type SaveType = "avatar" | "image" | "video" | "file";

export type DownloadData = {
  clientMsgID?: string;
  workMomentID?: string;
  conversationID?: string;
  downloadState?: DownloadState;
  progress?: number;
  originUrl?: string;
  downloadUrl?: string;
  isMediaMessage?: boolean;
  isThumb?: boolean;
  showError?: boolean;
  saveType?: SaveType;
  randomName?: boolean;
};

export interface GetMessageReverseParams {
  message: ExMessageItem;
  conversationID: string;
}

export interface MessageStore {
  historyMessageList: ExMessageItem[];
  previewImgList: PreviewGroupItem[];
  jumpLoading: boolean;
  jumpClientMsgID?: string;
  lastMinSeq: number;
  hasMore: boolean;
  laterHasMore: boolean;
  isCheckMode: boolean;
  downloadMap: Record<string, DownloadData>;
  getHistoryMessageListByReq: (loadMore?: boolean) => Promise<unknown>;
  getHistoryMessageListReverseByReq: () => Promise<unknown>;
  getTwoWayHistoryMessage: (param: GetMessageReverseParams) => Promise<unknown>;
  clearAppendState: () => void;
  updateJumpClientMsgID: (clientMsgID?: string) => void;
  pushNewMessage: (message: ExMessageItem) => void;
  updateOneMessage: (message: ExMessageItem, fromImageDownload?: boolean) => void;
  updateMessageNicknameAndFaceUrl: (params: UpdateMessaggeBaseInfoParams) => void;
  deleteAndPushOneMessage: (message: ExMessageItem) => void;
  deleteOneMessage: (clientMsgID: string) => void;
  clearHistoryMessage: () => void;
  updateCheckMode: (isCheckMode: boolean) => void;
  getConversationPreviewImgList: () => Promise<void>;
  tryAddPreviewImg: (messageList: ExMessageItem[]) => void;
  addDownloadTask: (url: string, data: DownloadData) => void;
  updateDownloadTask: (url: string, data: DownloadData) => void;
  removeDownloadTask: (url: string) => void;
}

export interface ContactApplicationOwner {
  owner: Session;
  generation: number;
}

// Only non-sensitive state is enumerable. Captured Session/actor/tenant leases
// remain inside contact.ts's private WeakMap, never in serializable store data.
export type FriendApplicationReadPhase = "idle" | "loading" | "ready" | "error";
export type FriendApplicationReadState = Readonly<{
  phase: Exclude<FriendApplicationReadPhase, "idle">;
  requestID: number;
}>;

export interface ContactStore {
  friendList: FriendUserItem[];
  blackList: BlackUserItem[];
  blackListReadState: FriendApplicationReadState | undefined;
  groupList: GroupItem[];
  recvFriendApplicationList: FriendApplicationItem[];
  recvFriendApplicationReadState: FriendApplicationReadState | undefined;
  sendFriendApplicationReadState: FriendApplicationReadState | undefined;
  sendFriendApplicationList: FriendApplicationItem[];
  recvGroupApplicationList: GroupApplicationItem[];
  sendGroupApplicationList: GroupApplicationItem[];
  unHandleFriendApplicationCount: number;
  unHandleGroupApplicationCount: number;
  getFriendListByReq: () => Promise<void>;
  setFriendList: (list: FriendUserItem[]) => void;
  updateFriend: (friend: FriendUserItem, remove?: boolean) => void;
  pushNewFriend: (friend: FriendUserItem) => void;
  getBlackListReadPhase: (expected: ContactApplicationOwner) => FriendApplicationReadPhase;
  getBlackListByReq: (expected?: ContactApplicationOwner) => Promise<void>;
  updateBlack: (black: BlackUserItem, remove?: boolean) => void;
  pushNewBlack: (black: BlackUserItem) => void;
  getGroupListByReq: () => Promise<void>;
  setGroupList: (list: GroupItem[]) => void;
  updateGroup: (group: GroupItem, remove?: boolean) => void;
  pushNewGroup: (group: GroupItem) => void;
  getFriendApplicationReadPhase: (direction: "recv" | "send", expected: ContactApplicationOwner) => FriendApplicationReadPhase;
  getRecvFriendApplicationListByReq: (expected?: ContactApplicationOwner) => Promise<void>;
  updateRecvFriendApplication: (application: FriendApplicationItem, expected?: ContactApplicationOwner) => Promise<void>;
  getSendFriendApplicationListByReq: (expected?: ContactApplicationOwner) => Promise<void>;
  updateSendFriendApplication: (application: FriendApplicationItem) => void;
  getRecvGroupApplicationListByReq: () => Promise<void>;
  updateRecvGroupApplication: (application: GroupApplicationItem, expected?: ContactApplicationOwner) => Promise<void>;
  getSendGroupApplicationListByReq: () => Promise<void>;
  updateSendGroupApplication: (application: GroupApplicationItem) => void;
  updateUnHandleFriendApplicationCount: (num: number) => void;
  updateUnHandleGroupApplicationCount: (num: number) => void;
  clearContactStore: () => void;
}
