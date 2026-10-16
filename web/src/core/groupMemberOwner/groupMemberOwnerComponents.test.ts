// @vitest-environment jsdom
import React, { act } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { action, clickLabel, confirmation, deferred, emit, enter, f, flush, group, member, owner, reset, save, store, surface } from "./fixture";
import GroupMemberList from "@/pages/chat/queryChat/GroupSetting/GroupMemberList";
import GroupSetting from "@/pages/chat/queryChat/GroupSetting";
let ui: ReturnType<typeof surface>;
beforeEach(async () => { await reset(); ui = surface(); });
afterEach(async () => { await ui.dispose(); });
const render = (isSearching = false) => ui.render(React.createElement(GroupMemberList, { isSearching }));
async function kickDialog() { await render(); await act(async () => action("kick")()); return confirmation(); }
const noWrites = () => { expect(f.kick).not.toHaveBeenCalled(); expect(f.mute).not.toHaveBeenCalled(); expect(f.admin).not.toHaveBeenCalled(); };

describe("real group management original intent and latest authority", () => {
  it.each([
    ["same-generation owner replacement", () => { f.session.current = owner(); }],
    ["generation change", () => { f.session.generation++; }],
    ["issuer mutated in place", () => { f.session.current.issuer = "issuer-B"; }],
    ["tenant mutated in place", () => { f.session.current.tenant.serverID = "tenant-B"; }],
    ["actor changed in UI", () => { f.user.selfInfo.userID = "other"; }],
  ] as const)("rejects retained kick confirmation after %s", async (_name, mutate) => {
    const onOk = await kickDialog(); mutate(); await act(async () => { await onOk(); }); noWrites();
  });
  it("rejects an old confirmation across A-B-A using the real private navigation epoch", async () => {
    const onOk = await kickDialog(); await act(async () => { await enter("B"); await enter("A"); });
    await act(async () => { await onOk(); }); noWrites();
  });
  it("rejects a retained confirmation after member-list unmount", async () => {
    const onOk = await kickDialog(); await ui.dispose(); await onOk(); noWrites();
  });
  it("invalidates immediately on real Drawer close even while its exit keeps the list mounted", async () => {
    const ref = React.createRef<any>(); await ui.render(React.createElement(GroupSetting, { ref }));
    await act(async () => { ref.current.openOverlay(); });
    const show = [...ui.host.querySelectorAll("button")].find((b) => b.textContent === "show-members");
    expect(show).toBeDefined(); await act(async () => show!.click());
    await act(async () => action("kick")()); const onOk = confirmation();
    await act(async () => { f.drawerClose!(); await onOk(); }); noWrites();
  });
  it("does not resurrect old confirmation after a close-and-reopen in the same group", async () => {
    const ref = React.createRef<any>(); await ui.render(React.createElement(GroupSetting, { ref }));
    await act(async () => ref.current.openOverlay());
    await act(async () => [...ui.host.querySelectorAll("button")].find((b) => b.textContent === "show-members")!.click());
    await act(async () => action("kick")()); const onOk = confirmation();
    await act(async () => { ref.current.closeOverlay(); ref.current.openOverlay(); await onOk(); }); noWrites();
    f.confirm.mockClear(); await act(async () => action("kick")());
    await act(async () => { await confirmation()(); }); expect(f.kick).toHaveBeenCalledOnce();
  });
  it("keeps a valid confirmation across unrelated same-group conversation metadata", async () => {
    const onOk = await kickDialog(); await act(async () => store.getState().updateCurrentConversation({ ...store.getState().currentConversation!, showName: "metadata" }));
    await act(async () => { await onOk(); });
    expect(f.kick).toHaveBeenCalledExactlyOnceWith({ groupID: "A", userIDList: ["peer"], reason: "" });
  });
  it("suppresses stale rejection feedback after one real dispatched call changes owner", async () => {
    const onOk = await kickDialog(), slow = deferred(); f.kick.mockReturnValueOnce(slow.promise);
    let pending!: Promise<unknown>;
    await act(async () => { pending = onOk(); }); expect(f.kick).toHaveBeenCalledOnce();
    f.session.current = owner(); f.session.generation++;
    await act(async () => { slow.reject(new Error("old action")); await pending; }); expect(f.toast).not.toHaveBeenCalled();
  });
  it("blocks duplicate native operations on the same target while the first confirmation is pending", async () => {
    const onOk = await kickDialog(), slow = deferred(); f.kick.mockReturnValueOnce(slow.promise);
    let pending!: Promise<unknown>;
    try { await act(async () => { pending = onOk(); await onOk(); }); expect(f.kick).toHaveBeenCalledOnce(); }
    finally { await act(async () => { slow.resolve({ data: {} }); await pending; }); }
  });
  it("rechecks actual latest self role at confirmation", async () => {
    const onOk = await kickDialog(); await act(async () => store.setState({ currentMemberInGroup: member("actor", 20) as any }));
    await act(async () => { await onOk(); }); noWrites();
  });
  it("does not resurrect a confirmation when self role is revoked then restored before clicking", async () => {
    const onOk = await kickDialog();
    await act(async () => { store.setState({ currentMemberInGroup: member("actor", 20) as any }); emit("changed", member("actor", 20));
      store.setState({ currentMemberInGroup: member("actor", 100) as any }); emit("changed", member("actor", 100)); });
    await act(async () => { await onOk(); }); noWrites();
  });
  it("self removal blocks old and newly opened actions until the exact self membership is reread", async () => {
    const onOk = await kickDialog();
    await act(async () => { emit("deleted", member("actor")); await flush(); });
    await act(async () => { await onOk(); action("kick")(); }); noWrites(); expect(f.confirm).toHaveBeenCalledOnce();
    await act(async () => store.setState({ currentMemberInGroup: member("actor", 100) as any }));
    await act(async () => action("kick")()); expect(f.confirm).toHaveBeenCalledTimes(2);
  });
  it("updates search role facts and rejects an administrator's already-open confirmation after peer promotion", async () => {
    store.setState({ currentMemberInGroup: member("actor", 60) as any });
    const ref = React.createRef<any>(); await ui.render(React.createElement(GroupMemberList, { ref, isSearching: true }));
    await act(async () => { await ref.current.searchMember("peer"); });
    await act(async () => action("kick")()); const onOk = confirmation();
    await act(async () => { emit("changed", member("peer", 60)); });
    expect(ui.host.textContent).toContain("placeholder.administrator");
    await act(async () => { await onOk(); }); noWrites();
  });
  it("keeps management usable for a personally muted administrator even when canSend is false", async () => {
    store.setState({ currentMemberInGroup: member("actor", 60, { muteEndTime: Date.now() + 60_000 }) as any,
      currentGroupInfo: { ...group(), status: 3 } as any });
    expect(store.getState().captureConversationSendScope()!.canSend()).toBe(false);
    const onOk = await kickDialog(); await act(async () => { await onOk(); }); expect(f.kick).toHaveBeenCalledOnce();
  });
  it("preserves owner role toggle payload, normal profile target and role restrictions", async () => {
    await render(); await act(async () => { await action("setAdministrator")(); });
    expect(f.admin).toHaveBeenCalledExactlyOnceWith({ groupID: "A", userID: "peer", roleLevel: 60 });
    await act(async () => { f.profileAction!(); }); expect(f.profile).toHaveBeenCalledExactlyOnceWith("peer", "A");
    await act(async () => store.setState({ currentMemberInGroup: member("actor", 20) as any }));
    expect(ui.host.querySelector('[data-tooltip="placeholder.kick"]')).toBeNull();
  });
  it("does not let a retained profile callback open an old member under a new owner", async () => {
    await render(); const open = f.profileAction!; expect(open).toBeTypeOf("function"); f.session.current = owner();
    await act(async () => open()); expect(f.profile).not.toHaveBeenCalled();
  });
  it("rejects an administrator-toggle callback captured before owner replacement", async () => {
    await render(); const toggle = action("setAdministrator"); f.session.current = owner();
    await act(async () => { await toggle(); }); noWrites();
  });
  it("never applies an old mute save to a newer selected target, and resets the newer option", async () => {
    await render(); await act(async () => action("mute")()); await clickLabel(ui.host, "date.minute:10"); const oldSave = save();
    f.rows = [member("peer-two")];
    await act(async () => { emit("added", member("peer-two")); await flush(); });
    await act(async () => action("mute")()); const newSave = save();
    await act(async () => { await oldSave(); await newSave(); }); expect(f.mute).not.toHaveBeenCalled();
    await clickLabel(ui.host, "date.hour:1"); await act(async () => { await save()(); });
    expect(f.mute).toHaveBeenCalledExactlyOnceWith({ groupID: "A", userID: "peer-two", mutedSeconds: 3600 });
  });
  it("preserves explicit cancel-mute zero and never dispatches a mute save after owner rotation", async () => {
    await render(); await act(async () => action("mute")()); await clickLabel(ui.host, "placeholder.cancelMute"); const old = save();
    f.session.current = owner(); await act(async () => { await old(); }); expect(f.mute).not.toHaveBeenCalled();
  });
  it("does not overwrite a newer mute fact received while its original option dialog is open", async () => {
    await render(); await act(async () => action("mute")()); await clickLabel(ui.host, "date.minute:10"); const old = save();
    await act(async () => emit("changed", member("peer", 20, { muteEndTime: Date.now() + 10_000 })));
    await act(async () => { await old(); }); expect(f.mute).not.toHaveBeenCalled();
  });
});
