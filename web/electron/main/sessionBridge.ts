import { randomUUID } from "node:crypto";
import { BrowserWindow } from "electron";
import { accountScope, AccountLease } from "../security/accountScope";
import { handleTrusted } from "../security/ipc";
import { isTrustedDocument } from "../security/policy";
import { getWebContents } from "./windowManage";

const methods = new Set([
  "snapshot",
  "getBlackList",
  "removeBlack",
  "setGlobalRecvMessageOpt",
  "deleteAllMsgFromLocalAndSvr",
  "getSpecifiedGroupMembersInfo",
]);
interface Pending {
  owner: AccountLease;
  requester: Electron.WebContents;
  resolve(value: unknown): void;
  reject(error: Error): void;
  timeout: ReturnType<typeof setTimeout>;
}
export function initSessionBridge(timeoutMs = 15000) {
  const pending = new Map<string, Pending>();
  const revoke = () => {
    for (const value of pending.values()) {
      clearTimeout(value.timeout);
      value.reject(new Error("会话已切换"));
    }
    pending.clear();
  };
  const unsubscribe = accountScope.onChange(revoke);
  handleTrusted(
    "callDesktopSession",
    (event, method: string, args: unknown[], namespace: string) => {
      if (!accountScope.matches(namespace) || !accountScope.ownsWindow(event.sender))
        throw new Error("会话已切换");
      if (
        !methods.has(method) ||
        !Array.isArray(args) ||
        JSON.stringify(args).length > 512 * 1024
      )
        throw new Error("无效的子窗口操作");
      const main = getWebContents();
      if (!main || main.isDestroyed() || event.sender === main || pending.size >= 256)
        throw new Error("主窗口暂时不可用");
      const id = randomUUID();
      return new Promise((resolve, reject) => {
        const timeout = setTimeout(() => {
          pending.delete(id);
          reject(new Error("主窗口操作超时，请重试"));
        }, timeoutMs);
        pending.set(id, {
          owner: accountScope.lease(),
          requester: event.sender,
          resolve,
          reject,
          timeout,
        });
        try {
          main.send("desktopSessionRequest", { id, method, args, namespace });
        } catch {
          clearTimeout(timeout);
          pending.delete(id);
          reject(new Error("主窗口暂时不可用"));
        }
      });
    },
  );
  handleTrusted(
    "replyDesktopSession",
    (event, response: { id: string; ok: boolean; data?: unknown }) => {
      if (event.sender !== getWebContents()) throw new Error("仅主窗口可响应");
      const request = pending.get(response?.id);
      if (!request) return;
      pending.delete(response.id);
      clearTimeout(request.timeout);
      if (!accountScope.owns(request.owner) || request.requester.isDestroyed()) {
        request.reject(new Error("会话已切换"));
        return;
      }
      if (!response.ok) request.reject(new Error("主窗口操作未完成，请重试"));
      else request.resolve(response.data);
    },
  );
  handleTrusted(
    "pushDesktopSessionState",
    (event, state: unknown, namespace: string) => {
      if (event.sender !== getWebContents() || !accountScope.matches(namespace))
        throw new Error("拒绝其他窗口写入主会话");
      if (
        !state ||
        typeof state !== "object" ||
        JSON.stringify(state).length > 16 * 1024 * 1024
      )
        throw new Error("窗口数据无效");
      for (const window of BrowserWindow.getAllWindows()) {
        const contents = window.webContents;
        if (
          contents !== event.sender &&
          !contents.isDestroyed() &&
          isTrustedDocument(
            contents.getURL(),
            global.pathConfig.indexHtml,
            process.env.VITE_DEV_SERVER_URL,
          )
        )
          contents.send("desktopSessionState", state);
      }
    },
  );
  return () => {
    unsubscribe();
    revoke();
  };
}
