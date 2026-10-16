import { randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { accountScope, AccountLease } from "../security/accountScope";
import { downloadURL, safeFilename } from "../security/policy";
import { handleTrusted } from "../security/ipc";
import { IpcMainToRender, IpcRenderToMain } from "../constants";

interface PendingDownload {
  lease: AccountLease;
  sender: Electron.WebContents;
  key: string;
  directory: string;
  randomName: boolean;
}
interface ActiveDownload extends PendingDownload {
  item: Electron.DownloadItem;
  savePath: string;
}
const getSourceUrl = (item: Electron.DownloadItem) =>
  downloadURL(item.getURLChain()[0]).toString();

export const initDownloadManage = (webContents: Electron.WebContents) => {
  // An unresolved request keeps its slot across account changes. Otherwise a late
  // will-download for A could be incorrectly paired with B's request for the URL.
  const pending = new Map<string, PendingDownload>();
  const active = new Set<ActiveDownload>();
  const requestKey = (sender: Electron.WebContents, url: string) =>
    `${sender.id}:${url}`;
  const notify = (download: PendingDownload, channel: string, ...args: unknown[]) => {
    if (accountScope.owns(download.lease) && !download.sender.isDestroyed())
      download.sender.send(channel, download.key, ...args);
  };
  const cleanup = (download: ActiveDownload) => {
    try {
      fs.rmSync(download.savePath, { force: true });
    } catch {
      /* Interrupted cleanup must not crash the main process. */
    }
  };
  const cancel = (download: ActiveDownload) => {
    active.delete(download);
    download.item.cancel();
    cleanup(download);
  };
  const unsubscribe = accountScope.onChange(() => {
    for (const download of [...active]) cancel(download);
  });

  const willDownload = (
    event: Electron.Event,
    item: Electron.DownloadItem,
    sender: Electron.WebContents,
  ) => {
    let slot: string;
    try {
      slot = requestKey(sender, getSourceUrl(item));
    } catch {
      event.preventDefault();
      return;
    }
    const download = pending.get(slot);
    if (!download) {
      event.preventDefault();
      return;
    }
    pending.delete(slot);
    if (!accountScope.owns(download.lease)) {
      event.preventDefault();
      return;
    }
    try {
      // HTTPS is required for every redirect, not just the requested URL.
      for (const url of item.getURLChain()) downloadURL(url);
      const savePath = path.join(
        download.directory,
        `${randomUUID()}-${safeFilename(item.getFilename())}`,
      );
      item.setSavePath(savePath);
      const record: ActiveDownload = { ...download, item, savePath };
      active.add(record);
      item.on("updated", (_, state) => {
        if (!accountScope.owns(record.lease)) {
          cancel(record);
          return;
        }
        if (state === "interrupted") notify(record, IpcMainToRender.downloadPaused);
        else if (state === "progressing" && !item.isPaused()) {
          const total = item.getTotalBytes();
          notify(
            record,
            IpcMainToRender.downloadProgress,
            total > 0
              ? Math.min(100, Math.round((item.getReceivedBytes() / total) * 100))
              : 0,
          );
        }
      });
      item.once("done", (_, state) => {
        active.delete(record);
        if (!accountScope.owns(record.lease) || state !== "completed") cleanup(record);
        notify(
          record,
          state === "completed"
            ? IpcMainToRender.downloadSuccess
            : IpcMainToRender.downloadFailed,
          savePath,
        );
      });
    } catch {
      event.preventDefault();
      notify(download, IpcMainToRender.downloadFailed);
    }
  };
  webContents.session.on("will-download", willDownload);

  handleTrusted(
    IpcRenderToMain.startDownload,
    (
      event,
      key: string,
      options: { sourceUrl?: string; saveType?: string; randomName?: boolean } = {},
      namespace?: string,
    ) => {
      if (
        !accountScope.matches(namespace) ||
        (event.sender !== webContents && !accountScope.ownsWindow(event.sender))
      )
        throw new Error("会话已切换");
      const source = downloadURL(options.sourceUrl || key);
      if (source.searchParams.has("is-update"))
        throw new Error("仅支持已签名安装包更新");
      const saveType = ["image", "video", "avatar"].includes(options.saveType)
        ? options.saveType
        : "file";
      const directory = global.pathConfig[`${saveType}CachePath`];
      if (!directory) throw new Error("请先登录后下载附件");
      const slot = requestKey(event.sender, source.toString());
      if (pending.has(slot))
        throw new Error("相同附件的前次下载仍在建立连接，请稍后重试");
      pending.set(slot, {
        lease: accountScope.lease(),
        sender: event.sender,
        key,
        directory,
        randomName: !!options.randomName,
      });
      try {
        event.sender.downloadURL(source.toString());
      } catch (error) {
        pending.delete(slot);
        throw error;
      }
    },
  );
  for (const [channel, operation] of [
    [IpcRenderToMain.pauseDownload, "pause"],
    [IpcRenderToMain.resumeDownload, "resume"],
    [IpcRenderToMain.cancelDownload, "cancel"],
  ] as const)
    handleTrusted(channel, (event, key: string, namespace?: string) => {
      if (
        !accountScope.matches(namespace) ||
        (event.sender !== webContents && !accountScope.ownsWindow(event.sender))
      )
        throw new Error("会话已切换");
      const record = [...active].find(
        (value) =>
          value.key === key &&
          value.sender === event.sender &&
          accountScope.owns(value.lease),
      );
      if (!record) return;
      if (operation === "cancel") cancel(record);
      else record.item[operation]();
    });
  return () => {
    unsubscribe();
    webContents.session.removeListener("will-download", willDownload);
    for (const download of [...active]) cancel(download);
    pending.clear();
  };
};
