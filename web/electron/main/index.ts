import { app } from "electron";
import { join } from "node:path";
import { createMainWindow } from "./windowManage";
import { createTray } from "./trayManage";
import { setIpcMainListener } from "./ipcHandlerManage";
import {
  checkPreferences,
  performAppStartup,
  setAppGlobalData,
  setAppListener,
  setSingleInstance,
} from "./appManage";
import createAppMenu from "./menuManage";
import { isLinux } from "../utils";
import screenshotsManage from "./screenshotsManage";
import { getLogger } from "../utils/log";
import { initI18n } from "../i18n";

export const logger = getLogger(join(app.getPath("userData"), `/LuoboDuihua/logs`));

const init = async () => {
  await initI18n();
  createMainWindow();
  createAppMenu();
  createTray();
  screenshotsManage();
};

setAppGlobalData();
performAppStartup();
setIpcMainListener();
setSingleInstance();
setAppListener(init);

app.whenReady().then(() => {
  isLinux ? setTimeout(init, 300) : init();
  // Request microphone/camera access only from an explicit call/capture action.
});
