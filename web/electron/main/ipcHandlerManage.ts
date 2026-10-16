import { initSessionBridge } from "./sessionBridge";
import { accountScope } from "../security/accountScope";
import { isWithinRoot } from "../security/policy";
import { exportAttachment } from "../security/attachmentExport";
import {
  checkSignedUpdate,
  downloadSignedUpdate,
  installSignedUpdate,
} from "./updater";
import { handleTrusted, onTrusted } from "../security/ipc";
import fs from "fs";
import {
  BrowserWindow,
  Menu,
  app,
  desktopCapturer,
  dialog,
  ipcMain,
  shell,
} from "electron";
import { join } from "path";
import zipUtil from "adm-zip";
import {
  clearCache,
  closeWindow,
  createChildWindow,
  getWebContents,
  hotReload,
  minimize,
  showWindow,
  splashEnd,
  taskFlicker,
  updateMaximize,
} from "./windowManage";
import { t } from "i18next";
import { IpcMainToRender, IpcRenderToMain } from "../constants";
import { getStore } from "./storeManage";
import { flicker, setTrayTitle } from "./trayManage";
import { setUserCachePath } from "./appManage";

import { changeLanguage } from "../i18n";

const childWindowMap: { [key: string]: number } = {};

const store = getStore();

export const clearChildWindows = () => {
  for (const key in childWindowMap) {
    const childWindow = BrowserWindow.getAllWindows().find(
      (win) => win.id === childWindowMap[key],
    );
    if (childWindow && !childWindow.isDestroyed()) childWindow.close();
    delete childWindowMap[key];
  }
};

export const openChildWindowHandle = (props) => {
  const { arg, search, key, options } = props;
  if (!childWindowMap[key]) {
    const childWindow = createChildWindow(arg, options, search);
    accountScope.bindWindow(childWindow.webContents);
    childWindowMap[key] = childWindow.id;
    childWindow.once("closed", () => {
      delete childWindowMap[key];
    });
    return;
  }

  const childWindow = BrowserWindow.getAllWindows().find(
    (win) => win.id === childWindowMap[key],
  );
  if (childWindow) {
    if (childWindow.isMinimized()) {
      childWindow.restore();
    }
    if (childWindow.isVisible()) {
      childWindow.focus();
    } else {
      childWindow.show();
    }
  }
};

