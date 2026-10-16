import { handleTrusted } from "../security/ipc";
import { accountScope, AccountLease } from "../security/accountScope";
import { clipboard, ClipboardItem, globalShortcut } from "electron";
import { t } from "i18next";
import Screenshots from "electron-screenshots";
import {
  hideWindow,
  isShowMainWindow,
  sendEvent,
  showWindow,
  toggleWindowVisible,
  getWebContents,
} from "./windowManage";
import { IpcMainToRender, IpcRenderToMain } from "../constants";
import { isWin } from "../utils";

const checkWindowVisible = () => {
  if (isWin) toggleWindowVisible(true);
  else if (!isShowMainWindow()) showWindow();
};
export default () => {
  let screenshots: Screenshots | undefined;
  let owner: AccountLease | undefined;
  // The capture renderer and native library are created only for an explicit
  // authenticated capture request, not while the login page is starting.
  const getScreenshots = () => {
    if (screenshots) return screenshots;
    screenshots = new Screenshots({
      singleWindow: true,
      lang: { magnifier_position_label: t("system.magnifier_position_label") },
    });
    screenshots.on("windowCreated", ($win) => {
      $win.on("focus", () =>
        globalShortcut.register("esc", () => {
          if ($win?.isFocused()) screenshots?.endCapture();
        }),
      );
      $win.on("blur", () => globalShortcut.unregister("esc"));
    });
    screenshots.on("cancel", () => {
      checkWindowVisible();
      if (owner && accountScope.owns(owner)) sendEvent(IpcMainToRender.captureResponse);
      owner = undefined;
    });
    screenshots.on("ok", (event, array) => {
      event.preventDefault(); // The library default calls Electron's removed writeImage API.
      screenshots?.endCapture();
      const capturedOwner = owner;
      owner = undefined;
      checkWindowVisible();
      if (!capturedOwner || !accountScope.owns(capturedOwner)) return;
      void clipboard
        .write([new ClipboardItem({ "image/png": new Blob([Uint8Array.from(array)]) })])
        .catch(() => undefined);
      const image = `data:image/png;base64,${Buffer.from(array).toString("base64")}`;
      if (accountScope.owns(capturedOwner))
        sendEvent(IpcMainToRender.captureResponse, image);
    });
    return screenshots;
  };
  accountScope.onChange(() => {
    if (!owner || accountScope.owns(owner)) return;
    owner = undefined;
    screenshots?.endCapture();
    checkWindowVisible();
  });
  handleTrusted(IpcRenderToMain.capture, (event, hidden, namespace?: string) => {
    if (
      !accountScope.matches(namespace) ||
      (event.sender !== getWebContents() && !accountScope.ownsWindow(event.sender))
    )
      throw new Error("请先登录后截图");
    owner = accountScope.lease();
    global.hiddenWindow = hidden;
    if (hidden) isWin ? toggleWindowVisible(false) : hideWindow();
    getScreenshots().startCapture();
  });
};
