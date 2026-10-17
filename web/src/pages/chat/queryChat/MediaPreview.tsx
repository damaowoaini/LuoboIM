import { captureMessageAccess, captureMessageRetention, captureMessageView, messageOwner, messagePrivacyState } from "@/core/messagePrivacyRuntime";
import { useMessagePrivacy, useMessagePrivacyRevision } from "@/hooks/useMessagePrivacy";
import type { JSX } from "react";
import { useLatest } from "ahooks";
import { Image, Spin } from "antd";
import {
  forwardRef,
  ForwardRefRenderFunction,
  memo,
  ReactNode,
  useEffect,
  useImperativeHandle,
  useState,
  useRef,
} from "react";

import PreviewToolsBar from "@/components/PreviewToolsBar";
import VideoPlayer from "@/components/VideoPlayer";
import { useConversationStore, useMessageStore, useUserStore } from "@/store";
import { PreviewGroupItem } from "@/store/type";
import { devObjectTransportURL } from "@/core/session/devObjectTransport";
import FileDownloadIcon from "@/svg/FileDownloadIcon";
import { downloadFile, getDownloadTask } from "@/utils/common";

const MediaPreview: ForwardRefRenderFunction<
  { showAlbum: (clientMsgID: string) => void },
  { conversationID: string }
> = ({ conversationID }, ref) => {
  const [albumVisible, setAlbumVisible] = useState(false);
  const latestVisible = useLatest(albumVisible);
  const [autoPlay, setAutoPlay] = useState(false);
  const [albumCurrent, setAlbumCurrent] = useState(0);
  useMessagePrivacyRevision();
  const sourceOwner = useRef(messageOwner()).current;
  const sourceAccess = captureMessageAccess(conversationID, sourceOwner);
  const allPreviewItems = useMessageStore((state) => state.previewImgList);
  const previewImgList = allPreviewItems.filter(item => item.sourceMessage && !messagePrivacyState(item.sourceMessage, conversationID, sourceOwner).expired);
  const [selectedID, setSelectedID] = useState("");
  const currentIsValid = previewImgList[albumCurrent]?.clientMsgID === selectedID;
  const updateDownloadTask = useMessageStore((state) => state.updateDownloadTask);
  const imageCache = useUserStore((state) => state.imageCache);
  const getConversationPreviewImgList = useMessageStore(
    (state) => state.getConversationPreviewImgList,
  );

  useEffect(() => {
    if (!albumVisible) return;
    const index = previewImgList.findIndex(item => item.clientMsgID === selectedID);
    if (index < 0) { setAlbumVisible(false); setAutoPlay(false); }
    else if (index !== albumCurrent) setAlbumCurrent(index);
  }, [previewImgList.map(item=>item.clientMsgID).join("|"), selectedID, albumVisible, albumCurrent]);

  const downloadOrShowFinder = (current: number) => {
    if (!sourceAccess.current()) return;
    const item = previewImgList[current];
    if (!item?.sourceMessage || !captureMessageRetention(item.sourceMessage, conversationID, sourceOwner).current()) return;
    const { url, videoUrl, clientMsgID } = item;
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
    const sourceUrl = videoUrl ?? url;
    if (!sourceUrl.startsWith("http")) {
      window.electronAPI?.showInFinder(sourceUrl.replace("file://", ""));
      return;
    }
    downloadFile(sourceUrl, {
      clientMsgID,
      conversationID,
      isMediaMessage: true,
      showError: true,
      saveType: videoUrl ? "video" : "image",
    });
  };

  const showAlbum = async (clientMsgID: string) => {
    if (!sourceAccess.current()) return;
    const access = captureMessageAccess(conversationID, sourceOwner);
    if (!access.current()) return;
    let current = previewImgList.findIndex((img) => img.clientMsgID === clientMsgID);
    let findItem = previewImgList[current];
    if (current < 0) {
      try { await getConversationPreviewImgList(); } catch { return; }
      current = useMessageStore
        .getState()
        .previewImgList.filter(item => item.sourceMessage && !messagePrivacyState(item.sourceMessage, conversationID, sourceOwner).expired).findIndex((img) => img.clientMsgID === clientMsgID);
      findItem = useMessageStore.getState().previewImgList.filter(item => item.sourceMessage && !messagePrivacyState(item.sourceMessage, conversationID, sourceOwner).expired)[current];
    }
    if (!access.current() || current < 0 || !findItem?.sourceMessage || !captureMessageView(findItem.sourceMessage, conversationID, sourceOwner).current()) return;
    const sourceUrl = findItem.videoUrl ?? findItem.url;
    if (window.electronAPI && sourceUrl.startsWith("http") && captureMessageRetention(findItem.sourceMessage, conversationID, sourceOwner).current()) {
      downloadFile(sourceUrl, {
        clientMsgID,
        conversationID,
        isMediaMessage: true,
        showError: true,
        saveType: findItem.videoUrl ? "video" : "image",
      });
    } else {
      setAutoPlay(true);
      setTimeout(() => setAutoPlay(false), 200);
    }
    setSelectedID(clientMsgID);
    setAlbumCurrent(current);
    setAlbumVisible(true);
  };

  const onToggle = (next: number) => {
    const item = previewImgList[next];
    if (!item?.sourceMessage || !captureMessageView(item.sourceMessage, conversationID, sourceOwner).current()) { setAlbumVisible(false); return; }
    setSelectedID(item.clientMsgID);
    const sourceUrl = previewImgList[next].videoUrl ?? previewImgList[next].url;
    if (window.electronAPI && sourceUrl.startsWith("http") && captureMessageRetention(item.sourceMessage, conversationID, sourceOwner).current()) {
      downloadFile(sourceUrl, {
        clientMsgID: previewImgList[next].clientMsgID,
        conversationID,
        isMediaMessage: true,
        showError: true,
        saveType: previewImgList[next].videoUrl ? "video" : "image",
      });
    }
    setAlbumCurrent(next);
  };

  useImperativeHandle(ref, () => ({ showAlbum }), [previewImgList]);

  const previewList = previewImgList.map((image) => {
    if (!window.electronAPI) return devObjectTransportURL(image.url);
    if (image.url.startsWith("file://") || !image.thumbUrl) {
      return image.url;
    }
    return imageCache[image.thumbUrl ?? ""]
      ? `file://${imageCache[image.thumbUrl ?? ""]}`
      : image.thumbUrl;
  });

  return (
    <div style={{ display: "none" }}>
      <Image.PreviewGroup
        preview={{
          current: albumCurrent,
          visible: albumVisible && currentIsValid,
          toolbarRender: (originalNode, { current }) => {
            const previewItem = previewImgList[current];
            if (!previewItem) return null;
            if (!captureMessageRetention(previewItem.sourceMessage, conversationID, sourceOwner).current()) return originalNode;
            const isFileDownloadedField = previewItem.videoUrl ? "videoUrl" : "url";
            const isFileDownloaded = Boolean(
              previewItem[isFileDownloadedField]?.startsWith("file://"),
            );
            return (
              <PreviewToolsBar
                clientMsgID={previewItem.clientMsgID}
                isFileDownloaded={isFileDownloaded}
                dom={originalNode as JSX.Element}
                hiddenImageAction={previewItem.videoUrl !== undefined}
                onDownloadOrShowFinder={() => downloadOrShowFinder(current)}
              />
            );
          },
          imageRender: (originNode, { current }) => previewImgList[current] ? (
            <PreviewItemRender
              dom={originNode}
              source={previewImgList[current]}
              autoplay={autoPlay}
            />
          ) : <span>消息已过期或已移除</span>,
          onVisibleChange: (vis) => setAlbumVisible(vis),
          onChange: onToggle,
        }}
        items={previewList}
      />
    </div>
  );
};

