import { ArrowLeftOutlined, CheckOutlined, CloseOutlined } from "@ant-design/icons";
import { useRequest } from "ahooks";
import { Empty, Modal, Spin, Tooltip } from "antd";
import clsx from "clsx";
import { t } from "i18next";
import { AllowType, GroupMemberRole } from "open-im-sdk-wasm";
import { GroupMemberItem } from "open-im-sdk-wasm/lib/types/entity";
import {
  FC,
  forwardRef,
  ForwardRefRenderFunction,
  memo,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react";
import { Virtuoso } from "react-virtuoso";

import { modal } from "@/AntdGlobalComp";
import { sessionCoordinator } from "@/core/session/browser";
import type { ConversationSendScope } from "@/core/messageSendIntent";
import member_admin from "@/assets/images/chatSetting/member_admin.png";
import member_admin_active from "@/assets/images/chatSetting/member_admin_active.png";
import member_delete from "@/assets/images/chatSetting/member_delete.png";
import member_mute from "@/assets/images/chatSetting/member_mute.png";
import member_mute_active from "@/assets/images/chatSetting/member_mute_active.png";
import OIMAvatar from "@/components/OIMAvatar";
import { useCurrentMemberRole } from "@/hooks/useCurrentMemberRole";
import useGroupMembers, { REACH_SEARCH_FLAG } from "@/hooks/useGroupMembers";
import { OverlayVisibleHandle, useOverlayVisible } from "@/hooks/useOverlayVisible";
import { IMSDK } from "@/layout/MainContentWrap";
import { useConversationStore, useUserStore } from "@/store";
import { useContactStore } from "@/store/contact";
import { feedbackToast } from "@/utils/common";

import styles from "./group-setting.module.scss";

export interface GroupMemberListHandle {
  searchMember: (keyword: string) => void;
}

interface IGroupMemberListProps {
  isSearching: boolean;
  isActive?: () => boolean;
}

type MemberIntent = {
  member: GroupMemberItem;
  current: () => boolean;
  viewCurrent: () => boolean;
};

const GroupMemberList: ForwardRefRenderFunction<
  GroupMemberListHandle,
  IGroupMemberListProps
> = ({ isSearching, isActive }, ref) => {
  const selfUserID = useUserStore((state) => state.selfInfo.userID);
  const conversation = useConversationStore((state) => state.currentConversation);
  const { isAdmin, isOwner } = useCurrentMemberRole();
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const owner = sessionCoordinator.current, generation = sessionCoordinator.generation;
  const issuer = owner?.issuer, tenant = owner?.tenant.serverID, actor = owner?.userID;
  const scopeRef = useRef<ConversationSendScope | undefined>(undefined);
  if (!scopeRef.current?.current()) scopeRef.current = useConversationStore.getState().captureConversationSendScope();
  const scope = scopeRef.current;
  const lease = useMemo(() => {
    const active = isActive;
    const current = () => Boolean(mounted.current && (!active || active()) && scope?.current() &&
      scope.target.kind === "group" && scope.target.id === conversation?.groupID && owner &&
      sessionCoordinator.current === owner && sessionCoordinator.generation === generation &&
      owner.userID === actor && owner.issuer === issuer && owner.tenant.serverID === tenant &&
      useUserStore.getState().selfInfo.userID === actor);
    return { groupID: scope?.target.kind === "group" ? scope.target.id : "", current };
  }, [scope, owner, generation, actor, issuer, tenant, selfUserID, conversation?.conversationID, conversation?.groupID, isActive]);
  const { fetchState, getMemberData, searchMember, resetState, captureMember } = useGroupMembers({
    groupID: lease.groupID, current: lease.current,
  });
  const muteModalRef = useRef<OverlayVisibleHandle>(null);
  const [muteIntent, setMuteIntent] = useState<MemberIntent>();
  const latestMute = useRef<MemberIntent | undefined>(undefined);
  const busy = useMemo(() => new Set<string>(), [lease]);
  const [busyIDs, setBusyIDs] = useState<string[]>([]);

  useEffect(() => {
    latestMute.current = undefined; setMuteIntent(undefined); setBusyIDs([]);
    if (lease.current()) void getMemberData(true);
    return () => { latestMute.current = undefined; resetState(); };
  }, [lease]);
  useImperativeHandle(ref, () => ({ searchMember: (keyword) => { if (lease.current()) return searchMember(keyword); } }));
  const endReached = () => {
    if (!lease.current()) return;
    if (!isSearching) void getMemberData(); else void searchMember(REACH_SEARCH_FLAG);
  };
  const captureIntent = (member: GroupMemberItem, action: "mute" | "admin" | "kick" | "profile"): MemberIntent | undefined => {
    const access = captureMember(member);
    if (!lease.current() || !access) return;
    const permitted = () => {
      const state = useConversationStore.getState(), self = state.currentMemberInGroup, group = state.currentGroupInfo;
      if (!lease.current() || !access.current() || !self || self.groupID !== lease.groupID || self.userID !== actor ||
          ![20, 60, 100].includes(self.roleLevel) || group?.groupID !== lease.groupID || group.status === 2) return false;
      if (action === "profile") return group.lookMemberInfo !== AllowType.NotAllowed;
      if (access.member.userID === actor) return false;
      if (action === "admin") return self.roleLevel === GroupMemberRole.Owner;
      return self.roleLevel === GroupMemberRole.Owner ||
        self.roleLevel === GroupMemberRole.Admin && access.member.roleLevel === GroupMemberRole.Nomal;
    };
    if (!permitted()) return;
    return { member: { ...access.member }, current: permitted, viewCurrent: lease.current };
  };
  const perform = async (intent: MemberIntent, task: () => Promise<unknown>) => {
    const key = intent.member.userID;
    if (!intent.current() || busy.has(key)) return false;
    busy.add(key); setBusyIDs([...busy]);
    try {
      // No await between the final intent/authority check and SDK dispatch.
      if (!intent.current()) return false;
      await task(); return intent.viewCurrent();
    } catch (error) {
      if (intent.viewCurrent()) feedbackToast({ error }); return false;
    } finally {
      busy.delete(key);
      if (intent.viewCurrent()) setBusyIDs([...busy]);
    }
  };
  const tryUpdateMute = (member: GroupMemberItem) => {
    const intent = captureIntent(member, "mute"); if (!intent) return;
    latestMute.current = intent; setMuteIntent(intent); muteModalRef.current?.openOverlay();
  };
  const updateMuteState = (intent: MemberIntent, mutedSeconds: number) => {
    if (latestMute.current !== intent) return Promise.resolve(false);
    return perform(intent, () => IMSDK.changeGroupMemberMute({ groupID: intent.member.groupID,
      userID: intent.member.userID, mutedSeconds }));
  };
  const closeMute = (intent?: MemberIntent) => {
    if (latestMute.current !== intent) return;
    latestMute.current = undefined; setMuteIntent(undefined); muteModalRef.current?.closeOverlay();
  };
  const tryKick = (member: GroupMemberItem) => {
    const intent = captureIntent(member, "kick"); if (!intent) return;
    modal.confirm({ title: t("placeholder.kickMember"), content: t("toast.confirmkickMember", { name: member.nickname }),
      onOk: () => perform(intent, () => IMSDK.kickGroupMember({ groupID: intent.member.groupID,
        userIDList: [intent.member.userID], reason: "" })) });
  };
  const adminChange = (member: GroupMemberItem) => {
    const intent = captureIntent(member, "admin"); if (!intent) return;
    return perform(intent, () => IMSDK.setGroupMemberInfo({ groupID: intent.member.groupID, userID: intent.member.userID,
      roleLevel: intent.member.roleLevel === GroupMemberRole.Admin ? GroupMemberRole.Nomal : GroupMemberRole.Admin }));
  };
  const showProfile = (member: GroupMemberItem) => {
    const intent = captureIntent(member, "profile");
    if (intent?.current()) window.userClick(intent.member.userID, intent.member.groupID);
  };

  const dataSource = isSearching
    ? fetchState.searchMemberList
    : fetchState.groupMemberList;

  return (
    <div className="h-full px-2 py-2.5">
      {isSearching && dataSource.length === 0 ? (
        <Empty
          className="flex h-full flex-col items-center justify-center"
          description={t("empty.noSearchResults")}
        />
      ) : (
        <Virtuoso
          className="h-full overflow-x-hidden"
          data={dataSource}
          endReached={endReached}
          components={{
            Header: () => (fetchState.loading ? <div>loading...</div> : null),
          }}
          itemContent={(_, member) => (
            <MemberItem
              member={member}
              selfUserID={selfUserID}
              currentIsAdmin={isAdmin}
              currentIsOwner={isOwner}
              tryUpdateMute={tryUpdateMute}
              tryKick={tryKick}
              adminChange={adminChange}
              showProfile={showProfile}
              loading={busyIDs.includes(member.userID)}
            />
          )}
        />
      )}
      <ForwardMuteModal ref={muteModalRef} intent={muteIntent} updateMuteState={updateMuteState} closeIntent={closeMute} />
    </div>
  );
};

export default forwardRef(GroupMemberList);

interface IMemberItemProps {
  member: GroupMemberItem;
  selfUserID: string;
  currentIsOwner: boolean;
  currentIsAdmin: boolean;
  tryUpdateMute: (member: GroupMemberItem) => void;
  tryKick: (member: GroupMemberItem) => void;
  adminChange: (member: GroupMemberItem) => unknown;
  showProfile: (member: GroupMemberItem) => void;
  loading: boolean;
}

const MemberItem = memo(
  ({
    member,
    selfUserID,
    currentIsOwner,
    currentIsAdmin,
    tryUpdateMute, tryKick, adminChange, showProfile, loading,
  }: IMemberItemProps) => {
    const isOwner = member.roleLevel === GroupMemberRole.Owner;
    const isAdmin = member.roleLevel === GroupMemberRole.Admin;
    const [muteClock, refreshMuteClock] = useState(0);
    const isMuted = member.muteEndTime > Date.now();

    useEffect(() => {
      const remaining = member.muteEndTime - Date.now();
      if (!Number.isFinite(remaining) || remaining <= 0) return;
      // SDK/server timestamps are Unix milliseconds. Refresh the mounted row
      // at expiry; long deadlines are re-armed within the browser timer limit.
      const timer = window.setTimeout(
        () => refreshMuteClock((clock) => clock + 1),
        Math.min(remaining, 2_147_483_647),
      );
      return () => window.clearTimeout(timer);
    }, [member.muteEndTime, muteClock]);
    const friend = useContactStore((state) =>
      state.friendList.find((f) => f.userID === member.userID),
    );
    const displayName = friend?.remark || member.nickname;

    const getShowTools = () => {
      if (currentIsOwner) {
        return selfUserID !== member.userID;
      }
      if (currentIsAdmin) {
        return !isOwner && !isAdmin;
      }
      return false;
    };

    return (
      <Spin spinning={loading}>
        <div className={styles["list-member-item"]}>
          <div
            className="flex items-center overflow-hidden"
            onClick={() => showProfile(member)}
          >
            <OIMAvatar src={member.faceURL} text={displayName} />
            <div className="ml-3 flex items-center">
              <div className="max-w-[120px] truncate">{displayName}</div>
              {isOwner && (
                <span className="ml-2 rounded border border-[#FF9831] px-1 text-xs text-[#FF9831]">
                  {t("placeholder.groupOwner")}
                </span>
              )}
              {isAdmin && (
                <span className="ml-2 rounded border border-[#0289FA] px-1 text-xs text-[#0289FA]">
                  {t("placeholder.administrator")}
                </span>
              )}
            </div>
          </div>
          {getShowTools() && (
            <div className={styles["tools-row"]}>
              <Tooltip title={t("placeholder.mute")}>
                <div
                  className="w-[60px] cursor-pointer"
                  onClick={() => tryUpdateMute(member)}
                >
                  <img
                    width={60}
                    src={isMuted ? member_mute_active : member_mute}
                    alt=""
                  />
                </div>
              </Tooltip>
              {currentIsOwner && (
                <Tooltip title={t("placeholder.setAdministrator")}>
                  <div className="w-[60px] cursor-pointer" onClick={() => adminChange(member)}>
                    <img
                      width={60}
                      src={isAdmin ? member_admin_active : member_admin}
                      alt=""
                    />
                  </div>
                </Tooltip>
              )}
              <Tooltip title={t("placeholder.kick")}>
                <div className="w-[60px] cursor-pointer" onClick={() => tryKick(member)}>
                  <img width={60} src={member_delete} alt="" />
                </div>
              </Tooltip>
            </div>
          )}
        </div>
      </Spin>
    );
  },
);

const MuteOptions = [
  {
    title: t("date.minute", { num: 10 }),
    value: 10 * 60,
  },
  {
    title: t("date.hour", { num: 1 }),
    value: 60 * 60,
  },
  {
    title: t("date.hour", { num: 12 }),
    value: 12 * 60 * 60,
  },
  {
    title: t("date.day", { num: 1 }),
    value: 24 * 60 * 60,
  },
  {
    title: t("placeholder.cancelMute"),
    value: 0,
  },
];

const MuteModal: ForwardRefRenderFunction<
  OverlayVisibleHandle,
  {
    intent?: MemberIntent;
    updateMuteState: (intent: MemberIntent, muteTime: number) => Promise<boolean>;
    closeIntent: (intent?: MemberIntent) => void;
  }
> = ({ updateMuteState, intent, closeIntent }, ref) => {
  const [selectedOption, setSelectedOption] = useState<number>();
  const { isOverlayOpen } = useOverlayVisible(ref);

  const { loading, runAsync } = useRequest(updateMuteState, { manual: true });
  useEffect(() => { setSelectedOption(undefined); }, [intent]);
  const cancel = () => { closeIntent(intent); };

  const saveMute = async () => {
    if (selectedOption === undefined || !intent?.current()) {
      return;
    }
    const applied = await runAsync(intent, selectedOption);
    if (applied && intent.viewCurrent()) closeIntent(intent);
  };

  return (
    <Modal
      title={null}
      footer={null}
      closable={false}
      open={isOverlayOpen}
      destroyOnClose
      centered
      onCancel={cancel}
      width={320}
      className="no-padding-modal"
    >
      <Spin spinning={loading}>
        <div className="py-6">
          <div className="flex items-center justify-between px-5">
            <ArrowLeftOutlined
              className="cursor-pointer !text-[#8e9aaf]"
              onClick={cancel}
            />
            <div>{t("placeholder.setMute")}</div>
            <span className="cursor-pointer text-[var(--primary)]" onClick={saveMute}>
              {t("placeholder.save")}
            </span>
          </div>
          <div className="mt-5">
            {MuteOptions.map((option) => (
              <div
                key={option.value}
                className={clsx(
                  "flex cursor-pointer items-center justify-between px-6 py-4 hover:bg-[var(--primary-active)]",
                  { "bg-[var(--primary-active)]": selectedOption === option.value },
                )}
                onClick={() => setSelectedOption(option.value)}
              >
                <span>{option.title}</span>
                {selectedOption === option.value && (
                  <CheckOutlined className="!text-[var(--primary)]" />
                )}
              </div>
            ))}
          </div>
        </div>
      </Spin>
    </Modal>
  );
};

const ForwardMuteModal = forwardRef(MuteModal);
