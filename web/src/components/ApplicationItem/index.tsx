import { Button, Spin } from "antd";
import { t } from "i18next";
import { ApplicationHandleResult } from "open-im-sdk-wasm";
import {
  FriendApplicationItem,
  GroupApplicationItem,
} from "open-im-sdk-wasm/lib/types/entity";
import { memo, useEffect, useRef, useState } from "react";

import arrow from "@/assets/images/contact/arrowTopRight.png";
import OIMAvatar from "@/components/OIMAvatar";
import { feedbackToast } from "@/utils/common";

type ApplicationItemSource = FriendApplicationItem & GroupApplicationItem;

export type AccessFunction = (source: Partial<ApplicationItemSource>) => Promise<void>;

const identifier = (value: unknown): value is string =>
  typeof value === "string" && value.length > 0 && value.trim() === value &&
  !/[\u0000-\u001f\u007f]/.test(value);

export function friendApplicationIdentity(source: Partial<ApplicationItemSource>, actor: string) {
  const pair = identifier(actor) && identifier(source.fromUserID) &&
    identifier(source.toUserID) && source.fromUserID !== source.toUserID;
  const incoming = Boolean(pair && source.toUserID === actor);
  const outgoing = Boolean(pair && source.fromUserID === actor);
  const peerID = incoming ? source.fromUserID : outgoing ? source.toUserID : undefined;
  const complete = Boolean((source.groupID === undefined || source.groupID === "") && pair && (incoming || outgoing) &&
    Number.isSafeInteger(source.createTime) && source.createTime! > 0 &&
    typeof source.reqMsg === "string" &&
    [ApplicationHandleResult.Unprocessed, ApplicationHandleResult.Agree,
      ApplicationHandleResult.Reject].includes(source.handleResult!));
  return { incoming, outgoing, peerID, canHandle: complete && incoming &&
    source.handleResult === ApplicationHandleResult.Unprocessed };
}

const ApplicationItem = ({
  currentUserID,
  source,
  onAccept,
  onReject,
  canHandle = true,
}: {
  source: Partial<ApplicationItemSource>;
  currentUserID: string;
  onAccept: AccessFunction;
  onReject: AccessFunction;
  canHandle?: boolean;
}) => {
  const [loading, setLoading] = useState(false);
  const busy = useRef(false);
  const mounted = useRef(false);
  const live = useRef({ source, currentUserID, onAccept, onReject });
  live.current = { source, currentUserID, onAccept, onReject };
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const isGroup = Boolean(source.groupID);
  const identity = friendApplicationIdentity(source, currentUserID);
  const isRecv = isGroup
    ? source.userID !== currentUserID && source.fromUserID !== currentUserID
    : identity.incoming;
  const showActionBtn = canHandle && (isGroup
    ? source.handleResult === ApplicationHandleResult.Unprocessed && isRecv
    : identity.canHandle);

  const getApplicationDesc = () => {
    if (isGroup) {
      return `${isRecv ? "" : `${t("me")}：`}${t("application.applyToJoin")} `;
    }
    return `${isRecv ? "" : `${t("me")}：`}${t("application.applyToFriend")} `;
  };

  const getTitle = () => {
    if (isGroup) {
      return isRecv ? source.nickname : source.groupName;
    }
    return isRecv ? source.fromNickname : source.toNickname;
  };

  const getStatusStr = () => {
    if (source.handleResult === ApplicationHandleResult.Agree) {
      return t("application.agreed");
    }
    if (source.handleResult === ApplicationHandleResult.Reject) {
      return t("application.refused");
    }
    return t("application.pending");
  };

  const getAvatarUrl = () => {
    if (isGroup) {
      return isRecv ? source.userFaceURL : source.groupFaceURL;
    }
    return isRecv ? source.fromFaceURL : source.toFaceURL;
  };

  const loadingWrap = async (isAgree: boolean) => {
    if (busy.current || !showActionBtn || !mounted.current) return;
    busy.current = true;
    const captured = live.current;
    setLoading(true);
    try {
      await (isAgree ? onAccept(source) : onReject(source));
    } catch (error) {
      if (mounted.current && live.current.source === captured.source &&
          live.current.currentUserID === captured.currentUserID &&
          live.current.onAccept === captured.onAccept && live.current.onReject === captured.onReject)
        feedbackToast({ error });
    } finally {
      busy.current = false;
      if (mounted.current) setLoading(false);
    }
  };

  return (
    <Spin spinning={loading}>
      <div className="flex min-w-0 flex-col gap-3 p-3.5 transition-colors hover:bg-[var(--primary-active)] sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 flex-1 items-start">
          <OIMAvatar src={getAvatarUrl()} text={getTitle()} isgroup={isGroup} />
          <div className="ml-3 min-w-0 flex-1">
            <p className="break-words text-sm">{getTitle()}</p>
            {!isGroup && identity.peerID && (
              <p className="break-all text-xs text-[var(--sub-text)]">
                {t("placeholder.recipientAccountID")}: {identity.peerID}
              </p>
            )}
            <p className="pb-2.5 pt-[5px] text-xs ">
              {getApplicationDesc()}
              {isGroup && (
                <span className="text-xs text-[#0289FAFF]">{source.groupName}</span>
              )}
            </p>
            <p className="text-xs text-[var(--sub-text)]">
              {t("application.information")}:
            </p>
            <p className="whitespace-pre-wrap break-words text-xs text-[var(--sub-text)]">{source.reqMsg}</p>
          </div>
        </div>

        {showActionBtn && (
          <div className="flex shrink-0 flex-row gap-3 self-end sm:self-center">
            <div className="h-8 w-[60px]">
              <Button
                block={true}
                size="small"
                disabled={loading}
                onClick={() => loadingWrap(false)}
                className="!h-full !rounded-md border-2 border-[#0089FF] text-[#0089FF]"
              >
                {t("application.refuse")}
              </Button>
            </div>
            <div className="h-8 w-[60px]">
              <Button
                block={true}
                size="small"
                disabled={loading}
                type="primary"
                className="!h-full !rounded-md bg-[#0289fa]"
                onClick={() => loadingWrap(true)}
              >
                {t("application.agree")}
              </Button>
            </div>
          </div>
        )}

        {!showActionBtn && (
          <div className="flex flex-row items-center">
            {!isRecv && <img className="mr-2 h-4 w-4" src={arrow} alt="" />}
            <p className="text-sm text-[var(--sub-text)]">{getStatusStr()}</p>
          </div>
        )}
      </div>
    </Spin>
  );
};

export default memo(ApplicationItem);
