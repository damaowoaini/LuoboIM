// @vitest-environment jsdom
import { f, reset, surface, member, store, deferred, flush, onOk, click } from "./groupFixture";
import React, { act } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import GroupSetting from "@/pages/chat/queryChat/GroupSetting";
import type { OverlayVisibleHandle } from "@/hooks/useOverlayVisible";
let ui: ReturnType<typeof surface>, ref: React.RefObject<OverlayVisibleHandle | null>;
beforeEach(async () => { await reset(); ui = surface(); ref = React.createRef();
  await ui.render(React.createElement(GroupSetting, { ref })); await act(async () => { ref.current!.openOverlay(); });
});
afterEach(async () => { await ui.dispose(); });
async function edit(which = 0, value = "saved draft") {
  const icons = ui.host.querySelectorAll<HTMLImageElement>('img[alt="edit name"]'); expect(icons.length).toBeGreaterThan(which);
  await act(async () => { icons[which].click(); }); const input = ui.host.querySelector<HTMLInputElement>("input")!;
  input.value = value; return input;
}
async function submit(input: HTMLInputElement) {
  await act(async () => { input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })); await f.inputPending; });
}
describe("actual group editors and acknowledged hooks", () => {
  it.each(["name", "nickname"] as const)("retains %s draft on a real wrapped SDK error", async (kind) => {
    const native = kind === "name" ? f.sdk.setGroupInfo : f.sdk.setGroupMemberInfo; native.mockRejectedValueOnce(new Error("rejected"));
    const input = await edit(kind === "name" ? 0 : 1); await submit(input);
    expect(native).toHaveBeenCalledOnce(); expect(f.toast).toHaveBeenCalledOnce();
    expect(ui.host.querySelector<HTMLInputElement>("input")?.value).toBe("saved draft");
    await submit(ui.host.querySelector<HTMLInputElement>("input")!); expect(native).toHaveBeenCalledTimes(2);
    expect(ui.host.querySelector("input")).toBeNull();
  });
  it("retains the name draft on a busy group operation instead of pretending it saved", async () => {
    const slow = deferred(); f.sdk.dismissGroup.mockReturnValueOnce(slow.promise); let pending!: Promise<unknown>;
    try { await click(ui.host, "placeholder.disbandGroup"); await act(async () => { pending = onOk()(); await flush(); });
      expect(f.sdk.dismissGroup).toHaveBeenCalledOnce(); const input = await edit(); await submit(input);
      expect(f.sdk.setGroupInfo).not.toHaveBeenCalled(); expect(ui.host.querySelector<HTMLInputElement>("input")?.value).toBe("saved draft");
    } finally { await act(async () => { slow.resolve({ data: {} }); await pending; }); }
  });
  it("preserves a personally muted ordinary member's own nickname editor", async () => {
    await act(async () => { store.setState({ currentMemberInGroup: member(20, { muteEndTime: Date.now() + 60_000 }) }); });
    const input = await edit(); await submit(input);
    expect(f.sdk.setGroupMemberInfo).toHaveBeenCalledExactlyOnceWith({ groupID: "A", userID: "actor", nickname: "saved draft" });
    expect(ui.host.querySelector("input")).toBeNull();
  });
  it("does not close a reopened Drawer editor after the older acknowledged save settles", async () => {
    const slow = deferred(); f.sdk.setGroupInfo.mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    try { const input = await edit(0, "old"); await act(async () => { input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })); pending = f.inputPending!; await flush(); });
      expect(f.sdk.setGroupInfo).toHaveBeenCalledOnce(); await act(async () => { ref.current!.closeOverlay(); });
      await act(async () => { ref.current!.openOverlay(); });
      await edit(0, "new draft");
    } finally { await act(async () => { slow.resolve({ data: {} }); await pending; }); }
    expect(f.sdk.setGroupInfo).toHaveBeenCalledOnce();
    expect(ui.host.querySelector<HTMLInputElement>("input")?.value).toBe("new draft");
  });
});
