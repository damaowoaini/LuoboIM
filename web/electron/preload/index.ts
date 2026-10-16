import { randomUUID } from "node:crypto";
import { isWithinRoot, safeFilename } from "../security/policy";
import { IpcMainToRender, IpcRenderToMain } from "../constants";
import fs from "fs";
import path from "path";
import os from "os";
import { DataPath, IElectronAPI } from "./../../src/types/globalExpose.d";
import { contextBridge, ipcRenderer, shell } from "electron";
import { isProd } from "../utils";

const getPlatform = () => {
  if (process.platform === "darwin") {
    return 4;
  }
  if (process.platform === "win32") {
    return 3;
  }
  return 7;
};

const getOsArch = () => os.arch();

const getDataPath = (key: DataPath) => {
  switch (key) {
    case "public":
      return isProd ? ipcRenderer.sendSync("getDataPath", "public") : "";
    case "extraResources":
      return isProd ? ipcRenderer.sendSync("getDataPath", "extraResources") : "";
    default:
      return "";
  }
};

const allowedInvoke = new Set([
  ...Object.values(IpcRenderToMain),
  "changeLanguage",
  "main-win-ready",
]);
const allowedEvents = new Set([
  ...Object.values(IpcMainToRender),
  "main-process-message",
]);
const allowedSync = new Set(["getKeyStoreSync", "getDataPath", "setUserCachePath"]);
let sessionNamespace: string | undefined;
let sessionGeneration = 0;
const scopedChannels = new Set([
  "startDownload",
  "pauseDownload",
  "resumeDownload",
  "cancelDownload",
  "saveAttachmentAs",
  "dragFile",
  "capture",
  "callDesktopSession",
  "openChildWindow",
  "pushDesktopSessionState",
]);
const roots = (): string[] =>
  ipcRenderer.sendSync("getAllowedFileRootsSync", sessionNamespace);
const requirePath = (value: string): string => {
  if (!isWithinRoot(value, roots())) throw new Error("文件不属于当前账号缓存");
  return value;
};
const subscribe = (channel: string, callback: (...args: any[]) => void) => {
  if (!allowedEvents.has(channel)) throw new Error("无效的桌面事件");
  const subscription = (_, ...args) => callback(...args);
  ipcRenderer.on(channel, subscription);
  return () => ipcRenderer.removeListener(channel, subscription);
};

const subscribeOnce = (channel: string, callback: (...args: any[]) => void) => {
  if (!allowedEvents.has(channel)) throw new Error("无效的桌面事件");
  ipcRenderer.once(channel, (_, ...args) => callback(...args));
};

const unsubscribeAll = (channel: string) => {
  if (allowedEvents.has(channel)) ipcRenderer.removeAllListeners(channel);
};

const ipcInvoke = (channel: string, ...arg: any) => {
  if (!allowedInvoke.has(channel)) return Promise.reject(new Error("无效的桌面操作"));
  return ipcRenderer.invoke(
    channel,
    ...arg,
    ...(scopedChannels.has(channel) ? [sessionNamespace] : []),
  );
};

const ipcSendSync = (channel: string, ...arg: any) => {
  if (!allowedSync.has(channel)) throw new Error("无效的桌面操作");
  if (channel === "setUserCachePath") {
    const next = arg[0] || undefined;
    if (next !== sessionNamespace) sessionGeneration += 1;
    sessionNamespace = next;
  }
  return ipcRenderer.sendSync(
    channel,
    ...arg,
    ...(channel === "getDataPath" ? [sessionNamespace] : []),
  );
};

const fileExists = (path: string) => {
  return isWithinRoot(path, roots()) && fs.existsSync(path);
};

const openFile = (path: string) => {
  shell.openPath(requirePath(path));
};

const showInFinder = (path: string) => {
  shell.showItemInFolder(requirePath(path));
};

const getTempFileURL = (path: string) => {
  const file = fs.readFileSync(requirePath(path));
  const bolb = new Blob([file]);
  return URL.createObjectURL(bolb);
};

const saveFileToDisk = async ({
  file,
  type,
  sync,
}: {
  file: File;
  type: "fileCache" | "sentFileCache";
  sync?: boolean;
}): Promise<string> => {
  if (!["fileCache", "sentFileCache"].includes(type))
    throw new Error("无效的文件缓存类型");
  const saveDir = ipcRenderer.sendSync("getDataPath", type, sessionNamespace);
  if (!saveDir) throw new Error("请先登录后保存附件");
  const generation = sessionGeneration;
  const arrayBuffer = await file.arrayBuffer();
  if (
    generation !== sessionGeneration ||
    saveDir !== ipcRenderer.sendSync("getDataPath", type, sessionNamespace)
  )
    throw new Error("账号已变更，请重新选择附件");
  const savePath = path.join(saveDir, `${randomUUID()}-${safeFilename(file.name)}`);
  if (!fs.existsSync(saveDir)) {
    fs.mkdirSync(saveDir, { recursive: true });
  }
  if (sync) {
    await fs.promises.writeFile(savePath, Buffer.from(arrayBuffer));
  } else {
    await fs.promises.writeFile(savePath, Buffer.from(arrayBuffer));
  }
  return savePath;
};

const getFileByPath = async (filePath: string) => {
  try {
    requirePath(filePath);
    const generation = sessionGeneration;
    const filename = path.basename(filePath);
    const data = await fs.promises.readFile(filePath);
    if (generation !== sessionGeneration) throw new Error("会话已切换");
    requirePath(filePath);
    return new File([data], filename);
  } catch {
    return null;
  }
};

const Api: IElectronAPI = {
  getDataPath,
  getVersion: () => process.version,
  getPlatform,
  getOsArch,
  getSystemVersion: process.getSystemVersion,
  subscribe,
  subscribeOnce,
  unsubscribeAll,
  ipcInvoke,
  ipcSendSync,
  fileExists,
  openFile,
  showInFinder,
  getTempFileURL,
  saveFileToDisk,
  getFileByPath,
};

contextBridge.exposeInMainWorld("electronAPI", Api);
