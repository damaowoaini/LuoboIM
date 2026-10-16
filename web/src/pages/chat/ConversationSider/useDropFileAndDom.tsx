import { captureMessageRetention } from "@/core/messagePrivacyRuntime";
import { selectedSendTarget } from "@/core/messageSendIntent";
import { useDrop } from "ahooks";
import { t } from "i18next";
import { MessageType } from "open-im-sdk-wasm";
import { ConversationItem } from "open-im-sdk-wasm/lib/types/entity";
import { useState } from "react";

import { modal } from "@/AntdGlobalComp";
import file_icon from "@/assets/images/messageItem/file_icon.png";
import { IMSDK } from "@/layout/MainContentWrap";
import { ExMessageItem } from "@/store";
import { bytesToSize } from "@/utils/common";

import { useFileMessage } from "../queryChat/ChatFooter/SendActionBar/useFileMessage";
import { checkKnownSendPermission, useSendMessage } from "../queryChat/ChatFooter/useSendMessage";

export function useDropFileAndDom({
  domRef,
  currentConversation,
}: {
  domRef: React.RefObject<HTMLDivElement | null>;
  currentConversation: ConversationItem;
}) {
  const [droping, setDroping] = useState(false);
  const { createFileMessage } = useFileMessage();
  const { sendMessage, beginSelectedSend } = useSendMessage();
  const captureTarget = () => { const target = selectedSendTarget(currentConversation); return target ? beginSelectedSend(target) : undefined; };

  const dropEnd = () => {
    if (!droping) return;
    setDroping(false);
  };

  useDrop(domRef.current, {
    onText: (_, e) => {
      e?.preventDefault();
      dropEnd();
    },
    onFiles: async (files, e) => {
      const intent = captureTarget();
      if (!intent || !(await checkKnownSendPermission(intent)) || !intent.current()) {
        dropEnd();
        return;
      }

      if (files.length) {
        modal.confirm({
          title: `${t("placeholder.sendTo")}${currentConversation.showName}`,
          icon: null,
          width: 320,
          centered: true,
          className: "drop-file-moal",
          content: (
            <div className="h-[240px] overflow-y-auto border-b border-t border-[var(--gap-text)] p-2">
              {files.map((file) => (
                <div className="mb-2 flex items-center" key={file.lastModified}>
                  <img width={38} src={file_icon} alt="file" />
                  <div className="ml-3 overflow-hidden">
                    <div className="mb-1.5 truncate">{file.name}</div>
                    <div className="text-xs text-[var(--sub-text)]">
                      {bytesToSize(file.size)}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          ),
          okText: t("confirm"),
          cancelText: t("cancel"),
          onOk: async () => {
            for (const file of files) {
              if (!intent.current()) return;
              const message = await createFileMessage(file, intent.assertCurrent);
              if (!intent.current() || (await sendMessage({ message, intent })).state !== "sent") return;
            }
          },
        });
      }

      dropEnd();
    },
    onUri: (_, e) => {
      e?.preventDefault();
      dropEnd();
    },
    onDom: async ({ message, conversationID, sourceOwner }: { message: ExMessageItem; conversationID?: string; sourceOwner?: string }, e) => {
      const intent = captureTarget();
      if (!intent || !conversationID || sourceOwner !== intent.ownerKey) return;
      const retention = captureMessageRetention(message, conversationID, sourceOwner);
      if (!retention.current() || !(await checkKnownSendPermission(intent)) || !intent.current()) {
        dropEnd();
        return;
      }
      const { fileName, fileSize } = getFileData(message);
      modal.confirm({
        title: `${t("placeholder.sendTo")}${currentConversation.showName}`,
        icon: null,
        width: 320,
        centered: true,
        className: "drop-file-moal",
        content: (
          <div className="h-[240px] overflow-y-auto border-b border-t border-[var(--gap-text)] p-2">
            <div className="mb-2 flex items-center">
              <img width={38} src={file_icon} alt="file" />
              <div className="ml-3 overflow-hidden">
                <div className="mb-1.5 truncate">{fileName}</div>
                <div className="text-xs text-[var(--sub-text)]">
                  {bytesToSize(fileSize)}
                </div>
              </div>
            </div>
          </div>
        ),
        okText: t("confirm"),
        cancelText: t("cancel"),
        onOk: async () => {
          intent.assertCurrent(); retention.assertCurrent();
          const newMessage = (await IMSDK.createForwardMessage(message)).data;
          intent.assertCurrent(); retention.assertCurrent();
          await sendMessage({ message: newMessage, intent, sourceRetention: retention });
        },
      });
      dropEnd();
    },
    onDragEnter: () => {
      if (droping) return;
      setDroping(true);
    },
    onDragLeave: dropEnd,
  });

  return {
    droping,
  };
}

const getFileData = (message: ExMessageItem) => {
  if (message.contentType === MessageType.PictureMessage) {
    const idx = message.pictureElem.sourcePath?.lastIndexOf("/") ?? -1;
    return {
      fileName:
        idx > -1
          ? message.pictureElem.sourcePath.slice(idx + 1)
          : t("placeholder.file"),
      fileSize: message.pictureElem.sourcePicture.size,
    };
  }
  if (message.contentType === MessageType.VideoMessage) {
    const idx = message.videoElem.videoPath.lastIndexOf("/");
    return {
      fileName:
        idx > -1 ? message.videoElem.videoPath.slice(idx + 1) : t("placeholder.file"),
      fileSize: message.videoElem.videoSize,
    };
  }
  return {
    fileName: message.fileElem.fileName,
    fileSize: message.fileElem.fileSize,
  };
};
