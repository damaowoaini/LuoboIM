import { StaleSessionError } from "@/core/session/session";
import { sessionCoordinator } from "@/core/session/browser";
import { createSessionLogout, revokeCurrentSession } from "@/core/session/logout";
import { t } from "i18next";
import { create } from "zustand";

import {
  BusinessAllowType,
  BusinessUserInfo,
  getAppConfig,
  getBusinessUserInfo,
} from "@/api/login";
import { getMomentsUnreadCount } from "@/api/moments";
import { IMSDK } from "@/layout/MainContentWrap";
import router from "@/routes";
import { feedbackToast } from "@/utils/common";
import { clearIMProfile, getLocale, setImageCache, setLocale } from "@/utils/storage";

import { useContactStore } from "./contact";
import { useConversationStore } from "./conversation";
import { AppConfig, AppSettings, UserStore } from "./type";

export const useUserStore = create<UserStore>()((set, get) => ({
  selfInfo: {} as BusinessUserInfo,
  appConfig: {} as AppConfig,
  appConfigStatus: "idle",
  appSettings: {
    locale: getLocale(),
    closeAction: "miniSize",
  },
  imageCache: {} as Record<string, string>,
  workMomentsUnreadCount: 0,
  getSelfInfoByReq: () => {
    IMSDK.getSelfUserInfo()
      .then(({ data }) => {
        set(() => ({ selfInfo: data as unknown as BusinessUserInfo }));
        getBusinessUserInfo([data.userID]).then(({ data: { users } }) =>
          set((state) => ({ selfInfo: { ...state.selfInfo, ...users[0] } })),
        );
      })
      .catch((error) => {
        if (error instanceof StaleSessionError) return;
        feedbackToast({ error, msg: t("toast.getSelfInfoFailed") });
        get().userLogout();
      });
  },
  updateSelfInfo: (info: Partial<BusinessUserInfo>) => {
    set((state) => ({ selfInfo: { ...state.selfInfo, ...info } }));
  },
  getAppConfigByReq: async () => {
    set({appConfigStatus:"loading"});
    try {
      const { data } = await getAppConfig();
      set({appConfig:data.config ?? {} as AppConfig,appConfigStatus:"ready"});
    } catch (error) {
      if (error instanceof StaleSessionError) return;
      set({appConfigStatus:"error"});
    }
  },
  updateAppSettings: (settings: Partial<AppSettings>) => {
    if (settings.locale) {
      setLocale(settings.locale);
    }
    set((state) => ({ appSettings: { ...state.appSettings, ...settings } }));
  },
  userLogout: async (force?: boolean) => { await logoutCurrentSession(force); },
  getWorkMomentsUnreadCount: async () => {
    try {
      const { data } = await getMomentsUnreadCount();
      set({ workMomentsUnreadCount: data.total });
    } catch (error) {
      console.error("get work moments unread count err");
    }
  },
  updateWorkMomentsUnreadCount: (count = 0) => {
    set({ workMomentsUnreadCount: count });
  },
  initImageCache: (cache: Record<string, string>) => {
    set(() => ({ imageCache: cache }));
  },
  addImageCache: (url: string, path: string) => {
    const newCache = { ...get().imageCache };
    newCache[url] = path;
    setImageCache(newCache);
    set(() => ({ imageCache: newCache }));
  },
  clearImageCache: () => {
    setImageCache({});
    set({ imageCache: {} });
  },
}));

const logoutCurrentSession = createSessionLogout(sessionCoordinator, revokeCurrentSession, (state) => {
  // clearIMProfile invalidates this generation synchronously. Its bound SDK
  // lifecycle closes the worker and restarts the document after persistence.
  const clearing = clearIMProfile().catch(() => undefined);
  useUserStore.setState({ selfInfo: {} as BusinessUserInfo, appConfig: {} as AppConfig,
    appConfigStatus: "idle", workMomentsUnreadCount: 0, imageCache: {} });
  useContactStore.getState().clearContactStore();
  useConversationStore.getState().clearConversationStore();
  window.electronAPI?.ipcInvoke("updateUnreadCount", 0);
  const notice = state === "pending" || state === "failed" ? `?logout=${state}` : "";
  router.navigate(`/login${notice}`, { replace: true });
  return clearing;
});

export const isExplicitLogoutPending = () => logoutCurrentSession.isPending();
