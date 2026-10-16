// @vitest-environment jsdom
import React, { act } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { deferred, enter, f, flush, group, member, reset, store, surface } from "./fixture";
import { useGroupSettings } from "@/pages/chat/queryChat/GroupSetting/useGroupSettings";

// The actual hook, real conversation scope and bound SDK wrappers are used.
// isActive is the hook's host contract; the six unchanged Drawer tests exercise
// the actual index -> GroupSettings -> hook activity propagation separately.
let ui: ReturnType<typeof surface>;
function Probe({ isActive }: { isActive?: () => boolean }) {
  f.api = useGroupSettings({ closeOverlay: f.close, isActive });
  return null;
}
async function render(isActive?: () => boolean) {
  await ui.render(React.createElement(Probe, { isActive }));
}
beforeEach(async () => { await reset(); ui = surface(); });
afterEach(async () => { await ui.dispose(); });

describe("actual group permission callback keeps its original owner and view", () => {
  it("becomes usable after metadata absent at first mount is loaded", async () => {
    store.setState({ currentGroupInfo: undefined }); await render();
    await act(async () => { await f.api.updateGroupMemberPermission(1, "lookMemberInfo"); });
    expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
    await act(async () => { store.setState({ currentGroupInfo: group() }); });
    await act(async () => { await f.api.updateGroupMemberPermission(1, "lookMemberInfo"); });
    expect(f.sdk.setGroupInfo).toHaveBeenCalledExactlyOnceWith({ groupID: "A", lookMemberInfo: 1 });
  });
  it("preserves both 0/1 fields for a personally muted admin after same-CID metadata refresh", async () => {
    store.setState({ currentMemberInGroup: member(60, { muteEndTime: Date.now() + 60_000 }) });
    expect(store.getState().captureConversationSendScope()!.canSend()).toBe(false); await render();
    await act(async () => {
      store.getState().updateCurrentConversation({ ...store.getState().currentConversation!, unreadCount: 3 });
      store.setState({ currentGroupInfo: { ...group(), groupName: "refreshed" } });
    });
    for (const field of ["applyMemberFriend", "lookMemberInfo"] as const) {
      for (const rule of [0, 1] as const) {
        await act(async () => { await f.api.updateGroupMemberPermission(rule, field); });
        expect(f.sdk.setGroupInfo).toHaveBeenLastCalledWith({ groupID: "A", [field]: rule });
      }
    }
    expect(f.sdk.setGroupInfo).toHaveBeenCalledTimes(4); expect(f.toast).not.toHaveBeenCalled();
  });
  it("rejects a retained permission callback after same-generation owner replacement", async () => {
    await render(); const update = f.api.updateGroupMemberPermission; f.session.current = f.owner();
    await act(async () => { await update(1, "lookMemberInfo"); }); expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("rejects a retained permission callback after A-B-A navigation", async () => {
    await render(); const update = f.api.updateGroupMemberPermission;
    await act(async () => { await enter("B"); await enter("A"); await update(1, "lookMemberInfo"); });
    expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("keeps a closed settings epoch invalid after reopening, while the new callback works", async () => {
    const activity = { open: true, epoch: 1 };
    await render(() => activity.open && activity.epoch === 1); const old = f.api.updateGroupMemberPermission;
    activity.open = false;
    await act(async () => { await old(1, "lookMemberInfo"); }); expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
    activity.epoch = 2; activity.open = true; await render(() => activity.open && activity.epoch === 2);
    await act(async () => { await old(1, "lookMemberInfo"); }); expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
    await act(async () => { await f.api.updateGroupMemberPermission(0, "applyMemberFriend"); });
    expect(f.sdk.setGroupInfo).toHaveBeenCalledExactlyOnceWith({ groupID: "A", applyMemberFriend: 0 });
  });
  it("uses the latest self role rather than the callback's original owner role", async () => {
    await render(); const update = f.api.updateGroupMemberPermission;
    await act(async () => { store.setState({ currentMemberInGroup: member(20) }); await update(1, "lookMemberInfo"); });
    expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("rejects foreign current-group metadata instead of sending the retained original target", async () => {
    await render(); const update = f.api.updateGroupMemberPermission;
    await act(async () => { store.setState({ currentGroupInfo: group("B") }); await update(1, "lookMemberInfo"); });
    expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("rejects a latest self-member row from another group", async () => {
    await render(); const update = f.api.updateGroupMemberPermission;
    await act(async () => { store.setState({ currentMemberInGroup: member(100, { groupID: "B" }) }); await update(1, "lookMemberInfo"); });
    expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("silences an old-owner error after one proven native permission dispatch", async () => {
    await render(); const slow = deferred(); f.sdk.setGroupInfo.mockReturnValueOnce(slow.promise); let pending!: Promise<unknown>;
    try {
      await act(async () => { pending = f.api.updateGroupMemberPermission(1, "lookMemberInfo"); await flush(); });
      expect(f.sdk.setGroupInfo).toHaveBeenCalledExactlyOnceWith({ groupID: "A", lookMemberInfo: 1 });
      f.session.current = f.owner();
    } finally { await act(async () => { slow.reject(new Error("old permission failure")); await pending; }); }
    expect(f.toast).not.toHaveBeenCalled(); expect(f.close).not.toHaveBeenCalled();
  });
  it("blocks concurrent permission writes, reports current failure, and releases busy for an explicit retry", async () => {
    await render(); const slow = deferred(); f.sdk.setGroupInfo.mockReturnValueOnce(slow.promise); let pending!: Promise<unknown>;
    try {
      await act(async () => { pending = f.api.updateGroupMemberPermission(1, "lookMemberInfo"); await flush(); });
      expect(f.sdk.setGroupInfo).toHaveBeenCalledOnce();
      await act(async () => { await f.api.updateGroupMemberPermission(0, "applyMemberFriend"); });
      expect(f.sdk.setGroupInfo).toHaveBeenCalledOnce();
    } finally { await act(async () => { slow.reject(new Error("current permission failure")); await pending; }); }
    expect(f.toast).toHaveBeenCalledOnce(); expect(f.sdk.setGroupInfo).toHaveBeenCalledOnce();
    await act(async () => { await f.api.updateGroupMemberPermission(0, "applyMemberFriend"); });
    expect(f.sdk.setGroupInfo).toHaveBeenCalledTimes(2);
    expect(f.sdk.setGroupInfo).toHaveBeenLastCalledWith({ groupID: "A", applyMemberFriend: 0 });
  });
  it("rejects a permission callback retained past actual hook unmount", async () => {
    await render(); const update = f.api.updateGroupMemberPermission; await ui.dispose(); await update(1, "lookMemberInfo");
    expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("does not dispatch fields or values outside the original permission contract", async () => {
    await render(); await act(async () => {
      await f.api.updateGroupMemberPermission(2, "lookMemberInfo");
      await f.api.updateGroupMemberPermission(1, "groupName");
    }); expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
});
