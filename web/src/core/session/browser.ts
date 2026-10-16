import localForage from "localforage";
import { getRouteUrl } from "./route";
import { Session, SessionCoordinator } from "./session";

const records = localForage.createInstance({
  name: "LuoboDuihua",
  storeName: "sessions",
});
const sessionKey = "current";
const revokedKey = "luobo.session.revoked";
const channel =
  typeof BroadcastChannel === "function"
    ? new BroadcastChannel("luobo.session")
    : undefined;
export const sessionCoordinator = new SessionCoordinator({
  read: async () => {
    if (localStorage.getItem(revokedKey)) return undefined;
    const value = await records.getItem<Session>(sessionKey);
    return value && value.issuer === getRouteUrl() ? value : undefined;
  },
  write: async (session: Session) => {
    localStorage.setItem(revokedKey, "1");
    channel?.postMessage("changed");
    await records.setItem(sessionKey, session);
    localStorage.removeItem(revokedKey);
    channel?.postMessage("changed");
  },
  clear: async () => {
    try {
      localStorage.setItem(revokedKey, "1");
    } catch {
      /* IndexedDB removal must still run if localStorage is full. */
    }
    channel?.postMessage("changed");
    await records.removeItem(sessionKey);
    channel?.postMessage("changed");
  },
});

// Notify other tabs/desktop windows without broadcasting credentials. A reload
// releases their SDK, media rooms and view caches before restoring new context.
if (channel)
  channel.onmessage = () => {
    sessionCoordinator.invalidate();
    window.location.reload();
  };
else
  window.addEventListener("storage", (event) => {
    if (event.key === revokedKey) {
      sessionCoordinator.invalidate();
      window.location.reload();
    }
  });

// Synchronous revocation closes the gap before a renderer can read old cache paths.
sessionCoordinator.onChange(() => {
  window.electronAPI?.ipcSendSync(
    "setUserCachePath",
    sessionCoordinator.current ? sessionCoordinator.namespace : undefined,
  );
});
