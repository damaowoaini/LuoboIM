import { Popover, Space, Tooltip } from "antd";
import { t } from "i18next";
import {
  FriendUserItem,
  PublicUserItem,
  SelfUserInfo,
} from "open-im-sdk-wasm/lib/types/entity";
import { memo, useState } from "react";

import { modal } from "@/AntdGlobalComp";
import bench from "@/assets/images/common/bench.png";
import call from "@/assets/images/common/call.png";
import cancel from "@/assets/images/common/cancel.png";
import card from "@/assets/images/common/card.png";
import { IMSDK } from "@/layout/MainContentWrap";
import CallPopContent from "@/pages/chat/queryChat/ChatFooter/SendActionBar/CallPopContent";
import { feedbackToast } from "@/utils/common";
import emitter from "@/utils/events";

const CardActionRow = ({
  isFriend,
  isSelf,
  cardInfo,
  closeOverlay,
  isCurrent,
  sourceCurrent,
  sourceCanShare,
}: {
  isFriend?: boolean;
  isSelf?: boolean;
  cardInfo?: Partial<SelfUserInfo & FriendUserItem>;
  closeOverlay: () => void;
  isCurrent: () => boolean;
  sourceCurrent?: () => boolean;
  sourceCanShare?: () => boolean;
}) => {
  const [callOptionsVisible, setCallOptionsVisible] = useState(false);
  const shareCard = () => {
    if (!isCurrent() || (sourceCurrent && !sourceCurrent()) || (sourceCanShare && !sourceCanShare())) return;
    const cardMessageOptions = {
      userID: cardInfo?.userID ?? "",
      nickname: cardInfo?.nickname ?? "",
      faceURL: cardInfo?.faceURL ?? "",
      ex: cardInfo?.ex ?? "",
    };
    emitter.emit("OPEN_CHOOSE_MODAL", {
      type: "SHARE_CARD",
      extraData: cardMessageOptions,
      // The message source survives this profile closing, unlike isCurrent.
      sourceCurrent,
      sourceCanShare,
    });
    closeOverlay();
  };

  const tryUnfriend = () => {
    if (!isCurrent()) return;
    modal.confirm({
      title: t("placeholder.unfriend"),
      content: t("toast.confirmUnfriend"),
      onOk: async () => {
        if (!isCurrent()) return;
        try {
          await IMSDK.deleteFriend(cardInfo!.userID!);
        } catch (error) {
          if (!isCurrent()) return;
          feedbackToast({ error, msg: t("toast.unfriendFailed") });
        }
        if (isCurrent()) closeOverlay();
      },
    });
  };

  const actionClass = "inline-flex items-center justify-center rounded-md text-[var(--primary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--primary)] disabled:opacity-40";
  const actionSize = { minWidth: 44, minHeight: 44 };
  return (
    <div className="flex items-center">
      <Space size={4}>
        <Tooltip title={t("placeholder.share")} placement="bottom">
          <button type="button" aria-label={t("placeholder.share")} className={actionClass} style={actionSize}
            disabled={Boolean(sourceCanShare && !sourceCanShare())} onClick={shareCard}>
            <img width={18} src={card} alt="" />
          </button>
        </Tooltip>
        {isFriend && (
          <Tooltip title={t("placeholder.unfriend")} placement="bottom">
            <button type="button" aria-label={t("placeholder.unfriend")} className={actionClass} style={actionSize} onClick={tryUnfriend}>
              <img width={18} src={cancel} alt="" />
            </button>
          </Tooltip>
        )}
        {!isSelf && (
          <>
            <Popover
              content={
                <CallPopContent
                  userInfo={cardInfo as PublicUserItem}
                  isCurrent={isCurrent}
                  callClick={() => setCallOptionsVisible(false)}
                />
              }
              arrow={false}
              title={null}
              trigger="click"
              placement="bottom"
              open={callOptionsVisible}
              onOpenChange={(vis) => { if (isCurrent()) setCallOptionsVisible(vis); }}
            >
              <button type="button" aria-label={t("placeholder.call")} className={actionClass} style={actionSize}>
                <img width={18} src={call} alt="" />
              </button>
            </Popover>
            {/* <Tooltip title={t("placeholder.moments")} placement="bottom">
              <img className="cursor-pointer" width={18} src={bench} alt="" />
            </Tooltip> */}
          </>
        )}
      </Space>
    </div>
  );
};

export default memo(CardActionRow);