export const setIpcMainListener = () => {
  initSessionBridge();
  handleTrusted(IpcRenderToMain.saveAttachmentAs, async (event, sourcePath: string, filename: string, namespace?: string) => {
    const lease = accountScope.lease();
    const assertOwner = () => {
      if (!accountScope.matches(namespace) || !accountScope.owns(lease) ||
          (event.sender !== getWebContents() && !accountScope.ownsWindow(event.sender)))
        throw new Error("会话已切换");
    };
    return exportAttachment({ sourcePath, filename, assertOwner,
      roots: [global.pathConfig.fileCachePath, global.pathConfig.sentFileCachePath,
        global.pathConfig.imageCachePath, global.pathConfig.videoCachePath,
        global.pathConfig.avatarCachePath].filter(Boolean),
      chooseDestination: async (defaultPath) => {
        const window = BrowserWindow.fromWebContents(event.sender);
        if (!window || window.isDestroyed()) throw new Error("应用窗口已关闭");
        const result = await dialog.showSaveDialog(window, { title: "附件另存为", defaultPath,
          properties: ["createDirectory", "showOverwriteConfirmation"] });
        return result.canceled ? undefined : result.filePath;
      },
    });
  });
  handleTrusted(IpcRenderToMain.clearSession, () => {
    clearCache();
  });

  // window manage
  handleTrusted("changeLanguage", (_, locale) => {
    if (!["zh-CN", "en-US"].includes(locale)) throw new Error("无效语言");
    store.set("language", locale);
    changeLanguage(locale).then(() => {
      app.relaunch();
      app.exit(0);
    });
  });
  handleTrusted("main-win-ready", () => {
    splashEnd();
  });
  handleTrusted(IpcRenderToMain.showMainWindow, () => {
    showWindow();
  });
  handleTrusted(IpcRenderToMain.openChildWindow, (event, props, namespace) => {
    if (
      !accountScope.matches(namespace) ||
      (event.sender !== getWebContents() && !accountScope.ownsWindow(event.sender))
    )
      throw new Error("会话已切换");
    openChildWindowHandle(props);
  });
  handleTrusted(IpcRenderToMain.minimizeWindow, (_, key) => {
    if (!key) {
      minimize();
      return;
    }
    const childWindow = BrowserWindow.getAllWindows().find(
      (win) => win.id === childWindowMap[key],
    );
    if (childWindow) {
      childWindow.minimize();
    }
  });
  handleTrusted(IpcRenderToMain.maxmizeWindow, (_, key) => {
    if (!key) {
      updateMaximize();
      return;
    }
    const childWindow = BrowserWindow.getAllWindows().find(
      (win) => win.id === childWindowMap[key],
    );
    if (childWindow) {
      if (childWindow.isMaximized()) {
        childWindow.unmaximize();
      } else {
        childWindow.maximize();
      }
    }
  });
  handleTrusted(IpcRenderToMain.closeWindow, (_, key) => {
    if (!key) {
      closeWindow();
      return;
    }
    const childWindow = BrowserWindow.getAllWindows().find(
      (win) => win.id === childWindowMap[key],
    );
    if (childWindow.isDestroyed()) {
      delete childWindowMap[key];
    }
    if (childWindow && !childWindow.isDestroyed()) {
      childWindow.close();
      delete childWindowMap[key];
    }
  });
  handleTrusted(IpcRenderToMain.showMessageBox, (_, options) => {
    return dialog
      .showMessageBox(BrowserWindow.getFocusedWindow(), options)
      .then((res) => res.response);
  });

  // data transfer
  handleTrusted(IpcRenderToMain.transferChooseModalData, (_, { key, data }) => {
    let targetWebContents: Electron.WebContents;
    if (!key) {
      targetWebContents = getWebContents();
    } else {
      targetWebContents = BrowserWindow.getAllWindows().find(
        (win) => win.id === childWindowMap[key],
      ).webContents;
    }
    if (targetWebContents) {
      targetWebContents.send(IpcMainToRender.transferChooseModalData, data);
    }
  });
  handleTrusted(IpcRenderToMain.getContactStoreData, (_, { key }) => {
    const targetWebContents = getWebContents();
    targetWebContents.send(IpcMainToRender.getContactStoreData, key);
  });
  handleTrusted(IpcRenderToMain.transferContactStoreData, (_, { key, data }) => {
    const targetWebContents = BrowserWindow.getAllWindows().find(
      (win) => win.id === childWindowMap[key],
    )?.webContents;
    if (targetWebContents) {
      targetWebContents.send(IpcMainToRender.transferContactStoreData, data);
    }
  });
  handleTrusted(IpcRenderToMain.setKeyStore, (_, { key, data }) => {
    if (key !== "closeAction" || !["miniSize", "quit"].includes(data))
      throw new Error("无效的设置项");
    store.set(key, data);
  });
  handleTrusted(IpcRenderToMain.getKeyStore, (_, { key }) => {
    if (!["closeAction", "language"].includes(key)) throw new Error("无效的设置项");
    return store.get(key);
  });
  onTrusted(IpcRenderToMain.getKeyStoreSync, (e, { key }) => {
    e.returnValue = ["closeAction", "language"].includes(key)
      ? store.get(key)
      : undefined;
  });
  handleTrusted(IpcRenderToMain.showInputContextMenu, () => {
    const menu = Menu.buildFromTemplate([
      {
        label: t("system.copy"),
        type: "normal",
        role: "copy",
        accelerator: "CommandOrControl+c",
      },
      {
        label: t("system.paste"),
        type: "normal",
        role: "paste",
        accelerator: "CommandOrControl+v",
      },
      {
        label: t("system.selectAll"),
        type: "normal",
        role: "selectAll",
        accelerator: "CommandOrControl+a",
      },
    ]);
    menu.popup({
      window: BrowserWindow.getFocusedWindow()!,
    });
  });
  handleTrusted(IpcRenderToMain.updateUnreadCount, (_, count) => {
    app.setBadgeCount(count);
    setTrayTitle(count);
    flicker(count > 0);
    if (count > 0) {
      taskFlicker();
    }
  });
  handleTrusted(IpcRenderToMain.checkForUpdate, () => checkSignedUpdate());
  handleTrusted(IpcRenderToMain.downloadUpdate, () => downloadSignedUpdate());
  handleTrusted(IpcRenderToMain.quitAndUpdate, () => installSignedUpdate());
  handleTrusted(IpcRenderToMain.appUpdate, async () => {
    throw new Error("旧热更新未验证签名，已停用。请使用正式签名安装包更新。");
  });
  onTrusted("getAllowedFileRootsSync", (event, namespace) => {
    event.returnValue =
      accountScope.matches(namespace) &&
      (event.sender === getWebContents() || accountScope.ownsWindow(event.sender))
        ? [
            global.pathConfig.fileCachePath,
            global.pathConfig.sentFileCachePath,
            global.pathConfig.imageCachePath,
            global.pathConfig.videoCachePath,
            global.pathConfig.avatarCachePath,
            global.pathConfig.logsPath,
          ].filter(Boolean)
        : [];
  });
  onTrusted(IpcRenderToMain.setUserCachePath, (event, namespace) => {
    // Child windows may restore their context but cannot replace the main account.
    if (event.sender === getWebContents()) {
      const changed = !namespace || !accountScope.matches(namespace);
      setUserCachePath(namespace || undefined);
      if (changed) clearChildWindows();
    }
    event.returnValue = accountScope.matches(namespace);
  });
  onTrusted(IpcRenderToMain.getDataPath, (e, key: string, namespace?: string) => {
    if (
      ["fileCache", "sentFileCache"].includes(key) &&
      (!accountScope.matches(namespace) ||
        (e.sender !== getWebContents() && !accountScope.ownsWindow(e.sender)))
    ) {
      e.returnValue = undefined;
      return;
    }
    switch (key) {
      case "public":
        e.returnValue = global.pathConfig.publicPath;
        break;
      case "fileCache":
        e.returnValue = global.pathConfig.fileCachePath;
        break;
      case "sentFileCache":
        e.returnValue = global.pathConfig.sentFileCachePath;
        break;
      case "extraResources":
        e.returnValue = global.pathConfig.extraResourcesPath;
        break;
      default:
        e.returnValue = global.pathConfig.publicPath;
        break;
    }
  });
  handleTrusted(IpcRenderToMain.dragFile, (e, filePath, namespace) => {
    const roots = [
      global.pathConfig.fileCachePath,
      global.pathConfig.sentFileCachePath,
      global.pathConfig.imageCachePath,
      global.pathConfig.videoCachePath,
      global.pathConfig.avatarCachePath,
    ].filter(Boolean);
    if (
      !accountScope.matches(namespace) ||
      (e.sender !== getWebContents() && !accountScope.ownsWindow(e.sender)) ||
      !isWithinRoot(filePath, roots)
    )
      throw new Error("文件不属于当前账号缓存");
    e.sender.startDrag({
      file: filePath,
      icon: global.pathConfig.trayIcon,
    });
  });
  handleTrusted(IpcRenderToMain.showLogsInFinder, () => {
    shell.openPath(global.pathConfig.logsPath);
  });
  handleTrusted(IpcRenderToMain.prepareUploadLogs, async () => {
    const logsPath = global.pathConfig.logsPath;
    const zip = new zipUtil();
    zip.addLocalFolder(logsPath);
    let date = new Date();
    let dateStr =
      date.getFullYear() + "-" + (date.getMonth() + 1) + "-" + date.getDate();
    const zipPath = join(global.pathConfig.logsPath, `${dateStr}electronlog.zip`);
    await zip.writeZipPromise(zipPath);
    return zipPath;
  });
  handleTrusted(IpcRenderToMain.hotRelaunch, hotReload);

  // screen share
  handleTrusted(IpcRenderToMain.getScreenSource, async () => {
    const sources = await desktopCapturer.getSources({ types: ["screen"] });
    return sources[0]?.id;
  });
};
