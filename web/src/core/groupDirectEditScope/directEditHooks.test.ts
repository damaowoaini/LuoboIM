// @vitest-environment jsdom
import { deferred, enter, f, flush, group, member, onOk, reset, store, surface } from "./fixture";
import React, { act } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { GroupVerificationType } from "open-im-sdk-wasm";
import { useGroupSettings } from "@/pages/chat/queryChat/GroupSetting/useGroupSettings";

const kinds = ["info", "nickname", "verification"] as const;
type Kind = typeof kinds[number];
let ui: ReturnType<typeof surface>, settings: ReturnType<typeof useGroupSettings>;
function Probe({ isActive }: { isActive?: () => boolean }) {
  settings = useGroupSettings({ closeOverlay: f.close, isActive }); return null;
}
function action(kind: Kind) {
  const original = settings;
  return () => kind === "info" ? original.updateGroupInfo({ groupName: "changed" }) :
    kind === "nickname" ? original.updateNickNameInGroup("my nickname") :
    original.updateGroupVerification(GroupVerificationType.AllNot);
}
const native = (kind: Kind) => kind === "nickname" ? f.sdk.setGroupMemberInfo : f.sdk.setGroupInfo;
beforeEach(async () => { await reset(); ui = surface(); });
afterEach(async () => { await ui.dispose(); });

describe("actual remaining group edit hooks", () => {
  it.each(kinds)("keeps the normal exact-target %s payload", async (kind) => {
    await ui.render(React.createElement(Probe)); await act(async () => { await action(kind)(); });
    const expected = kind === "info" ? { groupID: "A", groupName: "changed" } :
      kind === "nickname" ? { groupID: "A", userID: "actor", nickname: "my nickname" } :
      { groupID: "A", needVerification: 2 };
    expect(native(kind)).toHaveBeenCalledExactlyOnceWith(expected); expect(f.toast).not.toHaveBeenCalled();
    const error = new Error("current edit failure"); native(kind).mockRejectedValueOnce(error);
    await act(async () => { await action(kind)(); });
    expect(native(kind)).toHaveBeenCalledTimes(2);
    expect(f.toast).toHaveBeenCalledExactlyOnceWith(kind === "nickname" ? { error } :
      { error, msg: kind === "info" ? "toast.updateGroupInfoFailed" : "toast.updateGroupVerificationFailed" });
  });
  it.each(kinds)("allows %s for a personally muted admin after same-CID metadata replacement", async (kind) => {
    store.setState({ currentMemberInGroup: member(60, { muteEndTime: Date.now() + 60_000 }) });
    await ui.render(React.createElement(Probe)); const update = action(kind);
    await act(async () => {
      store.getState().updateCurrentConversation({ ...store.getState().currentConversation!, unreadCount: 7 });
      store.setState({ currentGroupInfo: { ...group(), groupName: "refreshed" } });
    });
    expect(store.getState().captureConversationSendScope()!.canSend()).toBe(false);
    await act(async () => { await update(); }); expect(native(kind)).toHaveBeenCalledOnce();
  });
  it("does not dispatch an old group edit after same-generation owner replacement", async () => {
    await ui.render(React.createElement(Probe)); const update = action("info"); f.session.current = f.owner();
    await act(async () => { await update(); }); expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("does not dispatch an old nickname edit after A-B-A navigation", async () => {
    await ui.render(React.createElement(Probe)); const update = action("nickname");
    await act(async () => { await enter("B"); await enter("A"); await update(); });
    expect(f.sdk.setGroupMemberInfo).not.toHaveBeenCalled();
  });
  it("does not dispatch verification from an inactive settings host", async () => {
    let active = true; await ui.render(React.createElement(Probe, { isActive: () => active }));
    const update = action("verification"); active = false;
    await act(async () => { await update(); }); expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it.each(["info", "verification"] as const)("rechecks latest admin permission for %s", async (kind) => {
    await ui.render(React.createElement(Probe)); const update = action(kind);
    await act(async () => { store.setState({ currentMemberInGroup: member(20) }); await update(); });
    expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("keeps an ordinary member's own nickname update available", async () => {
    store.setState({ currentMemberInGroup: member(20) }); await ui.render(React.createElement(Probe));
    await act(async () => { await action("nickname")(); });
    expect(f.sdk.setGroupMemberInfo).toHaveBeenCalledExactlyOnceWith({ groupID: "A", userID: "actor", nickname: "my nickname" });
  });
  it.each([
    ["foreign group", { groupID: "B" }],
    ["foreign actor", { userID: "other" }],
  ] as const)("does not use a latest %s self-member record for nickname", async (_name, patch) => {
    await ui.render(React.createElement(Probe)); const update = action("nickname");
    await act(async () => { store.setState({ currentMemberInGroup: member(20, patch) }); await update(); });
    expect(f.sdk.setGroupMemberInfo).not.toHaveBeenCalled();
  });
  it.each(kinds)("does not project a late %s native error into a left view", async (kind) => {
    await ui.render(React.createElement(Probe)); const slow = deferred(); native(kind).mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    try {
      await act(async () => { pending = action(kind)(); await flush(); }); expect(native(kind)).toHaveBeenCalledOnce();
      await ui.dispose();
    } finally { await act(async () => { slow.reject(new Error("old edit failure")); await pending; }); }
    expect(f.toast).not.toHaveBeenCalled();
  });
  it("does not begin a group edit through a callback retained past unmount", async () => {
    await ui.render(React.createElement(Probe)); const update = action("info"); await ui.dispose(); await update();
    expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("reports read-only waiting for all three direct edits while another accepted group operation is pending", async () => {
    await ui.render(React.createElement(Probe)); const slow = deferred();
    f.sdk.dismissGroup.mockReturnValueOnce(slow.promise); let pending!: Promise<unknown>;
    try {
      await act(async () => { settings.tryDismissGroup(); pending = onOk()(); await flush(); });
      expect(f.sdk.dismissGroup).toHaveBeenCalledExactlyOnceWith("A");
      for (const kind of kinds) await act(async () => { await action(kind)(); });
      expect(f.sdk.setGroupInfo).not.toHaveBeenCalled(); expect(f.sdk.setGroupMemberInfo).not.toHaveBeenCalled();
      expect(f.toast).toHaveBeenCalledTimes(3);
      for (const [options] of f.toast.mock.calls) expect(options.error).toEqual(new Error("errCode.operationTooFrequent"));
    } finally { await act(async () => { slow.resolve({ data: {} }); await pending; }); }
    expect(f.sdk.dismissGroup).toHaveBeenCalledOnce();
  });
});
