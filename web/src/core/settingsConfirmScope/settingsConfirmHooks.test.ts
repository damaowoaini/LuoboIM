// @vitest-environment jsdom
import React, { act } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { captureMessageAccess, deferred, enter, f, flush, group, member, messageHistoryFence, onOk, reset, store, surface } from "./fixture";
import { useGroupSettings } from "@/pages/chat/queryChat/GroupSetting/useGroupSettings";
import { useConversationSettings } from "@/hooks/useConversationSettings";
import { IMSDK } from "@/layout/MainContentWrap";
const actions = ["mute", "dismiss", "quit", "clear"] as const;
type Action = typeof actions[number];
let ui: ReturnType<typeof surface>;
function Probe() { f.api = { group: useGroupSettings({ closeOverlay: f.close }), conversation: useConversationSettings() }; return null; }
const native = (kind: Action) => f.sdk[kind === "mute" ? "changeGroupMute" : kind === "dismiss" ? "dismissGroup" : kind === "quit" ? "quitGroup" : "clearConversationAndDeleteAllMsg"];
async function prepare(kind: Action) { if (kind === "quit") store.setState({ currentMemberInGroup: member(20) }); await ui.render(React.createElement(Probe)); }
async function open(kind: Action) { await prepare(kind); await act(async () => {
  if (kind === "clear") f.api.conversation.clearConversationMessages();
  else if (kind === "mute") await f.api.group.updateGroupMuteAll();
  else if (kind === "dismiss") f.api.group.tryDismissGroup(); else f.api.group.tryQuitGroup();
}); return onOk(); }
const noWrites = () => { for (const kind of actions) expect(native(kind)).not.toHaveBeenCalled(); };
beforeEach(async () => { await reset(); ui = surface(); });
afterEach(async () => { await ui.dispose(); });

