import { sessionCoordinator } from "@/core/session/browser";
import { browserEndpoints } from "@/core/session/devEndpoints";

export { getRouteUrl } from "@/core/session/route";
export const getWsUrl = () => sessionCoordinator.current ? browserEndpoints(sessionCoordinator.current).ws : "";
export const getApiUrl = () => sessionCoordinator.current ? browserEndpoints(sessionCoordinator.current).im : "";
export const getChatUrl = () => sessionCoordinator.current ? browserEndpoints(sessionCoordinator.current).chat : "";
// Kept as exports for the existing server-configuration dialog; active tenant
// addresses are always resolved from the atomic session record.
export const WS_URL = "";
export const API_URL = "";
export const CHAT_URL = "";
export const APP_VERSION = "萝卜对话 v1.0.0";
export const APP_VERSION_CODE = 100;
export const SDK_VERSION = "OpenIM Web Wrapper 3.5.1-alpha.3";
export const CHECK_UPDATE_PREFIX = (import.meta.env.VITE_UPDATE_URL as string | undefined) || "";
export const isSaveLog = false;
