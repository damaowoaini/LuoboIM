import { LeftOutlined } from "@ant-design/icons";
import { useRequest } from "ahooks";
import { Button, Input } from "antd";
import { t } from "i18next";
import { useRef, useState } from "react";

import { message } from "@/AntdGlobalComp";
import { BusinessAllowType, getBusinessUserInfo } from "@/api/login";
import { exactUserCardPolicy } from "@/core/loadUserCard";
import OIMAvatar from "@/components/OIMAvatar";
import { IMSDK } from "@/layout/MainContentWrap";
import { feedbackToast } from "@/utils/common";

import { CardInfo } from ".";

const SendRequest = ({
  cardInfo,
  backToCard,
  isCurrent,
}: {
  cardInfo: CardInfo;
  backToCard: () => void;
  isCurrent: () => boolean;
}) => {
  const [reqMsg, setReqMsg] = useState("");
  const [checkingPolicy, setCheckingPolicy] = useState(false);
  const submitting = useRef(false);
  const { runAsync, loading } = useRequest(IMSDK.addFriend, {
    manual: true,
  });

  const sendApplication = async () => {
    if (!isCurrent() || submitting.current) return;
    submitting.current = true;
    setCheckingPolicy(true);
    let policyLoaded = false;
    try {
      const target = cardInfo.userID!;
      const { data: { users } } = await getBusinessUserInfo([target]);
      if (!isCurrent()) return;
      const policy = exactUserCardPolicy(target, users);
      policyLoaded = true;
      if (policy.allowAddFriend !== BusinessAllowType.Allow) {
        message.warning(t("toast.notCanAddFriend"));
        return;
      }
      // No await between the final owner/policy check and SDK dispatch.
      if (!isCurrent()) return;
      await runAsync({ toUserID: target, reqMsg });
      if (!isCurrent()) return;
      feedbackToast({ msg: t("toast.sendFreiendRequestSuccess") });
      backToCard();
    } catch {
      if (!isCurrent()) return;
      feedbackToast({ error: new Error("Friend application unavailable"),
        msg: t(policyLoaded ? "toast.sendApplicationFailed" : "toast.getUserInfoFailed") });
      // Keep the draft for an explicit retry; never automatically re-dispatch.
    } finally {
      submitting.current = false;
      if (isCurrent()) setCheckingPolicy(false);
    }
  };

  return (
    <div className="flex max-h-[520px] min-h-[484px] flex-col overflow-hidden px-5.5">
      <div className="w-full cursor-move">
        <div className="mb-8 mt-4.5 flex items-center">
          <LeftOutlined
            className="cursor-pointer text-[var(--sub-text)]"
            rev={undefined}
            onClick={backToCard}
          />
          <div className="ml-2 font-medium">{t("placeholder.friendVerification")}</div>
        </div>
      </div>
      <div className="ignore-drag flex flex-1 flex-col">
        <div className="flex items-center">
          <OIMAvatar size={60} src={cardInfo?.faceURL} text={cardInfo?.nickname} />
          <div className="ml-3 flex-1 overflow-hidden">
            <div
              className="mb-3 flex-1 truncate text-base font-medium"
              title={cardInfo?.nickname}
            >
              {cardInfo?.nickname}
            </div>
            <div className="mr-3 text-xs text-[var(--sub-text)]">
              {cardInfo?.userID}
            </div>
          </div>
        </div>
        <div className="mt-7">
          <div className="text-xs text-[var(--sub-text)]">
            {t("application.information")}
          </div>
          <div className="mx-2 my-4">
            <Input.TextArea
              showCount
              value={reqMsg}
              maxLength={50}
              bordered={false}
              spellCheck={false}
              placeholder={t("placeholder.pleaseEnter")}
              style={{ padding: "8px 6px" }}
              autoSize={{ minRows: 6, maxRows: 6 }}
              onChange={(e) => setReqMsg(e.target.value)}
              className="bg-[var(--chat-bubble)] hover:bg-[var(--chat-bubble)]"
            />
          </div>
        </div>
        <div className="mx-2 mb-6 flex flex-1 items-end">
          <Button
            className="flex-1"
            type="primary"
            onClick={sendApplication}
            loading={loading || checkingPolicy}
          >
            {t("placeholder.send")}
          </Button>
        </div>
      </div>
    </div>
  );
};

export default SendRequest;
