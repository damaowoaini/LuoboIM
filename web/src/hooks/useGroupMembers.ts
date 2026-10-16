import { CbEvents } from "open-im-sdk-wasm";
import { GroupMemberItem, WSEvent } from "open-im-sdk-wasm/lib/types/entity";
import { useCallback, useEffect, useRef, useState } from "react";

import { sessionCoordinator } from "@/core/session/browser";
import { IMSDK } from "@/layout/MainContentWrap";
import { useConversationStore, useUserStore } from "@/store";
import { useContactStore } from "@/store/contact";
import { feedbackToast } from "@/utils/common";

export const REACH_SEARCH_FLAG = "LAST_FLAG";
export interface FetchStateType {
  offset: number;
  searchOffset: number;
  count: number;
  loading: boolean;
  hasMore: boolean;
  groupMemberList: GroupMemberItem[];
  searchMemberList: GroupMemberItem[];
}
interface UseGroupMembersProps {
  groupID?: string;
  notRefresh?: boolean;
  current?: () => boolean;
}
const emptyState = (): FetchStateType => ({ offset: 0, searchOffset: 0, count: 20,
  loading: false, hasMore: true, groupMemberList: [], searchMemberList: [] });
const sameFact = (a: GroupMemberItem, b: GroupMemberItem) => a.groupID === b.groupID &&
  a.userID === b.userID && a.roleLevel === b.roleLevel && a.muteEndTime === b.muteEndTime;
const validMember = (member: GroupMemberItem, groupID: string) => member?.groupID === groupID &&
  typeof member.userID === "string" && !!member.userID && [20, 60, 100].includes(member.roleLevel) &&
  Number.isSafeInteger(member.muteEndTime) && member.muteEndTime >= 0;

