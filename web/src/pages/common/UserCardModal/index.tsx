import { Button, Divider, Modal, Spin } from "antd";
import dayjs from "dayjs";
import { t } from "i18next";
import { CbEvents } from "open-im-sdk-wasm";
import { GroupJoinSource, SessionType } from "open-im-sdk-wasm";
import {
  FriendUserItem,
  GroupMemberItem,
  WSEvent,
} from "open-im-sdk-wasm/lib/types/entity";
import {
  FC,
  forwardRef,
  ForwardRefRenderFunction,
  memo,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react";
import { useCopyToClipboard } from "react-use";

import { message } from "@/AntdGlobalComp";
import { BusinessAllowType, BusinessUserInfo, getBusinessUserInfo } from "@/api/login";
import DraggableModalWrap from "@/components/DraggableModalWrap";
import EditableContent from "@/components/EditableContent";
import OIMAvatar from "@/components/OIMAvatar";
import UserLoginInfo from "@/components/UserLoginInfo";
import UserLoginRecordList from "@/components/UserLoginRecordList";
import { loadUserCard } from "@/core/loadUserCard";
import { sessionCoordinator } from "@/core/session/browser";
import { useConversationToggle } from "@/hooks/useConversationToggle";
import { OverlayVisibleHandle, useOverlayVisible } from "@/hooks/useOverlayVisible";
import { IMSDK } from "@/layout/MainContentWrap";
import { useUserStore } from "@/store";
import { feedbackToast } from "@/utils/common";

import CardActionRow from "./CardActionRow";
import EditSelfInfo from "./EditSelfInfo";
import SendRequest from "./SendRequest";

interface IUserCardModalProps {
  userID?: string;
  groupID?: string;
  isSelf?: boolean;
  notAdd?: boolean;
  cardInfo?: CardInfo;
  sourceCurrent?: () => boolean;
  sourceCanShare?: () => boolean;
  sourceSubscribe?: (listener: () => void) => () => void;
}

export type CardInfo = Partial<BusinessUserInfo & FriendUserItem>;

const getGender = (gender: number) => {
  if (!gender) return "-";
  return gender === 1 ? t("placeholder.man") : t("placeholder.female");
};

const UserCardModal: ForwardRefRenderFunction<
  OverlayVisibleHandle,
  IUserCardModalProps
> = (props, ref) => {
  const { userID, groupID, isSelf, notAdd, sourceCurrent, sourceCanShare, sourceSubscribe } = props;

  const editInfoRef = useRef<OverlayVisibleHandle>(null);
  const [cardInfo, setCardInfo] = useState<CardInfo>();
  const [isFetching, setIsFetching] = useState(false);
  const [loadError, setLoadError] = useState(false);
  const [ready, setReady] = useState<{ current: () => boolean }>();
  const [, setRetentionStatus] = useState<boolean>();
  const requestEpoch = useRef(0);
  const [isSendRequest, setIsSendRequest] = useState(false);
  const [userFields, setUserFields] = useState<FieldRow[]>([]);
  const [gorupMemberFields, setGorupMemberFields] = useState<FieldRow[]>([]);
  const [showLoginRecords, setShowLoginRecords] = useState(false);
  const allowSendMsgNotFriend = useUserStore(
    (state) =>
      Number(state.appConfig.allowSendMsgNotFriend) === BusinessAllowType.Allow,
  );

  const selfInfo = useUserStore((state) => state.selfInfo);

  const visibilityRef = useRef<OverlayVisibleHandle>(null);
  const { isOverlayOpen, closeOverlay: hideOverlay, openOverlay } = useOverlayVisible(visibilityRef);
  const target = isSelf ? selfInfo.userID : userID ?? props.cardInfo?.userID;
  const owner = sessionCoordinator.current;
  const generation = sessionCoordinator.generation;
  const live = useRef({ target, groupID, isSelf, owner, generation, sourceCurrent, sourceCanShare, open: isOverlayOpen });
  live.current = { target, groupID, isSelf, owner, generation, sourceCurrent, sourceCanShare, open: isOverlayOpen };
  const closeOverlay = useCallback(() => {
    ++requestEpoch.current;
    live.current.open = false;
    hideOverlay();
  }, [hideOverlay]);
  useImperativeHandle(ref, () => ({ isOverlayOpen, openOverlay, closeOverlay }));
  const capture = () => {
    const snapshot = live.current;
    const epoch = ++requestEpoch.current;
    return { current: () => Boolean(snapshot.owner && snapshot.open && live.current.open &&
      epoch === requestEpoch.current && live.current.sourceCurrent === snapshot.sourceCurrent &&
      (!snapshot.sourceCurrent || snapshot.sourceCurrent()) && live.current.target === snapshot.target &&
      live.current.groupID === snapshot.groupID && live.current.isSelf === snapshot.isSelf &&
      sessionCoordinator.current === snapshot.owner && sessionCoordinator.generation === snapshot.generation) };
  };
  const actionCurrent = () => Boolean(ready?.current());
  const retentionCurrent = () => !live.current.sourceCanShare || live.current.sourceCanShare();
  const copyCurrent = () => actionCurrent() && retentionCurrent() && Boolean(cardInfo?.userID);
  const retentionRestricted = Boolean(sourceCanShare && !sourceCanShare());
  const { toSpecifiedConversation } = useConversationToggle();
  const [_, copyToClipboard] = useCopyToClipboard();

  useEffect(() => sessionCoordinator.onChange(closeOverlay), [closeOverlay]);
  // Optional message provenance stays live through hydration AND an open panel.
  // Normal contact/search/self cards have no source callback and keep their behavior.
  useEffect(() => {
    if (!isOverlayOpen || (!sourceCurrent && !sourceCanShare)) return;
    const check = () => {
      if (sourceCurrent && !sourceCurrent()) closeOverlay();
      // Rendering feedback only. The copy dispatch always checks the live capability.
      else setRetentionStatus(retentionCurrent());
    };
    check();
    const unsubscribe = sourceSubscribe?.(check);
    const timer = setInterval(check, 250);
    document.addEventListener("visibilitychange", check);
    return () => { unsubscribe?.(); clearInterval(timer); document.removeEventListener("visibilitychange", check); };
  }, [isOverlayOpen, sourceCurrent, sourceCanShare, sourceSubscribe, closeOverlay]);
  useEffect(() => {
    if (!isOverlayOpen) { resetState(); return; }
    const friendAddedHandler = ({ data }: WSEvent<FriendUserItem>) => {
      if (data.userID === target) void refreshData();
    };
    IMSDK.on(CbEvents.OnFriendAdded, friendAddedHandler);
    void refreshData();
    return () => {
      ++requestEpoch.current;
      IMSDK.off(CbEvents.OnFriendAdded, friendAddedHandler);
    };
  }, [isOverlayOpen, target, groupID, isSelf, owner, generation, sourceCurrent]);

  const refreshSelfInfo = useCallback(() => {
    if (!isSelf || !actionCurrent()) return;
    const latestInfo = useUserStore.getState().selfInfo;
    if (latestInfo.userID !== target) return;
    setCardInfo(latestInfo);
    setUserInfoRow(latestInfo);
  }, [isSelf, target, ready]);

  const refreshData = async () => {
    const access = capture();
    if (!access.current()) return;
    setReady(undefined);
    setCardInfo(undefined);
    setUserFields([]);
    setGorupMemberFields([]);
    setIsSendRequest(false);
    setShowLoginRecords(false);
    setLoadError(false);
    setIsFetching(true);
    try {
      let card: CardInfo;
      if (isSelf) {
        if (!target || selfInfo.userID !== target) throw new Error("Missing self identity");
        card = selfInfo;
      } else {
        const result = await loadUserCard(target ?? "", groupID, {
          sdk: async () => (await IMSDK.getUsersInfoWithCache({ userIDList: [target!], groupID })).data,
          business: async () => (await getBusinessUserInfo([target!])).data.users,
          current: access.current,
        });
        if (result.groupMember) await setGroupMemberInfoRow(result.groupMember, access.current);
        card = result.card;
      }
      if (!access.current()) return;
      setCardInfo(card);
      setUserInfoRow(card);
      setReady(access);
    } catch {
      // Never treat an unknown business policy as permission; dependency details
      // and data for a superseded owner do not belong in feedback or logs.
      if (access.current()) setLoadError(true);
    } finally {
      if (access.current()) setIsFetching(false);
    }
  };

  const updateCardRemark = (remark: string) => {
    if (!actionCurrent()) return;
    const next = { ...cardInfo!, remark };
    setCardInfo(next);
    setUserInfoRow(next);
  };

  const setUserInfoRow = (info: CardInfo) => {
    let tmpFields = [] as FieldRow[];
    tmpFields.push({
      title: t("placeholder.nickName"),
      value: info.nickname || "",
    });
    const isFriend = info?.remark !== undefined;

    if (isFriend) {
      tmpFields.push({
        title: t("placeholder.remark"),
        value: info.remark || "-",
        editable: true,
      });
    }
    if (isFriend || isSelf) {
      tmpFields = [
        ...tmpFields,
        ...[
          {
            title: t("placeholder.gender"),
            value: getGender(info.gender!),
          },
          {
            title: t("placeholder.birth"),
            value: info.birth ? dayjs(info.birth).format("YYYY/M/D") : "-",
          },
          ...(isSelf
            ? [
                {
                  title: t("placeholder.phoneNumber"),
                  value: info.phoneNumber || "-",
                },
                {
                  title: t("placeholder.email"),
                  value: info.email || "-",
                },
              ]
            : []),
        ],
      ];
    }
    
    // 添加用户角色字段（如果存在）
    if (info.userRole) {
      tmpFields.push({
        title: "用户角色",
        value: info.userRole,
      });
    }
    
    setUserFields(tmpFields);
  };

  const setGroupMemberInfoRow = async (info: GroupMemberItem, current: () => boolean) => {
    if (!current()) return;
    let joinSourceStr = "-";
    if (info.joinSource === GroupJoinSource.Invitation) {
      const { data } = await IMSDK.getSpecifiedGroupMembersInfo({
        groupID: groupID!,
        userIDList: [info.inviterUserID],
      });
      if (!current()) return;
      const inviterInfo = data.find(item => item.userID === info.inviterUserID);
      joinSourceStr = `${inviterInfo?.nickname ?? ""}${t("placeholder.inviteToGroup")}`;
    } else {
      joinSourceStr =
        info.joinSource === GroupJoinSource.QrCode
          ? t("placeholder.qrCodeToGroup")
          : t("placeholder.selectIDToGroup");
    }
    if (!current()) return;
    setGorupMemberFields([
      {
        title: t("placeholder.groupNickName"),
        value: info.nickname,
      },
      {
        title: t("placeholder.joinGroupTime"),
        value: dayjs(info.joinTime).format("YYYY/M/D"),
      },
      {
        title: t("placeholder.joinGroupMode"),
        value: joinSourceStr,
      },
    ]);
  };

  const backToCard = () => {
    if (!actionCurrent()) return;
    setIsSendRequest(false);
  };

  const trySendRequest = () => {
    if (!actionCurrent()) return;
    if (cardInfo?.allowAddFriend !== BusinessAllowType.Allow) {
      message.warning(t("toast.notCanAddFriend"));
      return;
    }
    setIsSendRequest(true);
  };

  const resetState = () => {
    setReady(undefined);
    setLoadError(false);
    setIsFetching(false);
    setShowLoginRecords(false);
    setCardInfo(undefined);
    setUserFields([]);
    setGorupMemberFields([]);
    setIsSendRequest(false);
  };

  const isFriend = cardInfo?.remark !== undefined;
  const showAddFriend = actionCurrent() && !isFriend && !isSelf && !notAdd;
  const showSendMessage = actionCurrent() && !isSelf && (isFriend || allowSendMsgNotFriend);

  return (
    <DraggableModalWrap
      title={null}
      footer={null}
      open={isOverlayOpen && (!sourceCurrent || sourceCurrent())}
      closable={false}
      width={332}
      centered
      onCancel={closeOverlay}
      destroyOnClose
      styles={{
        mask: {
          opacity: 0,
          transition: "none",
        },
      }}
      afterClose={resetState}
      ignoreClasses=".ignore-drag, .no-padding-modal, .cursor-pointer"
      className="no-padding-modal"
      maskTransitionName=""
    >
      <Spin spinning={isFetching}>
        {loadError ? (
          <div role="alert" className="space-y-4 p-6">
            <div>{t("toast.getUserInfoFailed")}</div>
            <Button onClick={() => void refreshData()}>{t("retry")}</Button>
          </div>
        ) : isSendRequest && actionCurrent() ? (
          <SendRequest cardInfo={cardInfo!} backToCard={backToCard} isCurrent={actionCurrent} />
        ) : (
          <div className={`flex max-h-[520px] min-h-[484px] flex-col overflow-hidden bg-[url(@/assets/images/common/card_bg.png)] bg-[length:332px_134px] bg-no-repeat px-5.5 ${retentionRestricted ? "select-none" : ""}`}
            style={retentionRestricted ? { userSelect: "none", WebkitUserSelect: "none" } : undefined}>
            <div className="h-[104px] min-h-[104px] w-full cursor-move" />
            <div className="ignore-drag flex flex-1 flex-col overflow-hidden">
              <div className="flex items-center">
                <OIMAvatar
                  size={60}
                  src={cardInfo?.faceURL}
                  text={cardInfo?.nickname}
                />
                <div className="ml-3 flex min-h-[80px] flex-1 flex-col justify-around overflow-hidden">
                  <div className="flex w-fit max-w-[80%] items-baseline">
                    <div
                      className={`flex-1 ${retentionRestricted ? "select-none" : "select-text"} truncate text-base font-medium text-white`}
                      title={cardInfo?.nickname}
                    >
                      {cardInfo?.nickname}
                    </div>
                    {/* <div className="ml-3 text-xs text-white">{t("placeholder.online")}</div> */}
                  </div>
                  <div className="flex items-center">
                    <button
                      type="button"
                      aria-label={`${t("placeholder.copy")} ID: ${cardInfo?.userID ?? ""}`}
                      className="mr-3 min-w-0 flex-1 truncate rounded-md text-left text-xs text-[var(--sub-text)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--primary)] disabled:opacity-40"
                      style={{ minWidth: 44, minHeight: 44 }}
                      disabled={!copyCurrent()}
                      onClick={() => {
                        if (!copyCurrent()) return;
                        copyToClipboard(cardInfo!.userID!);
                        feedbackToast({ msg: t("toast.copySuccess") });
                      }}
                    >
                      {cardInfo?.userID}
                    </button>
                    {actionCurrent() && <CardActionRow
                      cardInfo={cardInfo}
                      isFriend={isFriend}
                      isSelf={isSelf}
                      closeOverlay={closeOverlay}
                      isCurrent={actionCurrent}
                      sourceCurrent={sourceCurrent}
                      sourceCanShare={sourceCanShare}
                    />}
                  </div>
                </div>
              </div>
              <div className="flex-1 overflow-y-auto">
                {Boolean(groupID) && Boolean(gorupMemberFields.length) && (
                  <UserCardDataGroup
                    title={t("placeholder.groupInfo")}
                    fieldRows={gorupMemberFields}
                    selectable={!retentionRestricted}
                  />
                )}
                <UserCardDataGroup
                  title={t("placeholder.personalInfo")}
                  userID={cardInfo?.userID}
                  fieldRows={userFields}
                  updateCardRemark={updateCardRemark}
                  isCurrent={actionCurrent}
                  selectable={!retentionRestricted}
                />
                
                {/* 只有高级用户才能看到登录日志 */}
                {cardInfo?.userRole === "高级用户" && cardInfo?.userID && (
                  <div className="my-4">
                    <div className="mb-2 flex items-center justify-between">
                      <div className="text-[var(--sub-text)]">登录信息</div>
                      <Button 
                        type="link" 
                        size="small"
                        onClick={() => { if (actionCurrent()) setShowLoginRecords(true); }}
                      >
                        查看完整记录
                      </Button>
                    </div>
                    <UserLoginInfo userID={cardInfo.userID} />
                    <Divider className="my-0 border-[var(--gap-text)]" />
                  </div>
                )}
              </div>
            </div>
            <div className="mx-1 mb-6 mt-3 flex items-center gap-6">
              {showAddFriend && (
                <Button type="primary" className="flex-1" onClick={trySendRequest}>
                  {t("placeholder.addFriends")}
                </Button>
              )}
              {isSelf && actionCurrent() && (
                <Button
                  type="primary"
                  className="flex-1"
                  onClick={() => editInfoRef.current?.openOverlay()}
                >
                  {t("placeholder.editInfo")}
                </Button>
              )}
              {showSendMessage && (
                <Button
                  type="primary"
                  className="flex-1"
                  onClick={async () => {
                    if (!actionCurrent()) return;
                    const access = ready!;
                    const opened = await toSpecifiedConversation({ sourceID: target!, sessionType: SessionType.Single }, undefined, access.current);
                    if (opened && access.current()) closeOverlay();
                  }}
                >
                  {t("placeholder.sendMessage")}
                </Button>
              )}
            </div>
          </div>
        )}
      </Spin>
      <EditSelfInfo ref={editInfoRef} refreshSelfInfo={refreshSelfInfo} />
      
      {/* 登录记录弹窗 */}
      <Modal
        title="登录记录"
        open={showLoginRecords}
        onCancel={() => setShowLoginRecords(false)}
        footer={null}
        width={600}
        centered
      >
        {cardInfo?.userID && <UserLoginRecordList userID={cardInfo.userID} />}
      </Modal>
    </DraggableModalWrap>
  );
};

export default memo(forwardRef(UserCardModal));

interface IUserCardDataGroupProps {
  title: string;
  userID?: string;
  fieldRows: FieldRow[];
  updateCardRemark?: (remark: string) => void;
  isCurrent?: () => boolean;
  selectable?: boolean;
}

type FieldRow = {
  title: string;
  value: string;
  editable?: boolean;
};

const UserCardDataGroup: FC<IUserCardDataGroupProps> = ({
  title,
  userID,
  fieldRows,
  updateCardRemark,
  isCurrent = () => false,
  selectable = true,
}) => {
  const tryUpdateRemark = async (remark: string) => {
    if (!isCurrent()) return false;
    try {
      await IMSDK.setFriendRemark({
        toUserID: userID!,
        remark,
      });
      if (!isCurrent()) return false;
      updateCardRemark?.(remark); return true;
    } catch (error) {
      if (isCurrent()) feedbackToast({ error });
      return false;
    }
  };
  return (
    <div>
      <div className="my-4 text-[var(--sub-text)]">{title}</div>
      {fieldRows.map((fieldRow, idx) => (
        <div className="my-4 flex items-center text-xs" key={idx}>
          <div className="w-24 text-[var(--sub-text)]">{fieldRow.title}</div>
          {fieldRow.editable ? (
            <EditableContent
              className="!ml-0"
              textClassName={`font-medium ${selectable ? "" : "select-none"}`}
              value={fieldRow.value}
              editable={true}
              onChange={tryUpdateRemark}
              editIdentity={JSON.stringify([userID, "remark"])}
              captureEdit={() => ({ current: isCurrent })}
            />
          ) : (
            <div className={`flex-1 ${selectable ? "select-text" : "select-none"} truncate`}>{fieldRow.value}</div>
          )}
        </div>
      ))}

      <Divider className="my-0 border-[var(--gap-text)]" />
    </div>
  );
};
