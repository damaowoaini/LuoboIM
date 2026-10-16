import { ipcMain } from 'electron';
import { isTrustedDocument } from './policy';
function trusted(event: Electron.IpcMainEvent | Electron.IpcMainInvokeEvent) {
  return event.senderFrame === event.sender.mainFrame && isTrustedDocument(event.sender.getURL(), global.pathConfig.indexHtml, process.env.VITE_DEV_SERVER_URL);
}
export function handleTrusted(channel: string, listener: (event: Electron.IpcMainInvokeEvent,...args: any[]) => any) {
  ipcMain.handle(channel,(event,...args) => { if (!trusted(event)) throw new Error('拒绝非应用页面的桌面请求'); return listener(event,...args); });
}
export function onTrusted(channel: string, listener: (event: Electron.IpcMainEvent,...args: any[]) => void) {
  ipcMain.on(channel,(event,...args) => { if (!trusted(event)) { event.returnValue = undefined; return; } listener(event,...args); });
}
