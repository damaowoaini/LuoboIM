import { captureMessageRetention, messageOwner } from "@/core/messagePrivacyRuntime";
import { useMessagePrivacy } from "@/hooks/useMessagePrivacy";
import { subscribeSessionEvent } from "@/core/session/desktop";
import { usePagedMessageSearch } from "@/core/usePagedMessageSearch";
import { Empty, Spin } from "antd";
import { t } from "i18next";
import { MessageType } from "open-im-sdk-wasm";
import { memo, useEffect, useRef } from "react";
import { Virtuoso } from "react-virtuoso";

import file_download from "@/assets/images/messageItem/file_download.png";
import file_icon from "@/assets/images/messageItem/file_icon.png";
import {
  getSourceData,
  useMessageFileDownloadState,
} from "@/hooks/useMessageFileDownloadState";
import { IMSDK } from "@/layout/MainContentWrap";
import ViewFileInFinder from "@/pages/common/GlobalSearchModal/ViewFileInFinder";
import { ExMessageItem, useMessageStore } from "@/store";
import FileDownloadIcon from "@/svg/FileDownloadIcon";
import { bytesToSize, feedbackToast } from "@/utils/common";
import { formatMessageTime } from "@/utils/imCommon";

import MessageSearchBar from "./MessageSearchBar";

const FileMessagePane = ({
  isActive,
  conversationID,
  isOverlayOpen,
}: {
  isActive: boolean;
  conversationID?: string;
  isOverlayOpen: boolean;
}) => {
  const loadMoreKeyword = useRef("");
  const inputRef = useRef<{ clear: () => void }>(null);
  const {search,updateMessages,...loadState}=usePagedMessageSearch<ExMessageItem>({
    conversationID,enabled:isOverlayOpen&&isActive,
    loadPage:async(keyword,pageIndex)=>{
      const {data}=await IMSDK.searchLocalMessages({conversationID:conversationID!,keywordList:keyword?[keyword]:[],keywordListMatchType:0,senderUserIDList:[],messageTypeList:[MessageType.FileMessage],searchTimePosition:0,searchTimePeriod:0,pageIndex,count:20});
      return data.searchResultItems?.[0]?.messageList??[];
    },
    onError:error=>feedbackToast({error,msg:t("toast.getMessageListFailed")}),
  });
  const triggerSearch=(keyword:string,loadMore=false)=>{loadMoreKeyword.current=keyword;search(keyword,loadMore);};
  useEffect(()=>{
    const unsubscribe=subscribeSessionEvent("downloadSuccess",(url:string,filePath:string)=>{
      const task=useMessageStore.getState().downloadMap[url];if(!task)return;
      updateMessages(messages=>messages.map(message=>message.clientMsgID===task.clientMsgID?{...message,localEx:filePath}:message));
    });
    return()=>{unsubscribe?.();};
  },[conversationID,isOverlayOpen,isActive,updateMessages]);
  useEffect(()=>{if(isOverlayOpen&&isActive){loadMoreKeyword.current="";inputRef.current?.clear();search("",false,true);}},[search,isOverlayOpen,isActive]);

  return (
    <div className="flex h-full flex-col">
      <MessageSearchBar ref={inputRef} triggerSearch={triggerSearch} />
      <div className="my-2 flex-1 px-2.5">
        <Virtuoso
          className="h-full overflow-x-hidden"
          data={loadState.messageList}
          endReached={() => triggerSearch(loadMoreKeyword.current, true)}
          components={{
            EmptyPlaceholder: () =>
              loadState.loading ? null : (
                <Empty
                  className="flex h-full flex-col items-center justify-center"
                  description={t("empty.noSearchResults")}
                />
              ),
            Footer: () =>
              loadState.loading ? (
                <div className="flex w-full justify-center py-3">
                  <Spin spinning />
                </div>
              ) : null,
          }}
          itemContent={(_, message) => <FileMessageItem message={message} />}
        />
      </div>
    </div>
  );
};

export default memo(FileMessagePane);

const FileMessageItem = memo(({ message }: { message: ExMessageItem }) => {
  const sourceOwner = useRef(messageOwner()).current;
  const privacy = useMessagePrivacy(message);
  const { fileElem } = message;

  const { progress, downloadState, tryDownload } = useMessageFileDownloadState(message);

  const viewInFinder = () => {
    if (!captureMessageRetention(message,"",sourceOwner).current()) return;
    const path = message.localEx || getSourceData(message).path;
    if (path) {
      window.electronAPI?.showInFinder(path);
    }
  };

  if (privacy.expired) return <span>消息已过期或已移除</span>;
  return (
    <ViewFileInFinder viewInFinder={viewInFinder}>
      <div
        data-message-retention={privacy.allowsRetention ? "allowed" : "blocked"}
        onCopyCapture={event => { if (!privacy.allowsRetention) event.preventDefault(); }}
        className="flex cursor-pointer items-center justify-between rounded-md px-3 py-2.5 hover:bg-[var(--primary-active)]"
        onClick={tryDownload}
      >
        <div className="flex items-center">
          <div className="relative">
            <img width={38} src={file_icon} alt="file" />
            {downloadState !== "finish" && (
              <div className="absolute left-0 top-0 flex h-full w-full items-center justify-center rounded-md bg-[rgba(0,0,0,.4)]">
                <FileDownloadIcon
                  pausing={downloadState === "pause"}
                  percent={progress ?? 0}
                />
              </div>
            )}
          </div>
          <div className="ml-3">
            <div>{fileElem.fileName}</div>
            <div className="mt-2 flex items-center text-xs">
              <div>{bytesToSize(fileElem.fileSize)}</div>
              <div className="ml-3.5 mr-2 max-w-[120px] truncate text-[var(--sub-text)]">
                {message.senderNickname}
              </div>
              <div className="text-[var(--sub-text)]">
                {formatMessageTime(message.sendTime)}
              </div>
            </div>
          </div>
        </div>
      </div>
    </ViewFileInFinder>
  );
});
