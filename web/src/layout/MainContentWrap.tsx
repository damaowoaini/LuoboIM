import { blockPrivateSelectionCopy } from "@/core/messageSelection";
import { bindMessageHistory } from "@/core/messageHistory";
import { messageHistoryFence, messageOwner } from "@/core/messagePrivacyRuntime";
import { sdkDataDirectory, closeSdkWorker } from "@/core/session/sdkDirectory";
import { activateRichAction } from "@/core/content/html";
import { SdkRuntimeBoundary } from "@/core/session/sdkRuntime";
import { bindSessionView } from "@/core/session/view";
import { createChildSdk, isDesktopChild } from "@/core/session/childSdk";
import { sessionCoordinator } from "@/core/session/browser";
import { bindSessionSdk } from "@/core/session/sdk";
import { browserSdkLoopbackOptIn } from "@/core/session/devEndpoints";
import { getSDK } from "open-im-sdk-wasm";
import { AllowType } from "open-im-sdk-wasm";
import { useEffect, useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router-dom";

import { useConversationStore, useUserStore } from "@/store";
import emitter from "@/utils/events";
import { getImageCache, getIMToken, getIMUserID } from "@/utils/storage";

import { useAutoUpdate } from "./useAutoUpdate";

const originalSdk = (
  isDesktopChild
    ? createChildSdk()
    : getSDK({
        coreWasmPath: new URL("./openIM.wasm", document.baseURI).href,
        sqlWasmPath: new URL("./sql-wasm.wasm", document.baseURI).href,
      })
) as ReturnType<typeof getSDK>;
// The JS wrapper registers its SQL close callback synchronously in getSDK.
const closeSdkDatabase = isDesktopChild
  ? undefined
  : (window as unknown as { close(): Promise<unknown> }).close;
const runtimeBoundary = isDesktopChild
  ? undefined
  : new SdkRuntimeBoundary(
      sessionCoordinator,
      () => window.location.reload(),
      () => IMSDK.logout(),
    );
export const IMSDK = bindMessageHistory(bindSessionSdk(
  originalSdk,
  sessionCoordinator,
  isDesktopChild
    ? {}
    : {
        prepareLogin: async (args) => {
          const session = sessionCoordinator.current;
          const params = args[0] as Record<string, unknown>;
          if (!session || params?.userID !== session.userID)
            throw new Error("SDK登录账号与当前会话不一致");
          const dataDir = await sdkDataDirectory(sessionCoordinator.namespace);
          runtimeBoundary?.beforeLogin();
          const prepared: Record<string, unknown> = { ...params, dataDir, isLogStandardOutput: false };
          // A caller cannot opt in independently of the validated deployment.
          delete prepared.allowInsecureLoopbackForTesting;
          if (browserSdkLoopbackOptIn(session)) prepared.allowInsecureLoopbackForTesting = true;
          return [prepared, ...args.slice(1)];
        },
        afterLogout: async () => {
          if (closeSdkDatabase) await closeSdkWorker(closeSdkDatabase);
        },
      },
), messageHistoryFence, messageOwner);

export const MainContentWrap = () => {
  const [restored, setRestored] = useState(!!sessionCoordinator.current);
  const updateAppSettings = useUserStore((state) => state.updateAppSettings);
  const getAppConfigByReq = useUserStore((state) => state.getAppConfigByReq);
  const initImageCache = useUserStore((state) => state.initImageCache);

  const navigate = useNavigate();
  const location = useLocation();

  useAutoUpdate();

  useEffect(() => {
    const activate = sessionCoordinator.guard((event: Event) =>
      activateRichAction(event.target, document, {
        user: (userID, groupID) => window.userClick?.(userID, groupID),
        edit: (messageID) => {
          if (useConversationStore.getState().revokeMap[messageID])
            window.editRevoke?.(messageID);
        },
      }),
    );
    const key = (event: KeyboardEvent) => {
      if ((event.key === "Enter" || event.key === " ") && activate(event))
        event.preventDefault();
    };
    const copy = (event: ClipboardEvent) => { blockPrivateSelectionCopy(event, document); };
    document.addEventListener("copy", copy, true);
    document.addEventListener("click", activate);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("copy", copy, true);
      document.removeEventListener("click", activate);
      document.removeEventListener("keydown", key);
    };
  }, []);

  useEffect(() => {
    return bindSessionView(sessionCoordinator, location.pathname, setRestored,
      (path) => navigate(path, { replace: true }));
  }, [location.pathname, navigate]);

  useEffect(() => {
    window.userClick = (userID?: string, groupID?: string) => {
      if (!userID || userID === "AtAllTag") return;

      const currentGroupInfo = useConversationStore.getState().currentGroupInfo;

      if (groupID && currentGroupInfo?.lookMemberInfo === AllowType.NotAllowed) {
        return;
      }

      emitter.emit("OPEN_USER_CARD", {
        userID,
        groupID,
        isSelf: userID === useUserStore.getState().selfInfo.userID,
        notAdd:
          Boolean(groupID) &&
          currentGroupInfo?.applyMemberFriend === AllowType.NotAllowed,
      });
    };
  }, []);

  useEffect(() => {
    const initSettingStore = async () => {
      if (!window.electronAPI) return;
      updateAppSettings({
        closeAction:
          (await window.electronAPI?.ipcInvoke("getKeyStore", {
            key: "closeAction",
          })) || "miniSize",
      });
      const cache = await getImageCache();
      initImageCache(cache);
      window.electronAPI?.ipcInvoke("main-win-ready");
    };

    initSettingStore();
    getAppConfigByReq();
  }, []);

  // Do not mount private screens before restoration. Their historical login
  // effects can revoke an empty session repeatedly while navigation is pending.
  return location.pathname === "/login" || (restored && sessionCoordinator.current) ? (
    <Outlet />
  ) : (
    <div
      role="status"
      className="flex h-screen items-center justify-center text-slate-500"
    >
      正在恢复会话…
    </div>
  );
};
