import { captureMessageRetention, captureMessageView, messageOwner, messagePrivacyState } from "@/core/messagePrivacyRuntime";
import { useMessagePrivacy, useMessagePrivacyRevision } from "@/hooks/useMessagePrivacy";
import type { JSX } from "react";
import { subscribeSessionEvent } from "@/core/session/desktop";
import { usePagedMessageSearch } from "@/core/usePagedMessageSearch";
import { Empty, Image } from "antd";
import clsx from "clsx";
import { isThisMonth, isThisWeek } from "date-fns";
import { t } from "i18next";
import { MessageType } from "open-im-sdk-wasm";
import {
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

import CacheImage from "@/components/CacheImage";
import PreviewToolsBar from "@/components/PreviewToolsBar";
import { useMessageFileDownloadState } from "@/hooks/useMessageFileDownloadState";
import { IMSDK } from "@/layout/MainContentWrap";
import {
  ExMessageItem,
  getImageMessageSourceUrl,
  useMessageStore,
  useUserStore,
} from "@/store";
import { PreviewGroupItem } from "@/store/type";
import FileDownloadIcon from "@/svg/FileDownloadIcon";
import { downloadFile, feedbackToast, getDownloadTask } from "@/utils/common";
import emitter from "@/utils/events";

const MediaMessagePane = ({
  isVideo,
  isActive,
  isOverlayOpen,
  conversationID,
}: {
  isVideo?: boolean;
  isActive: boolean;
  isOverlayOpen: boolean;
  conversationID?: string;
}) => {
  const sourceOwner = useRef(messageOwner()).current;
  const {messageList,loading,search,updateMessages}=usePagedMessageSearch<ExMessageItem>({
    conversationID,enabled:isOverlayOpen&&isActive,
    loadPage:async(_keyword,pageIndex)=>{
      const {data}=await IMSDK.searchLocalMessages({conversationID:conversationID!,keywordList:[],keywordListMatchType:0,senderUserIDList:[],messageTypeList:[isVideo?MessageType.VideoMessage:MessageType.PictureMessage],searchTimePosition:0,searchTimePeriod:0,pageIndex,count:20});
      return data.searchResultItems?.[0]?.messageList??[];
    },
    onError:error=>feedbackToast({error,msg:t("toast.getMessageListFailed")}),
  });
  const loadState=useMemo(()=>{
    const weekMessage:ExMessageItem[]=[],monthMessage:ExMessageItem[]=[],earlierMessage:ExMessageItem[]=[],previewItems:PreviewGroupItem[]=[];
    for(const message of messageList){
      if(isThisWeek(message.sendTime))weekMessage.push(message);
      else if(isThisMonth(message.sendTime))monthMessage.push(message);
      else earlierMessage.push(message);
      if(!isVideo && !messagePrivacyState(message, conversationID, sourceOwner).expired)previewItems.push({sourceMessage:message,url:getImageMessageSourceUrl(message)??"",clientMsgID:message.clientMsgID,thumbUrl:message.pictureElem?.snapshotPicture.url??""});
    }
    return {weekMessage,monthMessage,earlierMessage,previewItems,loading};
  },[messageList,loading,isVideo]);
  const galleryRef=useRef<{setAlbumCurrent(current:number):void;setAlbumVisible(visible:boolean):void;}>(null);
  useEffect(()=>{
    const unsubscribe=subscribeSessionEvent("downloadSuccess",(url:string,filePath:string)=>{
      const task=useMessageStore.getState().downloadMap[url];if(!task)return;
      updateMessages(messages=>messages.map(message=>message.clientMsgID===task.clientMsgID?{...message,localEx:filePath}:message));
    });
    return()=>{unsubscribe?.();};
  },[conversationID,isOverlayOpen,isActive,updateMessages]);
  useEffect(()=>{if(isOverlayOpen&&isActive)search("",false,true);},[search,isOverlayOpen,isActive]);
  const loadMore=()=>search("",true);

  const showAlbum = useCallback(
    (clientMsgID: string) => {
      const current = loadState.previewItems.findIndex(
        (img) => img.clientMsgID === clientMsgID,
      );
      if (current < 0 || !captureMessageView(loadState.previewItems[current].sourceMessage, conversationID, sourceOwner).current()) return;

      if (
        window.electronAPI &&
        loadState.previewItems[current].url.startsWith("http") && captureMessageRetention(loadState.previewItems[current].sourceMessage, conversationID, sourceOwner).current()
      ) {
        downloadFile(loadState.previewItems[current].url, {
          clientMsgID,
          conversationID,
          isMediaMessage: true,
          saveType: "image",
          showError: true,
        });
      }

      galleryRef.current?.setAlbumCurrent(current);
      galleryRef.current?.setAlbumVisible(true);
    },
    [loadState.previewItems,conversationID],
  );

  const dataSource = [
    loadState.weekMessage,
    loadState.monthMessage,
    loadState.earlierMessage,
  ];

  return (
    <div className="h-full px-5.5 pb-2">
      {dataSource.flat().length > 0 ? (
        <Virtuoso
          className="h-full overflow-x-hidden"
          data={dataSource}
          endReached={() => loadMore()}
          components={{
            Footer: () => (loadState.loading ? <div>loading...</div> : null),
          }}
          itemContent={(index, messageList) => {
            switch (index) {
              case 0:
                return messageList.length ? (
                  <MediaMessageRow
                    title={t("date.thisWeek")}
                    messageList={messageList}
                    preview={showAlbum}
                  />
                ) : (
                  <PlaceholderEl />
                );
              case 1:
                return messageList.length ? (
                  <MediaMessageRow
                    title={t("date.thisMonth")}
                    messageList={messageList}
                    preview={showAlbum}
                  />
                ) : (
                  <PlaceholderEl />
                );
              case 2:
                return messageList.length ? (
                  <MediaMessageRow
                    title={t("date.earlier")}
                    messageList={messageList}
                    preview={showAlbum}
                  />
                ) : (
                  <PlaceholderEl />
                );
              default:
                return <PlaceholderEl />;
            }
          }}
        />
      ) : (
        <Empty
          className="flex h-full flex-col items-center justify-center"
          description={t("empty.noSearchResults")}
        />
      )}
      <ForwardGallery
        conversationID={conversationID}
        previewItems={loadState.previewItems}
        ref={galleryRef}
      />
    </div>
  );
};

export default MediaMessagePane;

const MediaMessageRow = ({
  title,
  messageList,
  preview,
}: {
  title: string;
  messageList: ExMessageItem[];
  preview: (clientMsgID: string) => void;
}) => (
  <div className="mb-3">
    <div className="mb-3">{title}</div>
    <div className="grid grid-cols-4 gap-2">
      {messageList.map((message) => (
        <MediaItem key={message.clientMsgID} preview={preview} message={message} />
      ))}
    </div>
  </div>
);

const previewInPlayer = (path: string) => {
  emitter.emit("OPEN_VIDEO_PLAYER", `file://${path}`);
};

const MediaItem = ({
  message,
  preview,
}: {
  message: ExMessageItem;
  preview: (clientMsgID: string) => void;
}) => {
  const sourceOwner = useRef(messageOwner()).current;
  const privacy = useMessagePrivacy(message);
  const { progress, downloadState, tryDownload } = useMessageFileDownloadState(
    message,
    previewInPlayer,
  );

  const isVideo = message.contentType === MessageType.VideoMessage;

  const tryPlayVideo = () => {
    if (!captureMessageView(message, "", sourceOwner).current()) return;
    if (window.electronAPI && captureMessageRetention(message, "", sourceOwner).current()) {
      tryDownload();
      return;
    }
    emitter.emit("OPEN_VIDEO_PLAYER", {url:message.videoElem.videoUrl,message,owner:sourceOwner});
  };

  const getSourceUrl = () => {
    if (
      !isVideo &&
      message.localEx &&
      window.electronAPI?.fileExists(message.localEx)
    ) {
      return message.localEx;
    }
    if (
      !isVideo &&
      message.pictureElem.sourcePath &&
      window.electronAPI?.fileExists(message.pictureElem.sourcePath)
    ) {
      return message.pictureElem.sourcePath;
    }
    if (
      isVideo &&
      message.videoElem.snapshotPath &&
      window.electronAPI?.fileExists(message.videoElem.snapshotPath)
    ) {
      return message.videoElem.snapshotPath;
    }
    return isVideo
      ? message.videoElem.snapshotUrl
      : message.pictureElem.snapshotPicture.url;
  };

  if (privacy.expired) return <span>消息已过期或已移除</span>;
  return (
    <div
      onCopyCapture={event => { if (!privacy.allowsRetention) event.preventDefault(); }}
      onDragStartCapture={event => { if (!privacy.allowsRetention) event.preventDefault(); }}
      className={clsx(
        "grid-image relative flex h-[90px] items-center justify-center overflow-hidden rounded-md",
        { "cursor-pointer": isVideo },
      )}
      key={message.clientMsgID}
    >
      <CacheImage
        cache={privacy.allowsRetention}
        src={getSourceUrl()}
        height={90}
        preview={isVideo ? false : { visible: false }}
        onClick={() => { if (!isVideo && captureMessageView(message, "", sourceOwner).current()) preview?.(message.clientMsgID); }}
        className="object-cover"
      />
      {isVideo && (
        <div
          className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 cursor-pointer"
          onClick={tryPlayVideo}
        >
          <FileDownloadIcon
            size={40}
            pausing={downloadState === "pause"}
            finished={downloadState === "finish" || !window.electronAPI}
            percent={progress}
          />
        </div>
      )}
    </div>
  );
};

const PlaceholderEl = () => <div className="h-px" />;

const PreviewGallery: ForwardRefRenderFunction<
  unknown,
  {
    previewItems: PreviewGroupItem[];
    conversationID?: string;
  }
> = ({ previewItems: inputItems, conversationID }, ref) => {
  useMessagePrivacyRevision();
  const sourceOwner = useRef(messageOwner()).current;
  const previewItems = inputItems.filter(item => item.sourceMessage && !messagePrivacyState(item.sourceMessage, conversationID, sourceOwner).expired);
  const [albumCurrent, setAlbumCurrent] = useState(0);
  const [albumVisible, setAlbumVisible] = useState(false);
  const [selectedID,setSelectedID] = useState("");
  const selectAlbumCurrent=(index:number)=>{const item=previewItems[index];if (!item?.sourceMessage || !captureMessageView(item.sourceMessage, conversationID, sourceOwner).current()) return;setSelectedID(item.clientMsgID);setAlbumCurrent(index);};
  useEffect(()=>{if(albumVisible&&!previewItems.some(item=>item.clientMsgID===selectedID))setAlbumVisible(false);},[previewItems.map(item=>item.clientMsgID).join("|"),albumVisible,selectedID]);
  const imageCache = useUserStore((state) => state.imageCache);
  const updateDownloadTask = useMessageStore((state) => state.updateDownloadTask);

  const downloadOrShowFinder = (current: number) => {
    const item = previewItems[current];
    if (!item?.sourceMessage || !captureMessageRetention(item.sourceMessage, conversationID, sourceOwner).current()) return;
    const { url, clientMsgID } = item;
    const currentTask = getDownloadTask({
      downloadMap: useMessageStore.getState().downloadMap,
      compareKey: "clientMsgID",
      compareValue: clientMsgID,
    });
    if (currentTask?.downloadState === "pause") {
      window.electronAPI?.ipcInvoke("resumeDownload", currentTask?.downloadUrl);
      updateDownloadTask(currentTask?.downloadUrl ?? "", {
        downloadState: "downloading",
      });
      return;
    }
    if (!url.startsWith("http")) {
      window.electronAPI?.showInFinder(url.replace("file://", ""));
      return;
    }
    downloadFile(url, {
      clientMsgID,
      conversationID,
      isMediaMessage: true,
      saveType: "image",
      showError: true,
    });
  };

  useImperativeHandle(
    ref,
    () => ({
      setAlbumCurrent:selectAlbumCurrent,
      setAlbumVisible,
    }),
    [previewItems],
  );

  const previewList = previewItems.map((image) => {
    if (!window.electronAPI || image.url.startsWith("file://") || !image.thumbUrl) {
      return image.url;
    }
    return imageCache[image.thumbUrl ?? ""]
      ? `file://${imageCache[image.thumbUrl ?? ""]}`
      : image.thumbUrl;
  });

  return (
    <div style={{ display: "none" }}>
      <Image.PreviewGroup
        items={previewList}
        preview={{
          current: albumCurrent,
          visible: albumVisible && previewItems[albumCurrent]?.clientMsgID === selectedID,
          toolbarRender: (originalNode, { current }) => previewItems[current] && captureMessageRetention(previewItems[current].sourceMessage, conversationID, sourceOwner).current() ? (
            <PreviewToolsBar
              clientMsgID={previewItems[current].clientMsgID}
              dom={originalNode as JSX.Element}
              isFileDownloaded={!previewItems[current].url.startsWith("http")}
              onDownloadOrShowFinder={() => downloadOrShowFinder(current)}
            />
          ) : originalNode,
          onChange: selectAlbumCurrent,
          onVisibleChange: (vis) => setAlbumVisible(vis),
        }}
      ></Image.PreviewGroup>
    </div>
  );
};

const ForwardGallery = memo(forwardRef(PreviewGallery));
