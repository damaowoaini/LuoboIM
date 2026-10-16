import { CustomEmojiItem } from "@/pages/chat/queryChat/MessageItem/FaceMessageRender";
import { LocaleString } from "@/store/type";
import { sessionCoordinator } from "@/core/session/browser";
import localForage from "localforage";

localForage.config({ name: "OpenIM-Config" }); // Read-only legacy cache; no implicit ownership migration.
const accountData = localForage.createInstance({ name: "LuoboDuihua", storeName: "account_data" });
const scope = async (key: string) => { await sessionCoordinator.restore(); return `${sessionCoordinator.namespace}:${key}`; };
export const setAreaCode = (value: string) => localStorage.setItem("IM_AREA_CODE", value);
export const setPhoneNumber = (value: string) => localStorage.setItem("IM_PHONE_NUM", value);
export const setEmail = (value: string) => localStorage.setItem("IM_EMAIL", value);
export const setLoginMethod = (value: string) => localStorage.setItem("IM_LOGIN_METHOD", value);
// Older callers may confirm an already committed profile; independent token
// writes are deliberately rejected to prevent mixed-account credential sets.
export const setIMProfile = async (profile: { chatToken: string; imToken: string; userID: string }) => {
  const active = sessionCoordinator.current;
  if (!active || profile.userID !== active.userID || profile.chatToken !== active.chatToken || profile.imToken !== active.imToken) throw new Error("请通过完整租户认证保存会话");
};
export const setAccessedFriendApplication = async (list: string[], expected?: {
  owner: NonNullable<typeof sessionCoordinator.current>;
  generation: number;
}) => {
  // Existing callers retain their contract. The NewFriends effect passes its
  // rendered owner so restore cannot silently select the next account's key.
  if (!expected) return accountData.setItem(await scope("accessedFriendApplications"), list);
  const current = () => sessionCoordinator.current === expected.owner &&
    sessionCoordinator.generation === expected.generation;
  if (!current()) return;
  await sessionCoordinator.restore();
  if (!current()) return;
  // Capture the namespace synchronously, then dispatch with no intervening await.
  const key = `${sessionCoordinator.namespace}:accessedFriendApplications`;
  return accountData.setItem(key, list);
};
export const setAccessedGroupApplication = async (list: string[]) => accountData.setItem(await scope("accessedGroupApplications"), list);
export const setUserCustomEmojis = async (list: CustomEmojiItem[]) => accountData.setItem(await scope("customEmojis"), list);
export const setLocale = (locale: string) => localStorage.setItem("IM_LOCALE", locale);
export const setImageCache = async (cache: Record<string, string>) => {
  if (!sessionCoordinator.current) return;
  return accountData.setItem(await scope("imageCache"), cache);
};
export const clearIMProfile = async () => {
  await sessionCoordinator.clear();
  await Promise.all(["IM_TOKEN", "IM_CHAT_TOKEN", "IM_USERID"].map(key => localForage.removeItem(key)));
};
export const getAreaCode = () => localStorage.getItem("IM_AREA_CODE");
export const getPhoneNumber = () => localStorage.getItem("IM_PHONE_NUM");
export const getEmail = () => localStorage.getItem("IM_EMAIL");
export const getLoginMethod = () => (localStorage.getItem("IM_LOGIN_METHOD") ?? "phone") as "phone" | "email";
export const getIMToken = async () => (await sessionCoordinator.restore())?.imToken;
export const getChatToken = async () => (await sessionCoordinator.restore())?.chatToken;
export const getIMUserID = async () => (await sessionCoordinator.restore())?.userID;
export const getAccessedFriendApplication = async () => (await accountData.getItem<string[]>(await scope("accessedFriendApplications"))) ?? [];
export const getAccessedGroupApplication = async () => (await accountData.getItem<string[]>(await scope("accessedGroupApplications"))) ?? [];
export const getUserCustomEmojis = async (): Promise<CustomEmojiItem[]> => (await accountData.getItem<CustomEmojiItem[]>(await scope("customEmojis"))) ?? [];
export const getImageCache = async (): Promise<Record<string, string>> => {
  await sessionCoordinator.restore();
  return sessionCoordinator.current ? (await accountData.getItem<Record<string, string>>(await scope("imageCache"))) ?? {} : {};
};
export const getLocale = (): LocaleString => window.electronAPI?.ipcSendSync("getKeyStoreSync", { key: "language" }) || (localStorage.getItem("IM_LOCALE") as LocaleString) || "zh-CN";