const ForwardMediaPreview = memo(forwardRef(MediaPreview));

export default ForwardMediaPreview;

const PreviewItemRender = ({
  dom,
  source,
  autoplay,
}: {
  dom: ReactNode;
  source: PreviewGroupItem;
  autoplay: boolean;
}) => {
  const sourceOwner = useRef(messageOwner()).current;
  const updateDownloadTask = useMessageStore((state) => state.updateDownloadTask);
  const privacy = useMessagePrivacy(source.sourceMessage);
  const currentTask = useMessageStore((state) =>
    getDownloadTask({
      downloadMap: state.downloadMap,
      compareKey: "clientMsgID",
      compareValue: source.clientMsgID,
    }),
  );

  useEffect(() => {
    const el = document.getElementsByClassName(
      "ant-image-preview-img",
    )[0] as HTMLImageElement | null;
    if (!el) return;
    const loaded = () => { el.style.display = "block"; };
    el.addEventListener("load", loaded);
    if (el.complete) {
      el.style.display = "block";
    }
    return () => {
      if (!el) return;

      el.removeEventListener("load", loaded);
      el.style.display = "none";
    };
  }, [source.clientMsgID]);

  const tryPlayVideo = () => {
    if (!source.sourceMessage || !captureMessageRetention(source.sourceMessage,"",sourceOwner).current()) return;
    if (
      currentTask?.downloadState === "downloading" ||
      currentTask?.downloadState === "resume"
    ) {
      window.electronAPI?.ipcInvoke("pauseDownload", currentTask.downloadUrl!);
      updateDownloadTask(currentTask.downloadUrl!, {
        downloadState: "pause",
      });
      return;
    }

    if (currentTask?.downloadState === "pause") {
      window.electronAPI?.ipcInvoke("resumeDownload", currentTask.downloadUrl!);
      updateDownloadTask(currentTask.downloadUrl!, {
        downloadState: "downloading",
      });
      return;
    }

    if (!currentTask && source.videoUrl) {
      downloadFile(source.videoUrl, {
        isMediaMessage: true,
        showError: true,
        saveType: "video",
        clientMsgID: source.clientMsgID,
        conversationID:
          useConversationStore.getState().currentConversation?.conversationID,
      });
    }
  };

  if (privacy.expired) return <span>消息已过期或已移除</span>;
  if (!source.videoUrl) {
    return (
      <>
        {dom}
        <div className="absolute -z-10 flex items-center justify-center">
          <Spin spinning />
        </div>
      </>
    );
  }

  const isVideoDownloaded = source.videoUrl?.startsWith("file://");

  if (source.videoUrl?.startsWith("file://") || !window.electronAPI || !privacy.allowsRetention) {
    return (
      <div className="relative">
        <VideoPlayer url={source.videoUrl} autoplay={autoplay} />
      </div>
    );
  }

  return (
    <>
      {dom}
      <div
        className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 cursor-pointer"
        onClick={tryPlayVideo}
      >
        <FileDownloadIcon
          size={40}
          pausing={currentTask?.downloadState === "pause"}
          finished={isVideoDownloaded || !window.electronAPI}
          percent={currentTask?.progress ?? 0}
        />
      </div>
      <div className="absolute -z-10 flex items-center justify-center">
        <Spin spinning />
      </div>
    </>
  );
};
