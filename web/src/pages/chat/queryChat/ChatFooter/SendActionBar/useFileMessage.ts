import { sessionCoordinator } from "@/core/session/browser";
import { StaleSessionError } from "@/core/session/session";
import { v4 as uuidV4 } from "uuid";

import { IMSDK } from "@/layout/MainContentWrap";
import { ExMessageItem } from "@/store";
import { base64toFile } from "@/utils/common";

export interface FileWithPath extends File {
  path?: string;
}

export function useFileMessage() {
  const generation = sessionCoordinator.generation;
  const assertSession = () => { if (generation !== sessionCoordinator.generation) throw new StaleSessionError(); };
  const getImageMessage = async (file: FileWithPath, check = () => {}) => {
    assertSession(); check();
    const { width, height } = await getPicInfo(file);
    assertSession(); check();
    const baseInfo = {
      uuid: uuidV4(),
      type: file.type,
      size: file.size,
      width,
      height,
      url: URL.createObjectURL(file),
    };
    const sourcePath =
      (await window.electronAPI?.saveFileToDisk({
        file,
        sync: true,
        type: "sentFileCache",
      })) || `/${file.name}`;
    const options = {
      sourcePicture: baseInfo,
      bigPicture: baseInfo,
      snapshotPicture: baseInfo,
      sourcePath,
      file,
    };

    assertSession(); check();
    return (await IMSDK.createImageMessageByFile(options)).data;
  };

  const getVideoMessage = async (file: FileWithPath, snapShotFile: FileWithPath, check = () => {}) => {
    assertSession(); check();
    const { width, height } = await getPicInfo(snapShotFile);
    assertSession(); check();
    const snapshotPath =
      (await window.electronAPI?.saveFileToDisk({
        sync: true,
        file: snapShotFile,
        type: "sentFileCache",
      })) || `/${snapShotFile.name}`;
    assertSession(); check();
    const videoPath =
      (await window.electronAPI?.saveFileToDisk({
        file,
        type: "sentFileCache",
      })) || `/${file.name}`;
    assertSession(); check();
    const duration = await getMediaDuration(URL.createObjectURL(file));
    assertSession(); check();
    const options = {
      videoFile: file,
      snapshotFile: snapShotFile,
      videoPath,
      duration,
      videoType: file.type,
      snapshotPath,
      videoUUID: uuidV4(),
      videoUrl: "",
      videoSize: file.size,
      snapshotUUID: uuidV4(),
      snapshotSize: snapShotFile.size,
      snapshotUrl: URL.createObjectURL(snapShotFile),
      snapshotWidth: width,
      snapshotHeight: height,
      snapShotType: snapShotFile.type,
    };
    assertSession(); check();
    return (await IMSDK.createVideoMessageByFile(options)).data;
  };

  const getFileMessage = async (file: FileWithPath, check = () => {}) => {
    assertSession(); check();
    const filePath =
      (await window.electronAPI?.saveFileToDisk({
        file,
        sync: true,
        type: "sentFileCache",
      })) || `/${file.name}`;
    assertSession(); check();
    const sourceFile = (await window.electronAPI?.getFileByPath(filePath)) || file;
    assertSession(); check();
    const options = {
      file: sourceFile,
      filePath,
      fileName: file.name,
      uuid: uuidV4(),
      sourceUrl: "",
      fileSize: file.size,
      fileType: file.type,
    };
    assertSession(); check();
    return (await IMSDK.createFileMessageByFile(options)).data;
  };

  const createFileMessage = async (file: FileWithPath, check = () => {}): Promise<ExMessageItem> => {
    assertSession(); check();
    const isImage = file.type.includes("image");
    const isVideo = file.type.includes(window.electronAPI ? "video" : "mp4");
    if (isImage) {
      return await getImageMessage(file, check);
    }
    if (isVideo) {
      const snapShotFile = await getVideoSnshotFile(file);
      assertSession(); check();
      return await getVideoMessage(file, snapShotFile, check);
    }
    return await getFileMessage(file, check);
  };

  const getFileType = (name: string) => {
    const idx = name.lastIndexOf(".");
    return name.slice(idx + 1);
  };

  const getPicInfo = (file: File): Promise<HTMLImageElement> => new Promise((resolve,reject) => {
    const url = URL.createObjectURL(file); const image = new Image();
    const timer = setTimeout(()=>{cleanup();reject(new Error("图片读取超时"));},15000);
    const cleanup = () => {clearTimeout(timer);URL.revokeObjectURL(url);image.onload=null;image.onerror=null;};
    image.onload=()=>{cleanup();resolve(image);}; image.onerror=()=>{cleanup();reject(new Error("图片无法读取"));};image.src=url;
  });
  const getVideoSnshotFile = (file: File): Promise<File> => new Promise((resolve,reject) => {
    const url = URL.createObjectURL(file);const video=document.createElement("video");video.muted=true;video.preload="auto";video.playsInline=true;
    const timer=setTimeout(()=>{cleanup();reject(new Error("视频读取超时"));},20000);
    const cleanup=()=>{clearTimeout(timer);video.onloadeddata=null;video.onerror=null;video.pause();video.removeAttribute("src");video.load();URL.revokeObjectURL(url);};
    video.onerror=()=>{cleanup();reject(new Error("此视频无法读取，请作为文件发送"));};
    video.onloadeddata=()=>{
      try {const canvas=document.createElement("canvas");canvas.width=video.videoWidth;canvas.height=video.videoHeight;const context=canvas.getContext("2d");if(!context||!canvas.width||!canvas.height)throw new Error("无法读取视频画面");context.drawImage(video,0,0);const snapshot=base64toFile(canvas.toDataURL("image/png"));cleanup();resolve(snapshot);}catch(error){cleanup();reject(error);}
    };video.src=url;video.load();
  });
  const getMediaDuration = (source: string): Promise<number> => new Promise((resolve,reject)=>{
    const media=new Audio();media.preload="metadata";
    const timer=setTimeout(()=>{cleanup();reject(new Error("媒体时长读取超时"));},15000);
    const cleanup=()=>{clearTimeout(timer);media.onloadedmetadata=null;media.onerror=null;media.removeAttribute("src");media.load();URL.revokeObjectURL(source);};
    media.onloadedmetadata=()=>{const duration=Math.round(media.duration);cleanup();Number.isFinite(duration)?resolve(duration):reject(new Error("媒体时长无效"));};media.onerror=()=>{cleanup();reject(new Error("媒体无法读取"));};media.src=source;
  });

  return {
    getImageMessage,
    getVideoMessage,
    getFileMessage,
    createFileMessage,
    getPicInfo,
    getVideoSnshotFile,
  };
}