export default function useGroupMembers(props?: UseGroupMembersProps) {
  const { groupID, notRefresh, current: callerCurrent } = props ?? {};
  const [fetchState, setFetchState] = useState<FetchStateType>(emptyState);
  const stateRef = useRef(fetchState);
  const mounted = useRef(true);
  const epoch = useRef(0);
  const request = useRef(0);
  const authority = useRef(0);
  const revisions = useRef(new Map<string, number>());
  const selfRemoved = useRef<{ value: GroupMemberItem | undefined } | undefined>(undefined);
  const lastKeyword = useRef("");
  const write = (next: FetchStateType) => { stateRef.current = next; setFetchState(next); };
  const capture = useCallback((trackEpoch = true) => {
    const sourceID = groupID ?? useConversationStore.getState().currentConversation?.groupID;
    const owner = sessionCoordinator.current, generation = sessionCoordinator.generation;
    if (!sourceID || !owner?.userID || callerCurrent && !callerCurrent()) return;
    const actor = owner.userID, issuer = owner.issuer, tenant = owner.tenant.serverID;
    const initialActor = useUserStore.getState().selfInfo.userID;
    if (initialActor && initialActor !== actor) return;
    const bootstrap = !initialActor;
    const admittedEpoch = epoch.current;
    return { sourceID, actor, current: () => {
      const uiActor = useUserStore.getState().selfInfo.userID;
      return mounted.current && (!trackEpoch || epoch.current === admittedEpoch) && sessionCoordinator.current === owner &&
        sessionCoordinator.generation === generation && owner.userID === actor && owner.issuer === issuer &&
        owner.tenant.serverID === tenant && (groupID ?? useConversationStore.getState().currentConversation?.groupID) === sourceID &&
        (uiActor === actor || bootstrap && !uiActor) && (!callerCurrent || callerCurrent());
    } };
  }, [groupID, callerCurrent]);
  const rowsAccess = useRef<ReturnType<typeof capture>>(undefined);
  const resetState = useCallback(() => {
    ++epoch.current; ++request.current; ++authority.current;
    revisions.current.clear(); selfRemoved.current = undefined; rowsAccess.current = undefined;
    lastKeyword.current = ""; write(emptyState());
  }, []);
  const admit = () => {
    let access = capture();
    if (!access?.current()) return;
    if (!rowsAccess.current?.current() || rowsAccess.current.sourceID !== access.sourceID) {
      resetState(); access = capture();
      if (!access?.current()) return;
      rowsAccess.current = access;
    }
    return access;
  };
  const row = (userID: string) => {
    const access = rowsAccess.current;
    if (!access?.current()) return;
    const list = stateRef.current.groupMemberList.filter((m) => m.userID === userID);
    const search = stateRef.current.searchMemberList.filter((m) => m.userID === userID);
    if (list.length > 1 || search.length > 1 || !list.length && !search.length) return;
    const value = list[0] ?? search[0];
    if (!validMember(value, access.sourceID) || search[0] && !sameFact(value, search[0])) return;
    return value;
  };
  const membershipCurrent = () => {
    const access = rowsAccess.current;
    if (!access?.current()) return false;
    const member = useConversationStore.getState().currentMemberInGroup;
    if (selfRemoved.current) {
      if (!member || member === selfRemoved.current.value || member.groupID !== access.sourceID || member.userID !== access.actor) return false;
      selfRemoved.current = undefined;
    }
    return true;
  };
  const captureMember = (member: GroupMemberItem) => {
    const access = rowsAccess.current, latest = row(member.userID);
    if (!access?.current() || !latest || !sameFact(latest, member) || !membershipCurrent()) return;
    const revision = revisions.current.get(member.userID) ?? 0, selfRevision = authority.current;
    const snapshot = { ...member };
    return { member: snapshot, current: () => {
      const now = row(snapshot.userID);
      return access.current() && membershipCurrent() && authority.current === selfRevision &&
        (revisions.current.get(snapshot.userID) ?? 0) === revision && !!now && sameFact(snapshot, now);
    } };
  };
  const validate = (data: GroupMemberItem[], sourceID: string) => {
    if (!Array.isArray(data) || data.some((m) => !validMember(m, sourceID)) ||
        new Set(data.map((m) => m.userID)).size !== data.length) throw new Error("Invalid group member result");
    return data;
  };
  const observe = (data: GroupMemberItem[]) => {
    for (const value of data) {
      const old = row(value.userID);
      if (old && !sameFact(old, value)) revisions.current.set(value.userID, (revisions.current.get(value.userID) ?? 0) + 1);
    }
  };
  const merge = (old: GroupMemberItem[], data: GroupMemberItem[]) => {
    const values = new Map(old.map((m) => [m.userID, m]));
    data.forEach((m) => values.set(m.userID, m)); return [...values.values()];
  };
  const getMemberData = useCallback(async (refresh = false) => {
    const access = admit(); if (!access) return;
    const initial = stateRef.current;
    if ((initial.loading || !initial.hasMore) && !refresh) return;
    const ticket = ++request.current, offset = refresh ? 0 : initial.offset;
    const valid = () => access.current() && request.current === ticket;
    write({ ...initial, loading: true });
    try {
      const { data } = await IMSDK.getGroupMemberList({ groupID: access.sourceID, offset, count: initial.count, filter: 0 });
      if (!valid()) return;
      validate(data, access.sourceID); observe(data);
      write({ ...stateRef.current, groupMemberList: merge(refresh ? [] : stateRef.current.groupMemberList, data),
        hasMore: data.length === initial.count, offset: offset + data.length, loading: false });
    } catch (error) {
      if (!valid()) return;
      feedbackToast({ msg: "getMemberFailed", error }); write({ ...stateRef.current, loading: false });
    }
  }, [capture]);
  const searchMember = useCallback(async (keyword: string) => {
    const access = admit(); if (!access) return;
    const reach = keyword === REACH_SEARCH_FLAG, initial = stateRef.current;
    if (reach && (initial.loading || !initial.hasMore)) return;
    const actualKeyword = reach ? lastKeyword.current : keyword;
    const ticket = ++request.current, offset = reach ? initial.searchOffset : 0;
    const valid = () => access.current() && request.current === ticket;
    write({ ...initial, loading: true });
    try {
      const { data } = await IMSDK.searchGroupMembers({ groupID: access.sourceID, offset, count: initial.count,
        keywordList: [actualKeyword], isSearchMemberNickname: true, isSearchUserID: true });
      if (!valid()) return; validate(data, access.sourceID);
      let merged = data;
      if (!reach) {
        const upper = actualKeyword.toUpperCase(), ids = new Set(data.map((m) => m.userID));
        const remarks = useContactStore.getState().friendList.filter((friend) => !ids.has(friend.userID) &&
          friend.remark?.toUpperCase().includes(upper)).map((friend) => friend.userID);
        if (remarks.length) {
          try {
            if (!valid()) return;
            const { data: members } = await IMSDK.getSpecifiedGroupMembersInfo({ groupID: access.sourceID, userIDList: remarks });
            if (!valid()) return; validate(members, access.sourceID); merged = merge(data, members);
          } catch { if (!valid()) return; /* A remark may refer to somebody outside this group. */ }
        }
      }
      if (!valid()) return; observe(merged); lastKeyword.current = actualKeyword;
      write({ ...stateRef.current, searchMemberList: merge(reach ? stateRef.current.searchMemberList : [], merged),
        hasMore: data.length === initial.count, searchOffset: offset + data.length, loading: false });
    } catch (error) {
      if (!valid()) return;
      feedbackToast({ msg: "getMemberFailed", error }); write({ ...stateRef.current, loading: false });
    }
  }, [capture]);
  useEffect(() => {
    mounted.current = true;
    const access = capture(false);
    const changed = ({ data: member }: WSEvent<GroupMemberItem>) => {
      if (!access?.current() || !validMember(member, access.sourceID)) return;
      const known = [...stateRef.current.groupMemberList, ...stateRef.current.searchMemberList]
        .some((item) => item.groupID === access.sourceID && item.userID === member.userID);
      if (member.userID === access.actor) ++authority.current;
      ++request.current;
      if (!known) {
        // No array[-1] insertion, but an older pending page must not turn this
        // newer (possibly promoted) member back into an actionable old role.
        write({ ...stateRef.current, loading: false }); return;
      }
      revisions.current.set(member.userID, (revisions.current.get(member.userID) ?? 0) + 1);
      const replace = (list: GroupMemberItem[]) => list.map((m) => m.userID === member.userID ? { ...member } : m);
      write({ ...stateRef.current, groupMemberList: replace(stateRef.current.groupMemberList),
        searchMemberList: replace(stateRef.current.searchMemberList), loading: false });
    };
    const counted = ({ data }: WSEvent<GroupMemberItem>, removed: boolean) => {
      if (!access?.current() || data?.groupID !== access.sourceID || typeof data.userID !== "string" || !data.userID) return;
      if (removed) {
        ++request.current;
        revisions.current.set(data.userID, (revisions.current.get(data.userID) ?? 0) + 1);
        if (data.userID === access.actor) {
          ++authority.current;
          selfRemoved.current = { value: useConversationStore.getState().currentMemberInGroup };
        }
        const keep = (list: GroupMemberItem[]) => list.filter((m) => m.userID !== data.userID);
        write({ ...stateRef.current, groupMemberList: keep(stateRef.current.groupMemberList),
          searchMemberList: keep(stateRef.current.searchMemberList), loading: false });
      }
      if (!notRefresh) void getMemberData(true);
    };
    const added = (event: WSEvent<GroupMemberItem>) => counted(event, false);
    const deleted = (event: WSEvent<GroupMemberItem>) => counted(event, true);
    // This event carries GroupItem, not GroupMemberItem; it has no member UID.
    const joined = ({ data }: WSEvent<{ groupID: string }>) => {
      if (access?.current() && data?.groupID === access.sourceID && !notRefresh) void getMemberData(true);
    };
    IMSDK.on(CbEvents.OnGroupMemberInfoChanged, changed);
    IMSDK.on(CbEvents.OnGroupMemberAdded, added);
    IMSDK.on(CbEvents.OnGroupMemberDeleted, deleted);
    IMSDK.on(CbEvents.OnJoinedGroupAdded, joined);
    return () => {
      mounted.current = false; ++epoch.current; ++request.current;
      IMSDK.off(CbEvents.OnGroupMemberInfoChanged, changed);
      IMSDK.off(CbEvents.OnGroupMemberAdded, added);
      IMSDK.off(CbEvents.OnGroupMemberDeleted, deleted);
      IMSDK.off(CbEvents.OnJoinedGroupAdded, joined);
    };
  }, [capture, notRefresh, getMemberData]);

  return { fetchState, getMemberData, searchMember, resetState, captureMember };
}
