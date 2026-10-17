/// <reference types="node" />
// @vitest-environment jsdom
import "./harness";
import { webcrypto } from "node:crypto";
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { deferred, enter, fixture, flush, group, member, reset, store, conversation } from "./harness";

const local = vi.hoisted(() => ({ read: vi.fn(), write: vi.fn(), rows: new Map<string, unknown>() }));
vi.mock("localforage", () => ({ default: { createInstance: () => ({ getItem: local.read, setItem: local.write }) } }));
// Reuse the real group store/session/history fixture. Its original editor tests
// mock the card, so explicitly load the actual card under test here.
const { default: Card } = await vi.importActual<typeof import("@/pages/chat/queryChat/ChatHeader/GroupAnnouncementCard")>("@/pages/chat/queryChat/ChatHeader/GroupAnnouncementCard");
let root: Root, host: HTMLDivElement, focus = true;
const open = vi.fn(), resetAt = vi.fn();
const button = (text: string) => [...host.querySelectorAll("button")].find(value => value.textContent === text)!;
async function work(action: () => unknown) { await act(async () => { void action(); await flush(); }); }
async function until(check: () => boolean) {
  const end = Date.now() + 2000;
  while (!check() && Date.now() < end) await work(() => undefined);
  expect(check()).toBe(true);
}
function Host() {
  const info = store(state => state.currentGroupInfo), current = store(state => state.currentConversation);
  return <Card currentGroupInfo={info} conversationID={current?.conversationID} openGroupAnnouncementDrawer={open} />;
}
async function mount() {
  await work(() => enter());
  await work(() => root.render(<Host />));
  await until(() => button("在本机标为已读")?.disabled === false);
}
async function confirm() { await work(() => button("在本机标为已读").click()); }
beforeEach(() => {
  reset(); focus = true; open.mockReset(); resetAt.mockReset(); local.rows.clear(); local.read.mockReset(); local.write.mockReset();
  local.read.mockImplementation(async (key: string) => local.rows.get(key));
  local.write.mockImplementation(async (key: string, value: boolean) => { local.rows.set(key, value); return value; });
  Object.assign(fixture.sdk, { resetConversationGroupAtType: resetAt });
  vi.stubGlobal("crypto", webcrypto); vi.spyOn(document, "hasFocus").mockImplementation(() => focus);
  fixture.groups.A = group("A", "current announcement"); fixture.groups.B = group("B", "B announcement");
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => {
  await work(() => root.unmount()); host.remove();
  expect(resetAt).not.toHaveBeenCalled();
  vi.restoreAllMocks(); vi.unstubAllGlobals();
});

describe("real announcement card uses local exact-version acknowledgement without SDK @ reset", () => {
  it("opening or double-clicking reads without acknowledging and keeps the existing editor opener", async () => {
    await mount();
    await work(() => host.querySelector("section")!.dispatchEvent(new MouseEvent("dblclick", { bubbles: true })));
    expect(open).toHaveBeenCalledTimes(1); expect(local.write).not.toHaveBeenCalled();
    expect(fixture.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("hides only after successful exact boolean persistence and keeps a 44px keyboard confirmation", async () => {
    await mount(); const ack = deferred<boolean>(); local.write.mockReturnValue(ack.promise);
    expect(button("在本机标为已读").type).toBe("button"); expect(button("在本机标为已读").style.minHeight).toBe("44px");
    await confirm(); await until(() => local.write.mock.calls.length === 1);
    expect(host.textContent).toContain("current announcement");
    await work(() => ack.resolve(true)); await until(() => host.querySelector("section") === null);
    expect(local.write.mock.calls[0][1]).toBe(true); expect(fixture.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("keeps the prompt and an explicit retry after storage failure without automatic repeat", async () => {
    await mount(); local.write.mockRejectedValue(new Error("synthetic quota")); await confirm();
    await until(() => !!host.querySelector('[role="alert"]'));
    expect(host.textContent).toContain("本机已读未确认"); expect(host.textContent).toContain("current announcement");
    expect(local.write).toHaveBeenCalledTimes(1);
  });
  it("does not turn a failed local acknowledgement read into a hidden prompt", async () => {
    await mount(); local.read.mockRejectedValueOnce(new Error("synthetic storage read"));
    await work(() => button("重新读取").click()); await until(() => !!host.querySelector('[role="alert"]'));
    expect(host.textContent).toContain("群公告读取失败"); expect(host.querySelector("section")).not.toBeNull();
    expect(button("在本机标为已读").disabled).toBe(true); expect(local.write).not.toHaveBeenCalled();
  });
  it("shows a new version again while ordinary same-CID unread updates preserve valid reading", async () => {
    await mount(); await work(() => store.getState().updateCurrentConversation({ ...conversation(), unreadCount: 3 }));
    await until(() => button("在本机标为已读")?.disabled === false); await confirm();
    await until(() => host.querySelector("section") === null);
    fixture.groups.A = { ...group("A", "new version"), notificationUpdateTime: 2 };
    await work(() => store.getState().updateCurrentGroupInfo(fixture.groups.A));
    await until(() => host.textContent?.includes("new version") === true);
    expect(button("在本机标为已读").disabled).toBe(false); expect(local.write).toHaveBeenCalledTimes(1);
  });
  it("a fresh changed version cannot be confirmed under the old visible version", async () => {
    await mount(); fixture.groups.A = { ...group("A", "new current version"), notificationUpdateTime: 2 };
    await confirm(); await until(() => host.textContent?.includes("new current version") === true);
    expect(local.write).not.toHaveBeenCalled();
  });
  it("wrong group or current actor and empty membership reject confirmation before storage", async () => {
    await mount(); const original = fixture.sdk.getSpecifiedGroupMembersInfo.getMockImplementation()!;
    fixture.sdk.getSpecifiedGroupMembersInfo.mockResolvedValue({ data: [member("A", "foreign-actor")] });
    await confirm(); await until(() => !!host.querySelector('[role="alert"]')); expect(local.write).not.toHaveBeenCalled();
    fixture.sdk.getSpecifiedGroupMembersInfo.mockResolvedValue({ data: [] }); await confirm();
    await until(() => button("在本机标为已读")?.disabled === false); expect(local.write).not.toHaveBeenCalled();
    fixture.sdk.getSpecifiedGroupMembersInfo.mockImplementation(original); fixture.groups.A = group("wrong", "wrong group"); await confirm();
    await until(() => button("在本机标为已读")?.disabled === false); expect(local.write).not.toHaveBeenCalled();
  });
  it("A to B to A cannot revive a pending original confirmation", async () => {
    await mount(); const pending = deferred<any>(); fixture.sdk.getSpecifiedGroupsInfo.mockReturnValueOnce(pending.promise);
    await confirm(); await work(() => enter("B")); await work(() => enter("A"));
    await work(() => pending.resolve({ data: [fixture.groups.A] }));
    expect(local.write).not.toHaveBeenCalled();
  });
  it("owner issuer and tenant changes never let a pending confirmation write a replacement owner's record", async () => {
    await mount(); const pending = deferred<any>(); fixture.sdk.getSpecifiedGroupsInfo.mockReturnValueOnce(pending.promise); await confirm();
    fixture.session.current.issuer = "https://other.example"; fixture.session.current.tenant.serverID = "foreign";
    fixture.session.current = { ...fixture.owner(), userID: "foreign-owner" }; fixture.session.generation++;
    await work(() => pending.resolve({ data: [fixture.groups.A] })); expect(local.write).not.toHaveBeenCalled();
  });
  it("blur and refocus retire the old confirmation rather than resuming its action", async () => {
    await mount(); const pending = deferred<any>(); fixture.sdk.getSpecifiedGroupsInfo.mockReturnValueOnce(pending.promise); await confirm();
    await work(() => { focus = false; window.dispatchEvent(new Event("blur")); });
    await work(() => { focus = true; window.dispatchEvent(new Event("focus")); });
    await work(() => pending.resolve({ data: [fixture.groups.A] })); expect(local.write).not.toHaveBeenCalled();
  });
  it("an intervening modal retires pending confirmation even when the same view returns", async () => {
    await mount(); const pending = deferred<any>(); fixture.sdk.getSpecifiedGroupsInfo.mockReturnValueOnce(pending.promise); await confirm();
    const modal = document.createElement("div"); modal.setAttribute("role", "dialog");
    await work(() => document.body.append(modal)); await work(() => modal.remove());
    await work(() => pending.resolve({ data: [fixture.groups.A] })); expect(local.write).not.toHaveBeenCalled();
  });
  it("a hidden document retires pending confirmation and never resumes its write", async () => {
    await mount(); const pending = deferred<any>(); fixture.sdk.getSpecifiedGroupsInfo.mockReturnValueOnce(pending.promise); await confirm();
    const visibility = vi.spyOn(document, "visibilityState", "get");
    await work(() => { visibility.mockReturnValue("hidden"); document.dispatchEvent(new Event("visibilitychange")); });
    await work(() => { visibility.mockReturnValue("visible"); document.dispatchEvent(new Event("visibilitychange")); });
    await work(() => pending.resolve({ data: [fixture.groups.A] })); expect(local.write).not.toHaveBeenCalled();
  });
  it("an expired confirmation result cannot write or relabel the current prompt", async () => {
    await mount(); const pending = deferred<any>(); fixture.sdk.getSpecifiedGroupsInfo.mockReturnValueOnce(pending.promise); await confirm();
    const time = Date.now(); vi.spyOn(Date, "now").mockReturnValue(time + 31000);
    await work(() => pending.resolve({ data: [fixture.groups.A] })); expect(local.write).not.toHaveBeenCalled();
  });
  it("a late publisher from A or a wrong identity cannot label B's current announcement", async () => {
    const old = deferred<any>(); const normal = fixture.sdk.getSpecifiedGroupMembersInfo.getMockImplementation()!;
    fixture.sdk.getSpecifiedGroupMembersInfo.mockImplementation((args: any) => args.userIDList[0] === "author-A" ? old.promise : normal(args));
    await mount(); await work(() => enter("B"));
    await work(() => old.resolve({ data: [{ ...member("A", "author-A"), nickname: "stale author" }] }));
    await until(() => host.textContent?.includes("B announcement") === true);
    expect(host.textContent).not.toContain("stale author");
  });
  it("a mismatched current publisher remains generic and never changes the verified announcement", async () => {
    const normal = fixture.sdk.getSpecifiedGroupMembersInfo.getMockImplementation()!;
    fixture.sdk.getSpecifiedGroupMembersInfo.mockImplementation((args: any) => args.userIDList[0] === "author-A"
      ? Promise.resolve({ data: [{ ...member("wrong", "foreign"), nickname: "unrelated publisher" }] }) : normal(args));
    await mount(); expect(host.textContent).not.toContain("unrelated publisher");
    expect(host.textContent).toContain("placeholder.administrator"); expect(host.textContent).toContain("current announcement");
  });
  it("a newer personal or all-members mention is never reset by this version's local acknowledgement", async () => {
    await mount(); await work(() => store.getState().updateCurrentConversation({ ...conversation(), groupAtType: 3 }));
    await until(() => button("在本机标为已读")?.disabled === false); await confirm();
    await until(() => host.querySelector("section") === null);
    expect(store.getState().currentConversation?.groupAtType).toBe(3); expect(resetAt).not.toHaveBeenCalled();
  });
  it("a same-view group mute does not remove a normal member's local read acknowledgement", async () => {
    fixture.groups.A = { ...fixture.groups.A, status: 3 };
    await mount(); await confirm(); await until(() => host.querySelector("section") === null);
    expect(local.write).toHaveBeenCalledTimes(1);
  });
});
