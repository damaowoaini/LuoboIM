/// <reference types="node" />
import { webcrypto } from "node:crypto";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { announcementKey, announcementSnapshot, GroupAnnouncementAcknowledgements } from "./groupAnnouncementAck";

const account = { issuer: "https://realm.example", tenantID: "tenant-one", userID: "actor-one" };
const group = (text = "private announcement", time = 1, publisher = "publisher-one") => ({
  groupID: "group-one", status: 0, notification: text, notificationUpdateTime: time, notificationUserID: publisher,
} as any);
const member = { groupID: "group-one", userID: account.userID, roleLevel: 20 } as any;
const snapshot = (text?: string, time?: number, publisher?: string) => announcementSnapshot(group(text, time, publisher), member, "group-one", account.userID);
beforeEach(() => vi.stubGlobal("crypto", webcrypto));
afterEach(() => vi.unstubAllGlobals());

describe("exact-version local announcement acknowledgement", () => {
  it("stores only boolean true under realm account group and full-version hashes", async () => {
    const data = new Map<string, unknown>();
    const write = vi.fn(async (key: string, value: boolean) => { data.set(key, value); return value; });
    const store = new GroupAnnouncementAcknowledgements({ getItem: async key => data.get(key), setItem: write });
    const selected = await snapshot();
    await store.confirm(account, selected, () => true);
    expect(await store.read(account, selected, () => true)).toBe(true);
    expect([...data.values()]).toEqual([true]);
    expect([...data.keys()][0]).toMatch(/^luobo\.group-announcement\.v1\.[a-f0-9]{64}\.[a-f0-9]{64}\.[a-f0-9]{64}$/);
    for (const plaintext of [account.issuer, account.userID, account.tenantID, "group-one", "private announcement", "publisher-one"]) {
      expect(JSON.stringify([...data])).not.toContain(plaintext);
    }
    expect(write).toHaveBeenCalledTimes(1);
  });
  it("separates every realm account group and version dimension without deleting other versions", async () => {
    const value = await snapshot(); const original = await announcementKey(account, value);
    for (const changed of [{ ...account, issuer: "https://other.example" }, { ...account, tenantID: "other" }, { ...account, userID: "other" }]) {
      expect(await announcementKey(changed, value)).not.toBe(original);
    }
    expect(await announcementKey(account, { ...value, groupID: "other" })).not.toBe(original);
    for (const changed of [await snapshot("different"), await snapshot(undefined, 2), await snapshot(undefined, undefined, "other")]) {
      expect(await announcementKey(account, changed)).not.toBe(original);
    }
  });
  it("refuses wrong group actor status or malformed snapshot instead of acknowledging unverified metadata", async () => {
    for (const [g, m] of [[{ ...group(), groupID: "other" }, member], [group(), { ...member, userID: "other" }],
      [{ ...group(), status: 2 }, member], [{ ...group(), notificationUpdateTime: NaN }, member],
      [{ ...group(), notification: "bad\u0000text" }, member]]) {
      await expect(announcementSnapshot(g as any, m as any, "group-one", account.userID)).rejects.toThrow();
    }
  });
  it("does not report a failed or obsolete local write as a successful acknowledgement", async () => {
    const value = await snapshot(); let current = true;
    let finish!: (value: boolean) => void;
    const write = vi.fn(() => new Promise<boolean>(resolve => { finish = resolve; }));
    const store = new GroupAnnouncementAcknowledgements({ getItem: async () => null, setItem: write });
    const pending = store.confirm(account, value, () => current);
    const end = Date.now() + 2000;
    while (!write.mock.calls.length && Date.now() < end) await new Promise(resolve => setTimeout(resolve, 0));
    expect(write).toHaveBeenCalledTimes(1);
    current = false; finish(true); await expect(pending).rejects.toThrow("公告视图已变化");
    await expect(store.confirm(account, value, () => false)).rejects.toThrow(); expect(write).toHaveBeenCalledTimes(1);
    const failed = new GroupAnnouncementAcknowledgements({ getItem: async () => null, setItem: async () => { throw new Error("synthetic quota"); } });
    await expect(failed.confirm(account, value, () => true)).rejects.toThrow();
  });
});
