// @vitest-environment jsdom
import "./harness";
import React, { act, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import Announcement from "@/pages/chat/queryChat/GroupAnnouncementDrawer";
import ChatHeader from "@/pages/chat/queryChat/ChatHeader";
import type { OverlayVisibleHandle } from "@/hooks/useOverlayVisible";
import { conversation, deferred, enter, fixture, flush, group, member, reset, store } from "./harness";

let root: Root | undefined, host: HTMLDivElement;
let ref: React.RefObject<OverlayVisibleHandle | null>;
beforeEach(reset);
afterEach(() => { act(() => root?.unmount()); root = undefined; host?.remove(); });
async function render(header = false) {
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  ref = createRef<OverlayVisibleHandle>();
  await act(async () => { if (!store.getState().currentConversation) enter();
    root!.render(header ? <ChatHeader /> : <Announcement ref={ref} />); await flush(); });
  if (!header) await act(async () => { ref.current!.openOverlay(); await flush(); });
}
async function callback(fn: () => unknown) { await act(async () => { void fn(); await flush(); }); }
async function edit(text = "A draft") {
  await callback(() => fixture.ui.action());
  expect(host.querySelector("textarea")).not.toBeNull();
  await callback(() => fixture.ui.change({ target: { value: text } }));
  expect(host.querySelector("textarea")?.value).toBe(text);
}
async function navigate(id: string) { await callback(() => enter(id)); }
const draft = () => host.querySelector("textarea")?.value;

describe("real announcement editor binds the originating group and navigation lease", () => {
  it("a muted current administrator can publish exactly to the original group", async () => {
    await render();
    await callback(() => {
      store.getState().tryUpdateCurrentMemberInGroup({ ...member(), muteEndTime: Date.now() + 60000 });
      store.getState().updateCurrentGroupInfo({ ...group(), status: 3 });
    });
    expect(store.getState().captureConversationSendScope()?.canSend()).toBe(false);
    await edit(); await callback(() => fixture.ui.action());
    expect(fixture.sdk.setGroupInfo).toHaveBeenCalledTimes(1);
    expect(fixture.sdk.setGroupInfo).toHaveBeenCalledWith({ groupID: "A", notification: "A draft" });
    expect(host.querySelector("textarea")).toBeNull();
  });
  it("same-CID unread refresh and a new same-group notification do not replace a draft", async () => {
    await render(); await edit();
    await callback(() => {
      store.getState().updateCurrentConversation({ ...conversation(), unreadCount: 7 });
      store.getState().updateCurrentGroupInfo(group("A", "a newer server notification"));
    });
    expect(draft()).toBe("A draft");
    await callback(() => fixture.ui.action());
    expect(fixture.sdk.setGroupInfo).toHaveBeenCalledWith({ groupID: "A", notification: "A draft" });
  });
  it("equal blank announcements in A and B cannot carry A's draft into B", async () => {
    await render(); await edit(); const oldPublish = fixture.ui.action;
    await navigate("B");
    expect(host.querySelector("textarea")).toBeNull();
    await callback(oldPublish); expect(fixture.sdk.setGroupInfo).not.toHaveBeenCalled();
    await edit("B draft"); await callback(() => fixture.ui.action());
    expect(fixture.sdk.setGroupInfo).toHaveBeenCalledWith({ groupID: "B", notification: "B draft" });
  });
  it("A to B to A cannot revive an old retained publish callback", async () => {
    await render(); await edit(); const oldPublish = fixture.ui.action;
    await navigate("B"); await navigate("A"); await callback(oldPublish);
    expect(fixture.sdk.setGroupInfo).not.toHaveBeenCalled();
    expect(host.querySelector("textarea")).toBeNull();
  });
  it("an old publish callback cannot borrow the new B editor's lease", async () => {
    await render(); await edit(); const oldPublish = fixture.ui.action;
    await navigate("B"); await edit("new B draft"); await callback(oldPublish);
    expect(fixture.sdk.setGroupInfo).not.toHaveBeenCalled(); expect(draft()).toBe("new B draft");
  });
  for (const boundary of ["owner", "issuer", "tenant"] as const) {
    it(`${boundary} changes reject a retained editor callback without a native request or stale toast`, async () => {
      await render(); await edit(); const oldPublish = fixture.ui.action;
      await callback(() => {
        if (boundary === "owner") { fixture.session.current = fixture.owner(); fixture.session.generation++; }
        if (boundary === "issuer") fixture.session.current.issuer = "https://other.fixture.invalid";
        if (boundary === "tenant") fixture.session.current.tenant.serverID = "B";
        // Retain the component deliberately; the real App additionally replaces
        // the Router/document. This proves the component's own callback guard.
        root!.render(<Announcement ref={ref} />);
      });
      await callback(oldPublish);
      expect(fixture.sdk.setGroupInfo).not.toHaveBeenCalled(); expect(fixture.toast).not.toHaveBeenCalled();
    });
  }
  for (const change of ["normal member", "wrong actor", "missing group"] as const) {
    it(`${change} cannot publish with the editor's old administrator role`, async () => {
      await render(); await edit(); const oldPublish = fixture.ui.action;
      await callback(() => {
        if (change === "normal member") store.getState().tryUpdateCurrentMemberInGroup(member("A", "actor", 20));
        if (change === "wrong actor") store.setState({ currentMemberInGroup: member("A", "another", 60) });
        if (change === "missing group") store.setState({ currentGroupInfo: undefined });
      });
      await callback(oldPublish); expect(fixture.sdk.setGroupInfo).not.toHaveBeenCalled();
    });
  }
  it("an already dispatched A ACK does not close or reset a new B editor", async () => {
    const ack = deferred<any>(); fixture.sdk.setGroupInfo.mockReturnValue(ack.promise);
    await render(); await edit(); await callback(() => fixture.ui.action());
    expect(fixture.sdk.setGroupInfo).toHaveBeenCalledWith({ groupID: "A", notification: "A draft" });
    await navigate("B"); await edit("kept B draft");
    await callback(() => ack.resolve({ data: {} }));
    expect(draft()).toBe("kept B draft"); expect(ref.current?.isOverlayOpen).toBe(true);
    expect(fixture.sdk.setGroupInfo).toHaveBeenCalledTimes(1);
  });
  it("an obsolete A failure remains silent and does not reset the B editor", async () => {
    const ack = deferred<any>(); fixture.sdk.setGroupInfo.mockReturnValue(ack.promise);
    await render(); await edit(); await callback(() => fixture.ui.action());
    await navigate("B"); await edit("kept B draft");
    await callback(() => ack.reject(new Error("synthetic unknown ACK")));
    expect(fixture.toast).not.toHaveBeenCalled(); expect(draft()).toBe("kept B draft");
  });
  it("a current failure keeps the draft and describes an unconfirmed result without automatic retry", async () => {
    fixture.sdk.setGroupInfo.mockRejectedValue(new Error("synthetic timeout"));
    await render(); await edit(); await callback(() => fixture.ui.action());
    expect(draft()).toBe("A draft"); expect(fixture.sdk.setGroupInfo).toHaveBeenCalledTimes(1);
    expect(String(fixture.toast.mock.calls[0]?.[0]?.error)).toContain("发布结果未确认");
    expect(String(fixture.toast.mock.calls[0]?.[0]?.error)).toContain("不会自动重发");
  });
  it("closing and reopening fences old textarea and delayed close callbacks", async () => {
    await render(); await edit(); const oldChange = fixture.ui.change, oldAfterClose = fixture.ui.drawer.afterOpenChange;
    await callback(() => ref.current!.closeOverlay()); await callback(() => ref.current!.openOverlay());
    await edit("new editor");
    await callback(() => { oldChange({ target: { value: "old callback" } }); oldAfterClose(false); });
    expect(draft()).toBe("new editor"); expect(fixture.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
});

describe("real notification author reader and actual ChatHeader opener", () => {
  it("late A author data cannot label B's notification", async () => {
    fixture.groups.A = group("A", "notice A"); fixture.groups.B = group("B", "notice B");
    const pending = deferred<any>(); const normal = fixture.sdk.getSpecifiedGroupMembersInfo.getMockImplementation()!;
    fixture.sdk.getSpecifiedGroupMembersInfo.mockImplementation((args: any) => args.userIDList[0] === "author-A" ? pending.promise : normal(args));
    await render(); await navigate("B");
    await callback(() => pending.resolve({ data: [{ ...member("A", "author-A"), nickname: "late old author" }] }));
    expect(host.textContent).not.toContain("late old author"); expect(host.textContent).toContain("notice B");
    expect(host.textContent).toContain("author-B");
  });
  for (const field of ["groupID", "userID"] as const) {
    it(`a current author result with wrong ${field} cannot enter the notification`, async () => {
      fixture.groups.A = group("A", "notice A");
      const normal = fixture.sdk.getSpecifiedGroupMembersInfo.getMockImplementation()!;
      fixture.sdk.getSpecifiedGroupMembersInfo.mockImplementation((args: any) => args.userIDList[0] === "author-A"
        ? Promise.resolve({ data: [{ ...member("A", "author-A"), [field]: "wrong", nickname: "unrelated author" }] }) : normal(args));
      await render(); expect(host.textContent).not.toContain("unrelated author");
      expect(host.textContent).toContain("placeholder.administrator");
    });
  }
  it("a newer notification author wins over the earlier same-group read", async () => {
    fixture.groups.A = group("A", "notice A");
    const old = deferred<any>(), normal = fixture.sdk.getSpecifiedGroupMembersInfo.getMockImplementation()!;
    fixture.sdk.getSpecifiedGroupMembersInfo.mockImplementation((args: any) => args.userIDList[0] === "author-A" ? old.promise : normal(args));
    await render();
    await callback(() => store.getState().updateCurrentGroupInfo({ ...group("A", "new notice"), notificationUserID: "new-author" }));
    await callback(() => old.resolve({ data: [{ ...member("A", "author-A"), nickname: "obsolete author" }] }));
    expect(host.textContent).toContain("new-author"); expect(host.textContent).not.toContain("obsolete author");
  });
  it("retained closed content rereads on reopen and rejects the earlier same-author reply", async () => {
    fixture.groups.A = group("A", "notice A");
    const old = deferred<any>(), fresh = deferred<any>();
    const normal = fixture.sdk.getSpecifiedGroupMembersInfo.getMockImplementation()!;
    let authorReads = 0;
    fixture.sdk.getSpecifiedGroupMembersInfo.mockImplementation((args: any) => args.userIDList[0] === "author-A"
      ? (++authorReads === 1 ? old.promise : fresh.promise) : normal(args));
    await render();
    await callback(() => ref.current!.closeOverlay());
    expect(host.querySelector('[data-drawer="placeholder.groupAnnouncement"][data-open="false"]')).not.toBeNull();
    await callback(() => ref.current!.openOverlay());
    expect(authorReads).toBe(2);
    await callback(() => fresh.resolve({ data: [{ ...member("A", "author-A"), nickname: "fresh author" }] }));
    await callback(() => old.resolve({ data: [{ ...member("A", "author-A"), nickname: "pre-close author" }] }));
    expect(host.textContent).toContain("fresh author"); expect(host.textContent).not.toContain("pre-close author");
  });
  it("the actual Header closes an open announcement when the CID changes", async () => {
    await render(true);
    await callback(() => (host.querySelector('span[title="placeholder.groupAnnouncement"] img') as HTMLElement).click());
    expect(host.querySelector('[data-drawer="placeholder.groupAnnouncement"][data-open="true"]')).not.toBeNull();
    await navigate("B");
    expect(host.querySelector('[data-drawer="placeholder.groupAnnouncement"][data-open="true"]')).toBeNull();
    expect(fixture.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("the actual Header keeps the announcement open for a same-CID unread refresh", async () => {
    await render(true);
    await callback(() => (host.querySelector('span[title="placeholder.groupAnnouncement"] img') as HTMLElement).click());
    await callback(() => store.getState().updateCurrentConversation({ ...conversation(), unreadCount: 9 }));
    expect(host.querySelector('[data-drawer="placeholder.groupAnnouncement"][data-open="true"]')).not.toBeNull();
    expect(fixture.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
});
