import { sendFailureText, visibleSendFailure } from "@/core/sendFailure";
import { useMessageAccess } from "@/hooks/useMessagePrivacy";
import { messageOwner } from "@/core/messagePrivacyRuntime";
import { ExclamationCircleFilled, LoadingOutlined } from "@ant-design/icons";
import { Spin } from "antd";
import { MessageStatus, MessageType } from "open-im-sdk-wasm";
import { FC, useEffect, useState, useRef } from "react";

import { IMSDK } from "@/layout/MainContentWrap";
import { ExMessageItem } from "@/store";
import { sessionCoordinator } from "@/core/session/browser";
import { useMessagePrivacy } from "@/hooks/useMessagePrivacy";
import { captureMessageAccess, captureMessageView } from "@/core/messagePrivacyRuntime";
import { message as antdMessage } from "@/AntdGlobalComp";

import { useSendMessage } from "../ChatFooter/useSendMessage";
import { IMessageItemProps } from ".";
import styles from "./message-item.module.scss";

const MessageSuffix: FC<IMessageItemProps> = ({ message, conversationID }) => {
  const sourceOwner = useRef(messageOwner()).current;
  const sourceAccess = useMessageAccess(conversationID);
  const privacy = useMessagePrivacy(message, conversationID);
  const [showSending, setShowSending] = useState(false);
  const sendFailure = visibleSendFailure(message.sendFailure);

  const { sendMessage, updateOneMessage, beginCurrentSend } = useSendMessage();

  const isShowLimitTimer = privacy.rootPrivate && privacy.remainingSeconds !== undefined && !privacy.expired;

  useEffect(() => {
    if (message.status !== MessageStatus.Sending) return;
    const timer = setTimeout(() => {
      if (message.status === MessageStatus.Sending) {
        setShowSending(true);
      }
    }, 1000);
    return () => {
      clearTimeout(timer);
    };
  }, [message.status]);

  const reSend = async () => {
    const intent = beginCurrentSend();
    if (!sourceAccess.current() || !intent || intent.target.conversationID !== conversationID) return;
    const generation = sessionCoordinator.generation;
    const access = captureMessageAccess(conversationID, sourceOwner);
    if (!captureMessageView(message, conversationID, sourceOwner).current() || !access.current() || !intent.current()) return;
    // A policy change blocks dispatch, but must not leave this same-view retry
    // in Sending. This guard intentionally does not depend on canSend.
    const canProject = () => generation === sessionCoordinator.generation && sourceOwner === messageOwner() &&
      sourceAccess.current() && access.current() && intent.projectionCurrent() &&
      captureMessageView(message, conversationID, sourceOwner).current();
    updateOneMessage({ ...message, status: MessageStatus.Sending });
    try {
      await checkResendFile(message, () => { intent.assertCurrent(); access.assertCurrent(); captureMessageView(message, conversationID, sourceOwner).assertCurrent(); });
      if (!canProject()) return;
      const receipt = await sendMessage({ message, intent, needPush: false, isResend: true });
      if (receipt.state !== "sent" && canProject()) updateOneMessage({ ...message, status: MessageStatus.Failed });
    } catch {
      if (!canProject()) return;
      updateOneMessage({ ...message, status: MessageStatus.Failed });
      antdMessage.error("暂时无法重试，请检查本地文件后重新选择。");
    }
  };

  return (
    <div className={styles.suffix}>
      {showSending && message.status === MessageStatus.Sending && (
        <Spin
          className="flex"
          indicator={<LoadingOutlined style={{ fontSize: 16 }} spin rev={undefined} />}
        />
      )}
      {!privacy.expired && message.status === MessageStatus.Failed && (
        <div className="flex max-w-64 items-center gap-1">
          <button type="button" aria-label="发送失败，重试这条消息" title="发送失败，点击重试"
            className="rounded text-base text-[var(--warn-text)]" onClick={reSend}>
            <ExclamationCircleFilled rev={undefined} />
          </button>
          <span role="status" className="text-xs text-[var(--warn-text)]"
            data-send-failure-phase={sendFailure.phase}
            data-send-failure-reason={sendFailure.reason}
            data-send-failure-code={sendFailure.code ?? "unknown"}
            data-send-failure-http-status={sendFailure.httpStatus ?? "unknown"}
            data-send-failure-s3-code={sendFailure.s3Code}>
            {sendFailureText(sendFailure, [MessageType.PictureMessage, MessageType.VoiceMessage,
              MessageType.VideoMessage, MessageType.FileMessage].includes(message.contentType))}
          </span>
        </div>
      )}
      {isShowLimitTimer && (
        <div className="text-xs text-[var(--sub-text)]">{`${privacy.remainingSeconds}s`}</div>
      )}
    </div>
  );
};

export default MessageSuffix;

const getSourcePath = (message: ExMessageItem) => {
  if (message.contentType === MessageType.PictureMessage) {
    return {
      uuid: message.pictureElem.sourcePicture.uuid,
      path: message.pictureElem.sourcePath,
    };
  }
  if (message.contentType === MessageType.VideoMessage) {
    return {
      uuid: message.videoElem.videoUUID,
      path: message.videoElem.videoPath,
      snapshotPath: message.videoElem.snapshotPath,
      snapshotUUID: message.videoElem.snapshotUUID,
    };
  }
  return {
    uuid: message.fileElem.uuid,
    path: message.fileElem.filePath,
  };
};

const fileTypes = [
  MessageType.PictureMessage,
  MessageType.VideoMessage,
  MessageType.FileMessage,
];
const checkResendFile = async (message: ExMessageItem, assertCurrent: () => void) => {
  assertCurrent();
  if (!window.electronAPI || !fileTypes.includes(message.contentType)) return;
  const { uuid, path, snapshotPath, snapshotUUID } = getSourcePath(message);
  const sourceFile = await window.electronAPI?.getFileByPath(path);

  assertCurrent();
  let snapshotFile;
  if (snapshotPath) {
    snapshotFile = await window.electronAPI?.getFileByPath(snapshotPath);
  }
  assertCurrent();
  if (!sourceFile || (snapshotPath && !snapshotFile)) throw new Error("本地文件不可用");
  await IMSDK.fileMapSet(uuid, sourceFile);
  assertCurrent();
  if (snapshotUUID && snapshotFile) {
    await IMSDK.fileMapSet(snapshotUUID, snapshotFile);
  }
};
