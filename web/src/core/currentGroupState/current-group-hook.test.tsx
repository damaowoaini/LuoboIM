// @vitest-environment jsdom
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { conversation, enter, fixture, flush, group, member, resetFixture, store } from "./harness";
import { useCurrentMemberRole } from "@/hooks/useCurrentMemberRole";
import useConversationState from "@/pages/chat/queryChat/useConversationState";

let root: Root | undefined;
let host: HTMLDivElement;
let role: ReturnType<typeof useCurrentMemberRole>;
let sending: ReturnType<typeof useConversationState>;
function Probe() {
  role = useCurrentMemberRole(); sending = useConversationState();
  return <output>{sending.getIsCanSendMessage() ? "can-send" : "blocked"}</output>;
}
function mount() { host = document.createElement("div"); document.body.append(host);
  root = createRoot(host); act(() => root!.render(<Probe />)); }
beforeEach(() => {
  resetFixture(); (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  vi.useFakeTimers(); vi.setSystemTime(Date.UTC(2026, 9, 16));
});
afterEach(() => { act(() => root?.unmount()); root = undefined; host?.remove(); vi.useRealTimers(); });

describe("real member role and conversation send hooks", () => {
  it("refreshes at the actual Unix-millisecond mute deadline without an SDK unmute", () => {
    enter(); store.setState({ currentGroupInfo: group(), currentMemberInGroup: member("A", "actor-a", 20, Date.now() + 1500) });
    mount(); expect(role.currentIsMuted).toBe(true); expect(host.textContent).toBe("blocked");
    act(() => vi.advanceTimersByTime(1499)); expect(role.currentIsMuted).toBe(true);
    act(() => vi.advanceTimersByTime(1)); expect(role.currentIsMuted).toBe(false);
    expect(host.textContent).toBe("can-send");
    expect(fixture.sdk.setConversationDraft).not.toHaveBeenCalled();
    expect(fixture.sdk.getSpecifiedGroupMembersInfo).toHaveBeenCalledTimes(1);
  });
  it("replaces the old member deadline and cancels the timer on unmount", () => {
    enter(); store.setState({ currentGroupInfo: group(), currentMemberInGroup: member("A", "actor-a", 20, Date.now() + 1000) });
    mount();
    act(() => store.setState({ currentMemberInGroup: member("A", "actor-a", 20, Date.now() + 3000) }));
    act(() => vi.advanceTimersByTime(1000)); expect(role.currentIsMuted).toBe(true);
    act(() => vi.advanceTimersByTime(2000)); expect(role.currentIsMuted).toBe(false);
    act(() => store.setState({ currentMemberInGroup: member("A", "actor-a", 20, Date.now() + 5000) }));
    act(() => root!.unmount()); root = undefined;
    expect(vi.getTimerCount()).toBe(0);
  });
  it("rechecks clock rollback instead of unmuting before the authoritative timestamp", () => {
    enter(); const deadline = Date.now() + 1000;
    store.setState({ currentGroupInfo: group(), currentMemberInGroup: member("A", "actor-a", 20, deadline) });
    mount(); vi.setSystemTime(Date.now() - 2000);
    act(() => vi.advanceTimersByTime(1000)); expect(role.currentIsMuted).toBe(true);
    act(() => vi.advanceTimersByTime(2000)); expect(role.currentIsMuted).toBe(false);
  });
  it("keeps zero and already expired timestamps unmuted and preserves role flags", () => {
    enter(); store.setState({ currentGroupInfo: group(), currentMemberInGroup: member("A", "actor-a", 60, 0) });
    mount(); expect(role.isAdmin).toBe(true); expect(role.currentIsMuted).toBe(false);
    act(() => store.setState({ currentMemberInGroup: member("A", "actor-a", 100, Date.now() - 1) }));
    expect(role.isOwner).toBe(true); expect(role.currentIsMuted).toBe(false);
  });
  for (const missing of ["metadata", "member"] as const) {
    it(`blocks initial ${missing} loading and resumes after both current identities arrive`, () => {
      enter(); store.setState(missing === "metadata" ? { currentMemberInGroup: member() } : { currentGroupInfo: group() });
      mount(); expect(host.textContent).toBe("blocked");
      act(() => store.setState({ currentGroupInfo: group(), currentMemberInGroup: member() }));
      expect(host.textContent).toBe("can-send");
    });
  }
  for (const mismatch of ["group", "member group", "actor", "unknown status", "unknown role", "dismissed"] as const) {
    it(`does not send with ${mismatch} identity/policy`, () => {
      enter(); store.setState({ currentGroupInfo: group(), currentMemberInGroup: member() });
      if (mismatch === "group") store.setState({ currentGroupInfo: group("B") });
      if (mismatch === "member group") store.setState({ currentMemberInGroup: member("B") });
      if (mismatch === "actor") store.setState({ currentMemberInGroup: member("A", "foreign") });
      if (mismatch === "unknown status") store.setState({ currentGroupInfo: { groupID: "A" } as any });
      if (mismatch === "unknown role") store.setState({ currentMemberInGroup: member("A", "actor-a", 999) });
      if (mismatch === "dismissed") store.setState({ currentGroupInfo: group("A", 2) });
      mount(); expect(host.textContent).toBe("blocked");
    });
  }
  it("whole-group mute still blocks an ordinary member and allows an otherwise eligible admin", () => {
    enter(); store.setState({ currentGroupInfo: group("A", 3), currentMemberInGroup: member() });
    mount(); expect(host.textContent).toBe("blocked");
    act(() => store.setState({ currentMemberInGroup: member("A", "actor-a", 60) }));
    expect(host.textContent).toBe("can-send");
  });
  it("ordinary single chat needs no group read or metadata", () => {
    store.getState().updateCurrentConversation({ ...conversation(), conversationID: "single", conversationType: 1, groupID: "", userID: "peer" });
    mount(); expect(host.textContent).toBe("can-send");
    expect(fixture.groups).toHaveLength(0); expect(fixture.members).toHaveLength(0);
  });
  it("normal current-owner read and at-reset still dispatch for the actual rendered CID", async () => {
    enter(); store.setState({ currentConversation: { ...conversation(), unreadCount: 3, groupAtType: 2 },
      currentGroupInfo: group(), currentMemberInGroup: member() });
    mount(); await act(flush);
    expect(store.getState().currentConversation?.unreadCount).toBe(0);
    expect(fixture.sdk.markConversationMessageAsRead).toHaveBeenCalledWith("cid-A");
    expect(fixture.sdk.resetConversationGroupAtType).toHaveBeenCalledWith("cid-A");
  });
  it("old-owner trailing callbacks and cleanup cannot refill cleared state or write read/at", async () => {
    enter(); store.setState({ currentConversation: { ...conversation(), unreadCount: 3, groupAtType: 2 },
      currentGroupInfo: group(), currentMemberInGroup: member() });
    mount(); await act(flush);
    fixture.sdk.markConversationMessageAsRead.mockClear(); fixture.sdk.resetConversationGroupAtType.mockClear();
    fixture.session.current = { ...fixture.owner(), userID: "actor-b", issuer: "https://second.invalid", tenant: { serverID: "tenant-b" } };
    fixture.session.generation += 1;
    act(() => vi.advanceTimersByTime(2000));
    act(() => { store.getState().clearConversationStore(); root!.unmount(); }); root = undefined;
    act(() => vi.runOnlyPendingTimers()); await flush();
    expect(store.getState().currentConversation).toBeUndefined();
    expect(store.getState().conversationList).toEqual([]);
    expect(fixture.sdk.markConversationMessageAsRead).not.toHaveBeenCalled();
    expect(fixture.sdk.resetConversationGroupAtType).not.toHaveBeenCalled();
  });
  for (const changed of ["issuer", "tenant"] as const) {
    it(`${changed} mutation cannot reuse the mounted hook's old owner admission`, async () => {
      enter(); store.setState({ currentGroupInfo: group(), currentMemberInGroup: member() }); mount();
      if (changed === "issuer") fixture.session.current!.issuer = "https://changed.invalid";
      else fixture.session.current!.tenant.serverID = "changed-tenant";
      await act(async () => { store.setState({ currentConversation: { ...conversation(), unreadCount: 3, groupAtType: 2 } }); await flush(); });
      expect(sending.getIsCanSendMessage()).toBe(false);
      expect(store.getState().currentConversation?.unreadCount).toBe(3);
      expect(fixture.sdk.markConversationMessageAsRead).not.toHaveBeenCalled();
      expect(fixture.sdk.resetConversationGroupAtType).not.toHaveBeenCalled();
    });
  }
  it("old CID cleanup cannot repopulate a replacement view before unmount completes", async () => {
    enter(); store.setState({ currentConversation: { ...conversation(), groupAtType: 2 },
      currentGroupInfo: group(), currentMemberInGroup: member() }); mount(); await act(flush);
    act(() => vi.advanceTimersByTime(2000));
    fixture.sdk.markConversationMessageAsRead.mockClear(); fixture.sdk.resetConversationGroupAtType.mockClear();
    await act(async () => {
      store.setState({ currentConversation: { ...conversation("B"), unreadCount: 5, groupAtType: 2 } });
      root!.unmount(); root = undefined; await flush();
    });
    expect(store.getState().currentConversation?.conversationID).toBe("cid-B");
    expect(store.getState().currentConversation?.unreadCount).toBe(5);
    expect(fixture.sdk.markConversationMessageAsRead).not.toHaveBeenCalled();
    expect(fixture.sdk.resetConversationGroupAtType).not.toHaveBeenCalled();
  });
  it("a saved send callback cannot borrow old authority after A to B to A or a new mute", () => {
    enter(); store.setState({ currentGroupInfo: group(), currentMemberInGroup: member("A", "actor-a", 100) }); mount();
    const oldCanSend = sending.getIsCanSendMessage; expect(oldCanSend()).toBe(true);
    act(() => { enter("B"); enter("A"); });
    expect(oldCanSend()).toBe(false); expect(sending.getIsCanSendMessage()).toBe(false);
    act(() => store.setState({ currentGroupInfo: group(), currentMemberInGroup: member("A", "actor-a", 20, Date.now() + 60000) }));
    expect(oldCanSend()).toBe(false); expect(sending.getIsCanSendMessage()).toBe(false);
    act(() => store.setState({ currentMemberInGroup: member() }));
    expect(oldCanSend()).toBe(false); expect(sending.getIsCanSendMessage()).toBe(true);
  });
});
