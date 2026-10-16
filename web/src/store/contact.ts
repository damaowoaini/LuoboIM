import { t } from "i18next";
import { ApplicationHandleResult } from "open-im-sdk-wasm";
import {
  BlackUserItem,
  FriendApplicationItem,
  FriendUserItem,
  FullUserItem,
  GroupApplicationItem,
  GroupItem,
} from "open-im-sdk-wasm/lib/types/entity";
import { create } from "zustand";
import { sessionCoordinator } from "@/core/session/browser";
import { useUserStore } from "@/store/user";
import { IMSDK } from "@/layout/MainContentWrap";
import { feedbackToast } from "@/utils/common";
import {
  getAccessedFriendApplication,
  getAccessedGroupApplication,
} from "@/utils/storage";
import type { ContactApplicationOwner, ContactStore, FriendApplicationReadState } from "./type";

type ContactOwnerAccess = {
  owner: NonNullable<typeof sessionCoordinator.current>;
  generation: number;
  actor: string;
  current(): boolean;
};

// SDK list requests begin alongside self-info hydration during initStore.
// The authenticated session is the actor authority; an empty UI actor may only
// remain empty or hydrate to that same actor. Events require an initialized actor
// unless their actual listener supplies the exact registration-owner capture.
const captureContactOwner = (allowBootstrapActor = false, expected?: ContactApplicationOwner): ContactOwnerAccess | undefined => {
  const owner = sessionCoordinator.current;
  if (!owner || !owner.userID) return;
  const generation = sessionCoordinator.generation;
  if (expected && (expected.owner !== owner || expected.generation !== generation)) return;
  const actor = owner.userID;
  const issuer = owner.issuer;
  const tenant = owner.tenant.serverID;
  const initialActor = useUserStore.getState().selfInfo.userID;
  const bootstrap = allowBootstrapActor && (initialActor === undefined || initialActor === "");
  if (!bootstrap && initialActor !== actor) return;
  return { owner, generation, actor, current: () => {
    const nextActor = useUserStore.getState().selfInfo.userID;
    return sessionCoordinator.current === owner &&
      sessionCoordinator.generation === generation && owner.userID === actor &&
      owner.issuer === issuer && owner.tenant.serverID === tenant &&
      (nextActor === actor || (bootstrap && (nextActor === undefined || nextActor === "")));
  } };
};

// Last admitted event for the existing merge key wins, independently for each
// owner/generation. This does not change the legacy friend/group merge-key rules.
const friendEventTickets = new WeakMap<object, Map<string, symbol>>();
const groupEventTickets = new WeakMap<object, Map<string, symbol>>();
const eventTicket = (access: ContactOwnerAccess, id: string,
  registry: WeakMap<object, Map<string, symbol>>) => {
  const key = JSON.stringify([access.generation, id]);
  const entries = registry.get(access.owner) ?? new Map<string, symbol>();
  const token = Symbol();
  entries.set(key, token); registry.set(access.owner, entries);
  return {
    current: () => access.current() && entries.get(key) === token,
    release: () => {
      if (entries.get(key) !== token) return;
      entries.delete(key);
      if (entries.size === 0 && registry.get(access.owner) === entries) registry.delete(access.owner);
    },
  };
};

// Bind the public opaque read ticket privately. A Session contains credentials;
// it must never become an enumerable/serializable Zustand status field.
const friendReadScopes = new WeakMap<FriendApplicationReadState, ContactOwnerAccess>();
let friendReadSequence = 0;
const friendReadState = (access: ContactOwnerAccess,
  phase: FriendApplicationReadState["phase"], requestID = ++friendReadSequence) => {
  const state: FriendApplicationReadState = Object.freeze({ phase, requestID });
  friendReadScopes.set(state, access);
  return state;
};

