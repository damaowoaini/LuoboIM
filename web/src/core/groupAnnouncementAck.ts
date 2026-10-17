import localForage from "localforage";
import type { GroupItem, GroupMemberItem } from "open-im-sdk-wasm/lib/types/entity";

export type AnnouncementAccount = Readonly<{ issuer: string; tenantID: string; userID: string }>;
export type AnnouncementSnapshot = Readonly<{
  groupID: string; text: string; publisherID: string; updatedAt: number; version: string;
}>;
const controls = /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]/;
const text = (value: unknown, max: number): value is string => typeof value === "string" &&
  value.length <= max && !controls.test(value) && !/[\ud800-\udfff]/u.test(value);
const identifier = (value: unknown): value is string => text(value, 128) && !!value && !/\s/.test(value);

async function digest(value: unknown): Promise<string> {
  const bytes = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(JSON.stringify(value)));
  return [...new Uint8Array(bytes)].map(byte => byte.toString(16).padStart(2, "0")).join("");
}

// This validates a fresh SDK metadata/member read, not a received message or a
// server read receipt. A personal/group send mute does not remove read access.
export async function announcementSnapshot(group: GroupItem, member: GroupMemberItem,
  groupID: string, actor: string): Promise<AnnouncementSnapshot> {
  if (!identifier(groupID) || !identifier(actor) || group?.groupID !== groupID ||
      ![0, 3].includes(group.status) || member?.groupID !== groupID || member.userID !== actor ||
      ![20, 60, 100].includes(member.roleLevel) || !text(group.notification, 10000) ||
      !text(group.notificationUserID, 128) || /[\t\r\n]/.test(group.notificationUserID) ||
      !Number.isSafeInteger(group.notificationUpdateTime) || group.notificationUpdateTime < 0) {
    throw new Error("群公告或本人群身份无法核验");
  }
  const tuple = [groupID, group.notificationUpdateTime, group.notificationUserID, group.notification] as const;
  return Object.freeze({ groupID, text: group.notification, publisherID: group.notificationUserID,
    updatedAt: group.notificationUpdateTime, version: await digest(tuple) });
}

export async function announcementKey(account: AnnouncementAccount, snapshot: AnnouncementSnapshot): Promise<string> {
  if (!text(account.issuer, 2048) || !account.issuer || !identifier(account.tenantID) ||
      !identifier(account.userID) || !identifier(snapshot.groupID) || !/^[a-f0-9]{64}$/.test(snapshot.version)) {
    throw new Error("群公告本机记录缺少账号归属");
  }
  return `luobo.group-announcement.v1.${await digest([account.issuer, account.tenantID, account.userID])}.${await digest(snapshot.groupID)}.${snapshot.version}`;
}

export async function announcementDeadline<T>(work: Promise<T>): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([work, new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error("群公告操作超时，请重试")), 10000);
    })]);
  } finally { clearTimeout(timer); }
}

type BooleanStore = { getItem(key: string): Promise<unknown>; setItem(key: string, value: boolean): Promise<unknown> };
export class GroupAnnouncementAcknowledgements {
  private tail: Promise<unknown> = Promise.resolve();
  private storage?: BooleanStore;
  constructor(private readonly supplied?: BooleanStore) {}
  private store(): BooleanStore {
    return this.supplied ?? (this.storage ??= localForage.createInstance({ name: "LuoboDuihua", storeName: "group_announcement_ack_v1" }));
  }
  private ordered<T>(account: AnnouncementAccount, snapshot: AnnouncementSnapshot,
    current: () => boolean, action: (key: string) => Promise<T>): Promise<T> {
    const operation = this.tail.then(async () => {
      if (!current()) throw new Error("公告视图已变化");
      const key = await announcementKey(account, snapshot);
      if (!current()) throw new Error("公告视图已变化");
      const value = await announcementDeadline(action(key));
      if (!current()) throw new Error("公告视图已变化");
      return value;
    });
    this.tail = operation.catch(() => undefined);
    return operation;
  }
  read(account: AnnouncementAccount, snapshot: AnnouncementSnapshot, current: () => boolean): Promise<boolean> {
    return this.ordered(account, snapshot, current, async key => {
      const value = await this.store().getItem(key);
      if (value !== undefined && value !== null && typeof value !== "boolean") throw new Error("本机公告记录格式错误");
      return value === true;
    });
  }
  confirm(account: AnnouncementAccount, snapshot: AnnouncementSnapshot, current: () => boolean): Promise<void> {
    return this.ordered(account, snapshot, current, async key => {
      // Only an exact original key is dispatched. A later view retirement does
      // not undo that write and cannot authorize a new version or broad reset.
      if (await this.store().setItem(key, true) !== true) throw new Error("本机公告确认未保存");
    });
  }
}
export const groupAnnouncementAcknowledgements = new GroupAnnouncementAcknowledgements();
