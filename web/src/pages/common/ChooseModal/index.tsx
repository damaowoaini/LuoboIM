import { selectedSendTarget, type SendOrigin, type SendIntent } from "@/core/messageSendIntent";
import { captureMessageRetention, captureMessageAccess, messageOwner } from "@/core/messagePrivacyRuntime";
import { CloseOutlined } from "@ant-design/icons";
import { Button, Input, Modal, Upload } from "antd";
import i18n, { t } from "i18next";
import { GroupType, SessionType } from "open-im-sdk-wasm";
import { CardElem } from "open-im-sdk-wasm/lib/types/entity";
import { CustomMsgParams, MergerMsgParams } from "open-im-sdk-wasm/lib/types/params";
import {
  forwardRef,
  ForwardRefRenderFunction,
  memo,
  useEffect,
  useRef,
  useState,
} from "react";
import { v4 as uuidV4 } from "uuid";

import { message } from "@/AntdGlobalComp";
import { deliverMessageBatch } from "@/core/messageBatch";
import { sessionCoordinator } from "@/core/session/browser";
import OIMAvatar from "@/components/OIMAvatar";
import { OverlayVisibleHandle, useOverlayVisible } from "@/hooks/useOverlayVisible";
import { IMSDK } from "@/layout/MainContentWrap";
import { checkKnownSendPermission, useSendMessage } from "@/pages/chat/queryChat/ChatFooter/useSendMessage";
import { ExMessageItem, useConversationStore, useUserStore } from "@/store";
import { feedbackToast } from "@/utils/common";
import emitter from "@/utils/events";
import { formatMessageByType } from "@/utils/imCommon";

import ChooseBox, { ChooseBoxHandle } from "./ChooseBox";
import { CheckListItem } from "./ChooseBox/CheckItem";

export type ChooseModalType =
  | "CRATE_GROUP"
  | "INVITE_TO_GROUP"
  | "KICK_FORM_GROUP"
  | "TRANSFER_IN_GROUP"
  | "FORWARD_MESSAGE"
  | "SELECT_CARD"
  | "SHARE_CARD"
  | "SELECT_USER"
  | "RTC_INVITE"
  | "MEETING_INVITE";

export interface SelectUserExtraData {
  notConversation: boolean;
  list: CheckListItem[];
}

export interface ChooseModalState {
  type: ChooseModalType;
  extraData?: unknown;
  sendOrigin?: SendOrigin;
  sourceConversationID?: string;
  sourceOwner?: string;
  sourceCurrent?: () => boolean;
  sourceCanShare?: () => boolean;
}

interface IChooseModalProps {
  state: ChooseModalState;
}

const titleMap = {
  CRATE_GROUP: t("placeholder.createGroup"),
  INVITE_TO_GROUP: t("placeholder.invitation"),
  KICK_FORM_GROUP: t("placeholder.kickMember"),
  TRANSFER_IN_GROUP: t("placeholder.transferGroup"),
  FORWARD_MESSAGE: t("placeholder.mergeForward"),
  SELECT_CARD: t("placeholder.share"),
  SHARE_CARD: t("placeholder.share"),
  SELECT_USER: t("placeholder.selectUser"),
  RTC_INVITE: t("placeholder.selectUser"),
  MEETING_INVITE: t("placeholder.selectUser"),
};

i18n.on("languageChanged", () => {
  titleMap.CRATE_GROUP = t("placeholder.createGroup");
  titleMap.INVITE_TO_GROUP = t("placeholder.invitation");
  titleMap.KICK_FORM_GROUP = t("placeholder.kickMember");
  titleMap.TRANSFER_IN_GROUP = t("placeholder.transferGroup");
  titleMap.FORWARD_MESSAGE = t("placeholder.mergeForward");
  titleMap.SELECT_CARD = t("placeholder.share");
  titleMap.SHARE_CARD = t("placeholder.share");
  titleMap.SELECT_USER = t("placeholder.selectUser");
  titleMap.RTC_INVITE = t("placeholder.share");
  titleMap.MEETING_INVITE = t("placeholder.selectUser");
});

const showConversationTypes = ["FORWARD_MESSAGE", "SHARE_CARD", "MEETING_INVITE"];
const onlyOneTypes = ["TRANSFER_IN_GROUP", "SELECT_CARD"];
const onlyMemberTypes = ["KICK_FORM_GROUP", "TRANSFER_IN_GROUP", "RTC_INVITE"];

