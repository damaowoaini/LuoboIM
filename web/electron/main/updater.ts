import fs from "node:fs/promises";
import path from "node:path";
import { app } from "electron";
import { autoUpdater } from "electron-updater";
import { downloadURL } from "../security/policy";
let configured = false;
let available = false;
let downloaded = false;
async function updater() {
  if (!app.isPackaged) throw new Error("开发构建不安装更新");
  if (!["darwin", "win32"].includes(process.platform))
    throw new Error("此平台请使用正式签名安装包更新");
  if (!configured) {
    const feed = downloadURL(process.env.LUOBO_UPDATE_URL || "");
    const publishers = (process.env.LUOBO_UPDATE_PUBLISHERS || "")
      .split(",")
      .map((value) => value.trim())
      .filter(Boolean);
    if (process.platform === "win32" && !publishers.length)
      throw new Error("发布端尚未配置更新签名发布者");
    const config = path.join(app.getPath("userData"), "luobo-updater.json");
    await fs.writeFile(
      config,
      JSON.stringify({
        provider: "generic",
        url: feed.toString(),
        publisherName: publishers,
      }),
      { mode: 0o600 },
    );
    autoUpdater.updateConfigPath = config;
    autoUpdater.autoDownload = false;
    autoUpdater.autoInstallOnAppQuit = false;
    autoUpdater.on("update-available", () => {
      available = true;
    });
    autoUpdater.on("update-not-available", () => {
      available = false;
    });
    autoUpdater.on("update-downloaded", () => {
      downloaded = true;
    });
    configured = true;
  }
  return autoUpdater;
}
export async function checkSignedUpdate() {
  if (
    !app.isPackaged ||
    !process.env.LUOBO_UPDATE_URL ||
    !["darwin", "win32"].includes(process.platform)
  )
    return { available: false, version: "", releaseNotes: "", supported: false };
  const active = await updater();
  const result = await active.checkForUpdates();
  return {
    available,
    version: result?.updateInfo.version || "",
    releaseNotes: result?.updateInfo.releaseNotes || "",
  };
}
export async function downloadSignedUpdate() {
  const active = await updater();
  if (!available) throw new Error("请先检查可用更新");
  await active.downloadUpdate();
  return true;
}
export async function installSignedUpdate() {
  const active = await updater();
  if (!downloaded) throw new Error("更新尚未下载并完成签名校验");
  active.quitAndInstall(false, true);
  return true;
}
