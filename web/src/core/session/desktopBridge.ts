import { sessionCoordinator } from "./browser";
import { BusinessUserInfo } from "@/api/login";
import { useUserStore, useContactStore, useMessageStore } from "@/store";
import { AppConfig, AppSettings } from "@/store/type";
import type {
  BlackUserItem,
  FriendUserItem,
  GroupItem,
} from "open-im-sdk-wasm/lib/types/entity";
interface Snapshot {
  namespace: string;
  user: { selfInfo: BusinessUserInfo; appConfig: AppConfig; appSettings: AppSettings };
  contacts: {
    friendList: FriendUserItem[];
    groupList: GroupItem[];
    blackList: BlackUserItem[];
  };
}
const snapshot = (): Snapshot => {
  const user = useUserStore.getState();
  const contacts = useContactStore.getState();
  const { userID, nickname, faceURL, globalRecvMsgOpt, allowAddFriend, allowBeep } =
    user.selfInfo;
  return {
    namespace: sessionCoordinator.namespace,
    user: {
      selfInfo: {
        userID,
        nickname,
        faceURL,
        globalRecvMsgOpt,
        allowAddFriend,
        allowBeep,
      } as BusinessUserInfo,
      appConfig: user.appConfig,
      appSettings: user.appSettings,
    },
    contacts: {
      friendList: contacts.friendList,
      groupList: contacts.groupList,
      blackList: contacts.blackList,
    },
  };
};
export function applyDesktopSnapshot(input: unknown): void {
  if (!input || typeof input !== "object") throw new Error("窗口数据格式错误");
  const value = input as Snapshot;
  if (!sessionCoordinator.current || value.namespace !== sessionCoordinator.namespace)
    return;
  if (
    value.user?.selfInfo?.userID !== sessionCoordinator.current.userID ||
    !value.contacts ||
    !Array.isArray(value.contacts.friendList) ||
    !Array.isArray(value.contacts.groupList) ||
    !Array.isArray(value.contacts.blackList)
  )
    throw new Error("窗口数据归属无效");
  useUserStore.setState({ ...value.user, appConfigStatus: "ready", appConfigFailure: null });
  useContactStore.setState(value.contacts);
}
export function bindDesktopMainSdk(sdk: object): () => void {
  const methods = new Set([
    "getBlackList",
    "removeBlack",
    "setGlobalRecvMessageOpt",
    "deleteAllMsgFromLocalAndSvr",
    "getSpecifiedGroupMembersInfo",
  ]);
  const unsubscribe = window.electronAPI?.subscribe(
    "desktopSessionRequest",
    async (request: {
      id: string;
      method: string;
      args: unknown[];
      namespace: string;
    }) => {
      const lease = sessionCoordinator.lease();
      try {
        if (
          !sessionCoordinator.current ||
          request.namespace !== sessionCoordinator.namespace
        )
          throw new Error("会话已切换");
        let data: unknown;
        if (request.method === "snapshot") data = snapshot();
        else {
          if (!methods.has(request.method) || !Array.isArray(request.args))
            throw new Error("无效的窗口操作");
          data = await (
            sdk as Record<string, (...args: unknown[]) => Promise<unknown>>
          )[request.method](...request.args);
        }
        lease.assertCurrent();
        await window.electronAPI?.ipcInvoke("replyDesktopSession", {
          id: request.id,
          ok: true,
          data,
        });
      } catch {
        await window.electronAPI
          ?.ipcInvoke("replyDesktopSession", { id: request.id, ok: false })
          .catch(() => undefined);
      } finally {
        lease.release();
      }
    },
  );
  const publish = () => {
    if (sessionCoordinator.current)
      void window.electronAPI
        ?.ipcInvoke("pushDesktopSessionState", snapshot())
        .catch(() => undefined);
  };
  const unsubUser = useUserStore.subscribe(publish);
  const unsubContacts = useContactStore.subscribe(publish);
  return () => {
    unsubscribe?.();
    unsubUser();
    unsubContacts();
  };
}