export const useContactStore = create<ContactStore>()((set, get) => ({
  friendList: [],
  blackList: [],
  blackListReadState: undefined,
  groupList: [],
  recvFriendApplicationList: [],
  sendFriendApplicationList: [],
  recvFriendApplicationReadState: undefined,
  sendFriendApplicationReadState: undefined,
  recvGroupApplicationList: [],
  sendGroupApplicationList: [],
  unHandleFriendApplicationCount: 0,
  unHandleGroupApplicationCount: 0,
  getFriendListByReq: async () => {
    const access = captureContactOwner(true);
    if (!access) return;
    try {
      const { data } = await IMSDK.getFriendList();
      if (!access.current()) return;
      const friendList = data.map((item) => {
        const fullItem = item as unknown as FullUserItem;
        // Merge friendUser and top-level fields (like remark)
        const friendInfo = {
          ...fullItem.friendInfo,
          ...(fullItem as any), // Include top-level fields like remark
        };
        return friendInfo as FriendUserItem;
      });
      if (!access.current()) return;
      set((state) => access.current() ? ({
        friendList,
      }) : state);
    } catch (error) {
      if (!access.current()) return;
      feedbackToast({
        error,
        msg: t("toast.getFriendListFailed"),
      });
    }
  },
  setFriendList: (list: FriendUserItem[]) => {
    set(() => ({
      friendList: list,
    }));
  },
  updateFriend: (friend: FriendUserItem, remove?: boolean) => {
    const tmpList = [...get().friendList];
    const idx = tmpList.findIndex((f) => f.userID === friend.userID);
    if (idx < 0) {
      return;
    }
    if (remove) {
      tmpList.splice(idx, 1);
    } else {
      tmpList[idx] = {
        ...friend,
      };
    }
    set(() => ({
      friendList: tmpList,
    }));
  },
  pushNewFriend: (friend: FriendUserItem) => {
    set((state) => ({
      friendList: [...state.friendList, friend],
    }));
  },
  getBlackListReadPhase: (expected) => {
    const read = get().blackListReadState;
    const access = read && friendReadScopes.get(read);
    return read && access && access.owner === expected.owner &&
      access.generation === expected.generation && access.current() ? read.phase : "idle";
  },
  getBlackListByReq: async (expected) => {
    const access = captureContactOwner(true, expected);
    if (!access) return;
    // Reuse the existing private contact-read provenance, never a Session field.
    const previous = get().blackListReadState;
    const previousAccess = previous && friendReadScopes.get(previous);
    const keepRows = previousAccess && previousAccess.owner === access.owner &&
      previousAccess.generation === access.generation && previousAccess.actor === access.actor &&
      previousAccess.current();
    const read = friendReadState(access, "loading");
    const current = () => access.current() && get().blackListReadState === read;
    set((state) => access.current() ? ({
      blackListReadState: read,
      ...(keepRows ? {} : { blackList: [] }),
    }) : state);
    try {
      const { data } = await IMSDK.getBlackList();
      if (!current()) return;
      if (!Array.isArray(data) || data.some((row) => !row || typeof row !== "object" ||
          typeof row.userID !== "string" || !row.userID.trim())) {
        throw new Error("Invalid blacklist response");
      }
      set((state) => current() ? ({
        blackList: data,
        blackListReadState: friendReadState(access, "ready", read.requestID),
      }) : state);
    } catch {
      if (!current()) return;
      set((state) => current() ? ({
        blackListReadState: friendReadState(access, "error", read.requestID),
      }) : state);
    }
  },
  updateBlack: (black: BlackUserItem, remove?: boolean) => {
    const tmpList = [...get().blackList];
    const idx = tmpList.findIndex((b) => b.userID === black.userID);
    if (idx < 0) {
      return;
    }
    if (remove) {
      tmpList.splice(idx, 1);
    } else {
      tmpList[idx] = {
        ...black,
      };
    }
    set(() => ({
      blackList: tmpList,
    }));
  },
  pushNewBlack: (black: BlackUserItem) => {
    set((state) => ({
      blackList: [...state.blackList, black],
    }));
  },
  getGroupListByReq: async () => {
    const access = captureContactOwner(true);
    if (!access) return;
    try {
      const { data } = await IMSDK.getJoinedGroupList();
      if (!access.current()) return;
      set((state) => access.current() ? ({
        groupList: data,
      }) : state);
    } catch (error) {
      if (!access.current()) return;
      feedbackToast({
        error,
        msg: t("toast.getGroupListFailed"),
      });
    }
  },
  setGroupList: (list: GroupItem[]) => {
    set(() => ({
      groupList: list,
    }));
  },
  updateGroup: (group: GroupItem, remove?: boolean) => {
    const tmpList = [...get().groupList];
    const idx = tmpList.findIndex((g) => g.groupID === group.groupID);
    if (idx < 0) {
      return;
    }
    if (remove) {
      tmpList.splice(idx, 1);
    } else {
      tmpList[idx] = {
        ...group,
      };
    }
    set(() => ({
      groupList: tmpList,
    }));
  },
  pushNewGroup: (group: GroupItem) => {
    set((state) => ({
      groupList: [...state.groupList, group],
    }));
  },
  getFriendApplicationReadPhase: (direction, expected) => {
    const read = direction === "recv" ? get().recvFriendApplicationReadState : get().sendFriendApplicationReadState;
    const access = read && friendReadScopes.get(read);
    return read && access && access.owner === expected.owner &&
      access.generation === expected.generation && access.current() ? read.phase : "idle";
  },
  getRecvFriendApplicationListByReq: async (expected) => {
    const access = captureContactOwner(true, expected);
    if (!access) return;
    const previous = get().recvFriendApplicationReadState;
    const previousAccess = previous && friendReadScopes.get(previous);
    const keepRows = previousAccess && previousAccess.owner === access.owner &&
      previousAccess.generation === access.generation && previousAccess.actor === access.actor &&
      previousAccess.current();
    const read = friendReadState(access, "loading");
    const current = () => access.current() && get().recvFriendApplicationReadState === read;
    set((state) => access.current() ? ({
      recvFriendApplicationReadState: read,
      ...(keepRows ? {} : { recvFriendApplicationList: [] }),
    }) : state);
    try {
      const { data } = await IMSDK.getFriendApplicationListAsRecipient();
      if (!current()) return;
      if (!Array.isArray(data) || data.some((row) => !row || typeof row !== "object")) {
        throw new Error("Invalid friend application list");
      }
      set((state) => current() ? ({
        recvFriendApplicationList: data,
        recvFriendApplicationReadState: friendReadState(access, "ready", read.requestID),
      }) : state);
    } catch {
      // Keep successful/current rows. A failed read is never an empty success.
      if (!current()) return;
      set((state) => current() ? ({
        recvFriendApplicationReadState: friendReadState(access, "error", read.requestID),
      }) : state);
    }
  },
  updateRecvFriendApplication: async (application: FriendApplicationItem, expected?: ContactApplicationOwner) => {
    // Only the actual registration-owner capability permits empty-actor bootstrap.
    const access = captureContactOwner(Boolean(expected), expected);
    if (!access) return;
    const incoming = { ...application };
    const ticket = eventTicket(access, incoming.fromUserID, friendEventTickets);
    try {
      const viewed = await getAccessedFriendApplication();
      if (!ticket.current()) return;
      set((state) => {
        if (!ticket.current()) return state;
        const rows = [...state.recvFriendApplicationList];
        const index = rows.findIndex((row) => row.fromUserID === incoming.fromUserID);
        if (index < 0) rows.push(incoming); else rows[index] = incoming;
        return {
          recvFriendApplicationList: rows,
          unHandleFriendApplicationCount: rows.filter((row) => row.handleResult === 0 &&
            !viewed.includes(`${row.fromUserID}_${row.createTime}`)).length,
        };
      });
    } catch (error) {
      if (ticket.current()) feedbackToast({ error });
    } finally {
      ticket.release();
    }
  },
  getSendFriendApplicationListByReq: async (expected) => {
    const access = captureContactOwner(true, expected);
    if (!access) return;
    const previous = get().sendFriendApplicationReadState;
    const previousAccess = previous && friendReadScopes.get(previous);
    const keepRows = previousAccess && previousAccess.owner === access.owner &&
      previousAccess.generation === access.generation && previousAccess.actor === access.actor &&
      previousAccess.current();
    const read = friendReadState(access, "loading");
    const current = () => access.current() && get().sendFriendApplicationReadState === read;
    set((state) => access.current() ? ({
      sendFriendApplicationReadState: read,
      ...(keepRows ? {} : { sendFriendApplicationList: [] }),
    }) : state);
    try {
      const { data } = await IMSDK.getFriendApplicationListAsApplicant();
      if (!current()) return;
      if (!Array.isArray(data) || data.some((row) => !row || typeof row !== "object")) {
        throw new Error("Invalid friend application list");
      }
      set((state) => current() ? ({
        sendFriendApplicationList: data,
        sendFriendApplicationReadState: friendReadState(access, "ready", read.requestID),
      }) : state);
    } catch {
      // Keep successful/current rows. A failed read is never an empty success.
      if (!current()) return;
      set((state) => current() ? ({
        sendFriendApplicationReadState: friendReadState(access, "error", read.requestID),
      }) : state);
    }
  },
  updateSendFriendApplication: (application: FriendApplicationItem) => {
    let tmpList = [...get().sendFriendApplicationList];
    const idx = tmpList.findIndex((a) => a.toUserID === application.toUserID);
    if (idx < 0) {
      tmpList = [...tmpList, application];
    } else {
      tmpList[idx] = {
        ...application,
      };
    }
    set(() => ({
      sendFriendApplicationList: tmpList,
    }));
  },
  getRecvGroupApplicationListByReq: async () => {
    const access = captureContactOwner(true);
    if (!access) return;
    try {
      const { data } = await IMSDK.getGroupApplicationListAsRecipient();
      if (!access.current()) return;
      set((state) => access.current() ? ({
        recvGroupApplicationList: data,
      }) : state);
    } catch (error) {
      if (!access.current()) return;
      console.error(error);
    }
  },
  updateRecvGroupApplication: async (application: GroupApplicationItem, expected?: ContactApplicationOwner) => {
    // Only the actual registration-owner capability permits empty-actor bootstrap.
    const access = captureContactOwner(Boolean(expected), expected);
    if (!access) return;
    const incoming = { ...application };
    const ticket = eventTicket(access, incoming.userID, groupEventTickets);
    try {
      // Preserve the old completed-row behavior while merging at commit time.
      const absentAtEntry = !get().recvGroupApplicationList.some((row) => row.userID === incoming.userID);
      const viewed = absentAtEntry || incoming.handleResult === ApplicationHandleResult.Unprocessed
        ? await getAccessedGroupApplication() : undefined;
      if (!ticket.current()) return;
      set((state) => {
        if (!ticket.current()) return state;
        const rows = [...state.recvGroupApplicationList];
        const index = rows.findIndex((row) => row.userID === incoming.userID);
        if (index < 0) rows.push(incoming); else rows[index] = incoming;
        return {
          recvGroupApplicationList: rows,
          ...(viewed === undefined ? {} : { unHandleGroupApplicationCount: rows.filter((row) =>
            row.handleResult === 0 && !viewed.includes(`${row.userID}_${row.reqTime}`)).length }),
        };
      });
    } catch (error) {
      if (ticket.current()) feedbackToast({ error });
    } finally {
      ticket.release();
    }
  },
  getSendGroupApplicationListByReq: async () => {
    const access = captureContactOwner(true);
    if (!access) return;
    try {
      const { data } = await IMSDK.getGroupApplicationListAsApplicant();
      if (!access.current()) return;
      set((state) => access.current() ? ({
        sendGroupApplicationList: data,
      }) : state);
    } catch (error) {
      if (!access.current()) return;
      console.error(error);
    }
  },
  updateSendGroupApplication: (application: GroupApplicationItem) => {
    let tmpList = [...get().sendGroupApplicationList];
    const idx = tmpList.findIndex((a) => a.groupID === application.groupID);
    if (idx < 0) {
      tmpList = [...tmpList, application];
    } else {
      tmpList[idx] = {
        ...application,
      };
    }
    set(() => ({
      sendGroupApplicationList: tmpList,
    }));
  },
  updateUnHandleFriendApplicationCount: (num: number) => {
    set(() => ({
      unHandleFriendApplicationCount: num,
    }));
  },
  updateUnHandleGroupApplicationCount: (num: number) => {
    set(() => ({
      unHandleGroupApplicationCount: num,
    }));
  },
  clearContactStore: () => {
    set(() => ({
      friendList: [],
      blackList: [],
      blackListReadState: undefined,
      groupList: [],
      recvFriendApplicationList: [],
      sendFriendApplicationList: [],
      recvFriendApplicationReadState: undefined,
      sendFriendApplicationReadState: undefined,
      recvGroupApplicationList: [],
      sendGroupApplicationList: [],
      unHandleFriendApplicationCount: 0,
      unHandleGroupApplicationCount: 0,
    }));
  },
}));