const ChooseModal: ForwardRefRenderFunction<OverlayVisibleHandle, IChooseModalProps> = (
  { state: { type, extraData, sendOrigin, sourceConversationID, sourceOwner, sourceCurrent, sourceCanShare } },
  ref,
) => {
  const chooseBoxRef = useRef<ChooseBoxHandle>(null);
  const [loading, setLoading] = useState(false);
  const [groupBaseInfo, setGroupBaseInfo] = useState({
    groupName: "",
    groupAvatar: "",
  });

  const { sendMessage } = useSendMessage();
  const { isOverlayOpen, closeOverlay } = useOverlayVisible(ref);
  const mounted=useRef(true),inFlight=useRef(false);
  const context=useRef({open:isOverlayOpen,type,extraData,sendOrigin,sourceCurrent,sourceCanShare,turn:0,owner:messageOwner(),access:captureMessageAccess()});
  if(context.current.open!==isOverlayOpen||context.current.type!==type||context.current.extraData!==extraData||context.current.sendOrigin!==sendOrigin||context.current.sourceCurrent!==sourceCurrent||context.current.sourceCanShare!==sourceCanShare)
    context.current={open:isOverlayOpen,type,extraData,sendOrigin,sourceCurrent,sourceCanShare,turn:context.current.turn+1,owner:messageOwner(),access:captureMessageAccess()};
  useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;};},[]);
  const requestClose=()=>{if(!inFlight.current)closeOverlay();};

  useEffect(() => {
    if (isOverlayOpen && type === "CRATE_GROUP" && extraData) {
      setTimeout(
        () => chooseBoxRef.current?.updatePrevCheckList(extraData as CheckListItem[]),
        100,
      );
    }
    if (isOverlayOpen && type === "SELECT_USER" && extraData) {
      setTimeout(
        () =>
          chooseBoxRef.current?.updatePrevCheckList(
            (extraData as SelectUserExtraData).list,
          ),
        100,
      );
    }
  }, [isOverlayOpen]);

  const confirmChoose = async () => {
    if(inFlight.current||!isOverlayOpen||!context.current.access.current()||context.current.owner!==messageOwner())return;
    const choosedList = chooseBoxRef.current?.getCheckedList() ?? [];
    if (!choosedList?.length && type !== "SELECT_USER")
      return message.warning(t("toast.selectLeastOne"));

    if (!groupBaseInfo.groupName && type === "CRATE_GROUP")
      return message.warning(t("toast.inputGroupName"));

    const sendsMessage = ["SELECT_CARD", "FORWARD_MESSAGE", "SHARE_CARD", "MEETING_INVITE"].includes(type);
    if (sendsMessage && (!sendOrigin?.current() || (sourceCurrent && !sourceCurrent()) || (sourceCanShare && !sourceCanShare()))) return;
    if (type === "FORWARD_MESSAGE" && (!sourceConversationID || sourceConversationID !== sendOrigin?.sourceCID || sourceOwner !== sendOrigin?.ownerKey)) return;
    // Capture every explicit recipient before the first permission/file await.
    // Selection is independent of the active view, but bound to this modal's
    // original owner/history and original immutable recipient namespace.
    const sourcePermitsSend = () => (!sourceCurrent || sourceCurrent()) && (!sourceCanShare || sourceCanShare());
    const selected = choosedList.map(item => {
      const target = selectedSendTarget(item);
      return target ? { target, intent: sendOrigin?.selectedIntent(target, sourcePermitsSend) } : undefined;
    });
    const cardIntent = type === "SELECT_CARD" ? sendOrigin?.currentIntent(sourcePermitsSend) : undefined;
    if (type === "SELECT_CARD" && !cardIntent) return;
    const retention = type === "FORWARD_MESSAGE" ? captureMessageRetention(
      Array.isArray((extraData as MergerMsgParams)?.messageList) ? (extraData as MergerMsgParams).messageList : extraData,
      sendOrigin?.sourceCID, context.current.owner,
    ) : undefined;
    if (retention && !retention.current()) { message.warning("阅后即焚、已过期或未能核验的消息不能转发"); return; }
    const generation=sessionCoordinator.generation,turn=context.current.turn;
    const current=()=>mounted.current&&context.current.open&&context.current.turn===turn&&generation===sessionCoordinator.generation&&context.current.owner===messageOwner()&&context.current.access.current()&&(!retention||retention.current())&&sourcePermitsSend()&&context.current.sourceCurrent===sourceCurrent&&context.current.sourceCanShare===sourceCanShare&&(!sendsMessage||!!sendOrigin?.current());
    if (sendsMessage && type !== "SELECT_CARD" && selected.some(item => !item?.intent)) return;
    let deliveryConfirmed = true;
    inFlight.current=true;setLoading(true);
    try {
      switch (type) {
        case "CRATE_GROUP":
          await IMSDK.createGroup({
            groupInfo: {
              groupType: GroupType.WorkingGroup,
              groupName: groupBaseInfo.groupName,
              faceURL: groupBaseInfo.groupAvatar,
            },
            memberUserIDs: choosedList.map((item) => item.userID!),
            adminUserIDs: [],
          });
          break;
        case "INVITE_TO_GROUP":
          await IMSDK.inviteUserToGroup({
            groupID: extraData as string,
            userIDList: choosedList.map((item) => item.userID!),
            reason: "",
          });
          break;
        case "KICK_FORM_GROUP":
          await IMSDK.kickGroupMember({
            groupID: extraData as string,
            userIDList: choosedList.map((item) => item.userID!),
            reason: "",
          });
          break;
        case "TRANSFER_IN_GROUP":
          await IMSDK.transferGroupOwner({
            groupID: extraData as string,
            newOwnerUserID: choosedList[0].userID!,
          });
          break;
        case "SELECT_CARD": {
          cardIntent!.assertCurrent();
          const cardMessage = (await IMSDK.createCardMessage({
            userID: choosedList[0].userID!, nickname: choosedList[0].nickname ?? "",
            faceURL: choosedList[0].faceURL ?? "", ex: choosedList[0].ex ?? "",
          })).data;
          if (!current() || !cardIntent!.current()) break;
          const receipt = await sendMessage({ message: cardMessage, intent: cardIntent! });
          if (receipt.state !== "sent") { deliveryConfirmed = false; if (current()) message.warning("发送结果未确认，请核实记录，不会自动重发。"); }
          break;
        }
        case "FORWARD_MESSAGE":
        case "SHARE_CARD":
        case "MEETING_INVITE":
        {
          const targets = selected.filter((item): item is NonNullable<typeof item> => !!item);
          const key = (item: { userID?: string; groupID?: string }) => JSON.stringify([item.userID ? "user" : "group", item.userID || item.groupID]);
          const intents = new Map(targets.map(item => [JSON.stringify([item.target.kind, item.target.id]), item.intent!]));
          let activeIntent: SendIntent | undefined;
          const assertPreparing = () => { if (!current() || !activeIntent) throw new Error("发送范围已变化"); activeIntent.assertCurrent(); retention?.assertCurrent(); };
          const receipts=await deliverMessageBatch({
            recipients:targets.map(({ target }) => target.kind === "user" ? { userID: target.id } : { groupID: target.id }),
            additional:chooseBoxRef.current?.getAdditional()||"",isCurrent:current,
            canDeliver:async item=> { const intent = intents.get(key(item)); activeIntent = intent; return !!intent && await checkKnownSendPermission(intent) && current() && intent.current(); },
            createMessage:async()=>{ assertPreparing(); const result=await getBatchMessage(); assertPreparing(); return result; },
            createAdditional:async text=>{ assertPreparing(); const result=(await IMSDK.createTextMessage(text)).data; assertPreparing(); return result; },
            send:(message,item)=>sendMessage({message,intent:intents.get(key(item))!,sourceRetention:retention}),
          });
          if(current()){
            const confirmed=receipts.filter(item=>item.state==="sent").length;
            if(confirmed===receipts.length)message.success(t("toast.sendSuccess"));
            else message.warning(`已确认发送${confirmed}/${receipts.length}个会话；其余未发送或结果未确认，不会自动重发。请先核实消息记录，勿重复发送整批。`);
          }
          break;
        }
        case "SELECT_USER":
          emitter.emit("SELECT_USER", {
            notConversation: (extraData as SelectUserExtraData).notConversation,
            choosedList,
          });
          break;
        case "RTC_INVITE":
          emitter.emit("OPEN_RTC_MODAL", {
            invitation: {
              inviterUserID: useUserStore.getState().selfInfo.userID,
              inviteeUserIDList: choosedList.map((item) => item.userID!),
              groupID:
                useConversationStore.getState().currentConversation?.groupID ?? "",
              roomID:
                useConversationStore.getState().currentConversation?.groupID ??
                uuidV4(),
              timeout: 60,
              mediaType: extraData as string,
              sessionType: SessionType.WorkingGroup,
              platformID: window.electronAPI?.getPlatform() ?? 5,
            },
          });
          break;
        default:
          break;
      }
    } catch (error) {
      if(current())feedbackToast({ error });
    }
    inFlight.current=false;
    if(mounted.current)setLoading(false);
    if(current() && deliveryConfirmed)closeOverlay();
  };

  const getBatchMessage = async () => {
    if ((extraData as MergerMsgParams).title) {
      return (await IMSDK.createMergerMessage(extraData as MergerMsgParams)).data;
    }
    if ((extraData as ExMessageItem).clientMsgID) {
      return (await IMSDK.createForwardMessage(extraData as ExMessageItem)).data;
    }
    if ((extraData as CustomMsgParams).data) {
      return (await IMSDK.createCustomMessage(extraData as CustomMsgParams)).data;
    }
    return (await IMSDK.createCardMessage(extraData as CardElem)).data;
  };

  const getForwardContent = () => {
    if (!showConversationTypes.includes(type)) return undefined;
    if (type === "FORWARD_MESSAGE" && !captureMessageRetention(Array.isArray((extraData as MergerMsgParams)?.messageList) ? (extraData as MergerMsgParams).messageList : extraData, sendOrigin?.sourceCID, context.current.owner).current()) return "阅后即焚、已过期或未能核验的消息不可转发";
    if ((extraData as MergerMsgParams).title) {
      return t("messageDescription.forwardMessage", {
        additional: (extraData as MergerMsgParams).title,
      });
    }
    if ((extraData as ExMessageItem).clientMsgID) {
      return t("messageDescription.forwardMessage", {
        additional: formatMessageByType(extraData as ExMessageItem),
      });
    }
    if ((extraData as CustomMsgParams).data) {
      return t("messageDescription.meetingMessage");
    }
    return t("messageDescription.addtionalCardMessage", {
      additional: (extraData as CardElem).nickname,
    });
  };

  const resetState = () => {
    chooseBoxRef.current?.resetState();
    setGroupBaseInfo({
      groupName: "",
      groupAvatar: "",
    });
  };

  const customUpload = async ({ file }: { file: File }) => {
    try {
      const {
        data: { url },
      } = await IMSDK.uploadFile({
        name: file.name,
        contentType: file.type,
        uuid: uuidV4(),
        file,
      });
      setGroupBaseInfo((prev) => ({ ...prev, groupAvatar: url }));
    } catch (error) {
      feedbackToast({ error: t("toast.updateAvatarFailed") });
    }
  };

  const isCheckInGroup = type === "INVITE_TO_GROUP";
  const notConversation = !showConversationTypes.includes(type);

  return (
    <Modal
      title={null}
      footer={null}
      centered
      open={isOverlayOpen}
      closable={false}
      width={680}
      onCancel={requestClose}
      destroyOnClose
      styles={{
        mask: {
          opacity: 0,
          transition: "none",
        },
      }}
      afterClose={resetState}
      className="no-padding-modal max-w-[80vw]"
      maskTransitionName=""
    >
      <div>
        <div className="app-no-drag flex h-16 items-center justify-between bg-[var(--gap-text)] px-7">
          <div>{titleMap[type]}</div>
          <CloseOutlined
            className="cursor-pointer text-[var(--sub-text)]"
            rev={undefined}
            onClick={requestClose}
          />
        </div>
        {type === "CRATE_GROUP" ? (
          <div className="px-6 pt-4">
            <div className="mb-6 flex items-center">
              <div className="min-w-[60px] font-medium">
                {t("placeholder.groupName")}
              </div>
              <Input
                placeholder={t("placeholder.pleaseEnter")}
                spellCheck={false}
                value={groupBaseInfo.groupName}
                onChange={(e) =>
                  setGroupBaseInfo((state) => ({ ...state, groupName: e.target.value }))
                }
              />
            </div>
            <div className="mb-6 flex items-center">
              <div className="min-w-[60px] font-medium">
                {t("placeholder.groupAvatar")}
              </div>
              <div className="flex items-center">
                <OIMAvatar src={groupBaseInfo.groupAvatar} isgroup />
                <Upload
                  accept="image/*"
                  showUploadList={false}
                  customRequest={customUpload as any}
                >
                  <span className="ml-3 cursor-pointer text-xs text-[var(--primary)]">
                    {t("placeholder.clickToModify")}
                  </span>
                </Upload>
              </div>
            </div>
            <div className="flex">
              <div className="min-w-[60px] font-medium">
                {t("placeholder.groupMember")}
              </div>
              <ChooseBox
                className="!m-0 !h-[40vh] flex-1"
                ref={chooseBoxRef}
                notConversation={notConversation}
              />
            </div>
          </div>
        ) : (
          <ChooseBox
            className="!h-[60vh]"
            ref={chooseBoxRef}
            isCheckInGroup={isCheckInGroup}
            notConversation={
              (extraData as SelectUserExtraData)?.notConversation || notConversation
            }
            showGroupMember={onlyMemberTypes.includes(type)}
            chooseOneOnly={onlyOneTypes.includes(type)}
            checkMemberRole={type === "KICK_FORM_GROUP"}
            fowardContent={getForwardContent()}
          />
        )}
        <div className="flex justify-end px-9 py-6">
          <Button
            className="mr-6 border-0 bg-[var(--chat-bubble)] px-6"
            disabled={loading}
            onClick={requestClose}
          >
            {t("cancel")}
          </Button>
          <Button
            className="px-6"
            type="primary"
            loading={loading}
            onClick={confirmChoose}
          >
            {t("confirm")}
          </Button>
        </div>
      </div>
    </Modal>
  );
};

export default memo(forwardRef(ChooseModal));
