// @vitest-environment jsdom
import React, { act } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { click, deferred, f, flush, onOk, reset, surface } from "./fixture";
import GroupSetting from "@/pages/chat/queryChat/GroupSetting";
// Real index -> real GroupSettings -> real two hooks. Only unrelated widgets,
// member preview, Drawer presentation and native transport are adapted.
let ui: ReturnType<typeof surface>, ref: React.RefObject<any>;
beforeEach(async () => { await reset(); ui = surface(); ref = React.createRef<any>();
  await ui.render(React.createElement(GroupSetting, { ref })); await act(async () => ref.current.openOverlay()); });
afterEach(async () => { await ui.dispose(); });

describe("actual GroupSettings activity propagation to confirmation hooks", () => {
  it.each(["placeholder.disbandGroup", "toast.clearChatHistory"])("invalidates %s immediately on Drawer close before exit unmount", async (label) => {
    await click(ui.host, label); const confirm = onOk();
    await act(async () => { f.drawerClose!(); await confirm(); });
    expect(f.sdk.dismissGroup).not.toHaveBeenCalled(); expect(f.sdk.clearConversationAndDeleteAllMsg).not.toHaveBeenCalled();
  });
  it("never lets an old dismiss ACK close a same-group reopened Drawer", async () => {
    await click(ui.host, "placeholder.disbandGroup"); const confirm = onOk(), slow = deferred(); f.sdk.dismissGroup.mockReturnValueOnce(slow.promise);
    let pending!: Promise<unknown>; await act(async () => { pending = confirm(); await flush(); }); expect(f.sdk.dismissGroup).toHaveBeenCalledOnce();
    await act(async () => { ref.current.closeOverlay(); ref.current.openOverlay(); }); expect(f.drawerOpen).toBe(true);
    await act(async () => { slow.resolve({ data: {} }); await pending; }); expect(f.drawerOpen).toBe(true); expect(f.toast).not.toHaveBeenCalled();
  });
  it("drops the old all-mute confirm immediately when member preview is entered", async () => {
    await click(ui.host, "placeholder.allMuted"); const confirm = onOk();
    const members = [...ui.host.querySelectorAll("button")].find((button) => button.textContent === "members"); expect(members).toBeDefined();
    await act(async () => { members!.click(); await confirm(); }); expect(f.sdk.changeGroupMute).not.toHaveBeenCalled();
  });
  it("keeps a newly opened current settings action usable after a same-group close and reopen", async () => {
    await act(async () => { ref.current.closeOverlay(); ref.current.openOverlay(); });
    await click(ui.host, "placeholder.disbandGroup"); const confirm = onOk();
    await act(async () => { await confirm(); }); expect(f.sdk.dismissGroup).toHaveBeenCalledExactlyOnceWith("A"); expect(f.drawerOpen).toBe(false);
  });
  it("silences an old native failure after the settings Drawer has closed but remains mounted for exit", async () => {
    await click(ui.host, "placeholder.disbandGroup"); const confirm = onOk(), slow = deferred(); f.sdk.dismissGroup.mockReturnValueOnce(slow.promise);
    let pending!: Promise<unknown>; await act(async () => { pending = confirm(); await flush(); }); expect(f.sdk.dismissGroup).toHaveBeenCalledOnce();
    await act(async () => f.drawerClose!());
    await act(async () => { slow.reject(new Error("old drawer request")); await pending; }); expect(f.toast).not.toHaveBeenCalled(); expect(f.drawerOpen).toBe(false);
  });
});