describe("real settings hooks original confirm and native-clear phase", () => {
  it.each(actions)("preserves normal exact-target %s", async (kind) => {
    const confirm = await open(kind); await act(async () => { await confirm(); });
    if (kind === "mute") expect(native(kind)).toHaveBeenCalledExactlyOnceWith({ groupID: "A", isMute: true });
    else expect(native(kind)).toHaveBeenCalledExactlyOnceWith(kind === "clear" ? "cid-group-A" : "A");
    if (kind === "quit" || kind === "dismiss") expect(f.close).toHaveBeenCalledOnce(); expect(f.toast).not.toHaveBeenCalled();
  });
  it.each(actions)("keeps valid %s after same-CID unread/metadata replacement", async (kind) => {
    const confirm = await open(kind);
    await act(async () => { store.getState().updateCurrentConversation({ ...store.getState().currentConversation!, showName: "new summary", unreadCount: 9 }); });
    await act(async () => { await confirm(); }); expect(native(kind)).toHaveBeenCalledOnce();
  });
  for (const kind of actions) {
    it.each([
      ["same-generation owner replacement", async () => { f.session.current = f.owner(); }],
      ["generation rotation", async () => { f.session.generation++; }],
      ["issuer mutation", async () => { f.session.current!.issuer = "issuer-B"; }],
      ["tenant mutation", async () => { f.session.current!.tenant.serverID = "B"; }],
      ["A-B-A private navigation epoch", async () => { await enter("B"); await enter("A"); }],
    ] as const)(`${kind} rejects the original confirmation after %s`, async (_name, mutate) => {
      const confirm = await open(kind); await act(async () => { await mutate(); await confirm(); }); noWrites();
    });
  }
  it("does not even create an old group confirm after navigation", async () => {
    await prepare("mute"); const trigger = f.api.group.updateGroupMuteAll; await act(async () => { await enter("B"); await trigger(); });
    expect(f.confirmations).toHaveLength(0); noWrites();
  });
  it("does not create an old clear confirm under a replacement owner", async () => {
    await prepare("clear"); const trigger = f.api.conversation.clearConversationMessages; f.session.current = f.owner();
    await act(async () => trigger()); expect(f.confirmations).toHaveLength(0); noWrites();
  });
  it.each(actions)("rejects a retained %s confirmation after actual hook unmount", async (kind) => {
    const confirm = await open(kind); await ui.dispose(); await confirm(); noWrites();
  });
  it.each(actions)("does not project a late %s ACK into a newly navigated view", async (kind) => {
    const confirm = await open(kind), slow = deferred(); native(kind).mockReturnValueOnce(slow.promise); let pending!: Promise<unknown>;
    await act(async () => { pending = confirm(); await flush(); }); expect(native(kind)).toHaveBeenCalledOnce();
    await act(async () => { await enter("B"); });
    await act(async () => { slow.resolve({ data: {} }); await pending; });
    expect(native(kind)).toHaveBeenCalledOnce(); expect(f.close).not.toHaveBeenCalled(); expect(f.toast).not.toHaveBeenCalled(); expect(f.extraClear).not.toHaveBeenCalled();
  });
  it.each(actions)("suppresses old-owner %s error feedback after one actual native dispatch", async (kind) => {
    const confirm = await open(kind), slow = deferred(); native(kind).mockReturnValueOnce(slow.promise); let pending!: Promise<unknown>;
    await act(async () => { pending = confirm(); await flush(); }); expect(native(kind)).toHaveBeenCalledOnce(); f.session.current = f.owner();
    await act(async () => { slow.reject(new Error("old request")); await pending; });
    expect(f.toast).not.toHaveBeenCalled(); expect(f.close).not.toHaveBeenCalled(); expect(f.extraClear).not.toHaveBeenCalled();
  });
  it("allows a personally muted administrator to manage whole-group mute when canSend is false", async () => {
    store.setState({ currentMemberInGroup: member(60, { muteEndTime: Date.now() + 60_000 }) });
    expect(store.getState().captureConversationSendScope()!.canSend()).toBe(false);
    const confirm = await open("mute"); await act(async () => { await confirm(); }); expect(f.sdk.changeGroupMute).toHaveBeenCalledOnce();
  });
  it("does not offer dismiss to a current nonowner", async () => {
    await prepare("dismiss"); store.setState({ currentMemberInGroup: member(60) });
    await act(async () => f.api.group.tryDismissGroup()); expect(f.confirmations).toHaveLength(0); noWrites();
  });
  it("does not offer quit to a current owner", async () => {
    await prepare("dismiss"); await act(async () => f.api.group.tryQuitGroup()); expect(f.confirmations).toHaveLength(0); noWrites();
  });
  it("rechecks admin mute permission at final confirmation", async () => {
    const confirm = await open("mute"); store.setState({ currentMemberInGroup: member(20) }); await act(async () => { await confirm(); }); noWrites();
  });
  it("rechecks current owner permission before dismiss", async () => {
    const confirm = await open("dismiss"); store.setState({ currentMemberInGroup: member(60) }); await act(async () => { await confirm(); }); noWrites();
  });
  it.each([
    ["wrong self-member group", () => store.setState({ currentMemberInGroup: member(100, { groupID: "B" }) })],
    ["foreign self-member actor", () => store.setState({ currentMemberInGroup: member(100, { userID: "other" }) })],
    ["foreign current group metadata", () => store.setState({ currentGroupInfo: group("B") })],
  ] as const)("rejects latest %s before a frozen group write", async (_name, mutate) => {
    const confirm = await open("mute"); mutate(); await act(async () => { await confirm(); }); noWrites();
  });
  it("does not quit a group after the original member becomes owner", async () => {
    const confirm = await open("quit"); store.setState({ currentMemberInGroup: member(100) }); await act(async () => { await confirm(); }); noWrites();
  });
  it("does not reverse a newer whole-group mute status through an old confirmation", async () => {
    const confirm = await open("mute"); store.setState({ currentGroupInfo: group("A", 3) }); await act(async () => { await confirm(); }); noWrites();
  });
  it("preserves direct unmute false payload, but an old direct callback cannot execute under a new owner", async () => {
    store.setState({ currentGroupInfo: group("A", 3) }); await prepare("mute"); const trigger = f.api.group.updateGroupMuteAll;
    await act(async () => { await trigger(); }); expect(f.confirmations).toHaveLength(0);
    expect(f.sdk.changeGroupMute).toHaveBeenCalledExactlyOnceWith({ groupID: "A", isMute: false });
    f.session.current = f.owner(); await act(async () => { await trigger(); }); expect(f.sdk.changeGroupMute).toHaveBeenCalledOnce();
  });
  it("finishes a current-view native clear even though its own history ticket is invalidated, with no redundant third global invalidation", async () => {
    const confirm = await open("clear"), history = captureMessageAccess("cid-group-A"), revision = messageHistoryFence.version();
    await act(async () => { await confirm(); }); expect(f.sdk.clearConversationAndDeleteAllMsg).toHaveBeenCalledExactlyOnceWith("cid-group-A");
    expect(history.current()).toBe(false); expect(messageHistoryFence.version() - revision).toBe(2); expect(f.extraClear).not.toHaveBeenCalled(); expect(f.toast).not.toHaveBeenCalled();
  });
  it("preserves ordinary single-conversation clear without requiring group membership", async () => {
    await enter("peer", "user"); const confirm = await open("clear"); await act(async () => { await confirm(); });
    expect(f.sdk.clearConversationAndDeleteAllMsg).toHaveBeenCalledExactlyOnceWith("cid-user-peer");
  });
  it("retains existing owner-change rejection while the accepted clear is draining an older SDK operation", async () => {
    const confirm = await open("clear"), slow = deferred(); f.sdk.getAdvancedHistoryMessageList.mockReturnValueOnce(slow.promise);
    const history = IMSDK.getAdvancedHistoryMessageList({ conversationID: "cid-group-A", count: 1 } as any).catch(() => undefined);
    let pending!: Promise<unknown>; await act(async () => { pending = confirm(); await flush(); });
    expect(f.sdk.clearConversationAndDeleteAllMsg).not.toHaveBeenCalled(); f.session.current = f.owner(); f.session.generation++;
    await act(async () => { slow.resolve({ data: {} }); await history; await pending; }); expect(f.sdk.clearConversationAndDeleteAllMsg).not.toHaveBeenCalled();
  });
  it("does not pretend navigation cancels a clear already accepted by beginClear; it continues the original CID after drain", async () => {
    const confirm = await open("clear"), slow = deferred(); f.sdk.getAdvancedHistoryMessageList.mockReturnValueOnce(slow.promise);
    const history = IMSDK.getAdvancedHistoryMessageList({ conversationID: "cid-group-A", count: 1 } as any).catch(() => undefined);
    let pending!: Promise<unknown>; await act(async () => { pending = confirm(); await flush(); });
    expect(f.sdk.clearConversationAndDeleteAllMsg).not.toHaveBeenCalled();
    await act(async () => { await enter("B"); });
    await act(async () => { slow.resolve({ data: {} }); await history; await pending; });
    expect(f.sdk.clearConversationAndDeleteAllMsg).toHaveBeenCalledExactlyOnceWith("cid-group-A"); expect(f.extraClear).not.toHaveBeenCalled();
  });
  it.each(["dismiss", "clear"] as const)("does not dispatch a second %s on a synchronous repeated confirmation", async (kind) => {
    const confirm = await open(kind), slow = deferred(); native(kind).mockReturnValueOnce(slow.promise); let first!: Promise<unknown>;
    try { await act(async () => { first = confirm(); await flush(); await confirm(); }); expect(native(kind)).toHaveBeenCalledOnce(); }
    finally { await act(async () => { slow.resolve({ data: {} }); await first; }); }
  });
  it.each(actions)("still reports a current-view %s failure and does not fake successful close", async (kind) => {
    const confirm = await open(kind); native(kind).mockRejectedValueOnce(new Error("current failure"));
    await act(async () => { await confirm(); }); expect(f.toast).toHaveBeenCalledOnce(); expect(f.close).not.toHaveBeenCalled();
  });
});
