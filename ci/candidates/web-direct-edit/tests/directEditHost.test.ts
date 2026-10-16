// @vitest-environment jsdom
import React, { act } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import GroupSetting from "@/pages/chat/queryChat/GroupSetting";
import type { OverlayVisibleHandle } from "@/hooks/useOverlayVisible";
import { deferred, enter, f, flush, member, reset, store, surface } from "./fixture";

// Actual index -> GroupSettings -> selected hook, real EditableContent and
// VerificationSelectContent. AntD presentation/input transport and native SDK
// are adapted; this is not a browser/WASM/native upload acceptance claim.
let ui: ReturnType<typeof surface>, ref: React.RefObject<OverlayVisibleHandle | null>;
const file = () => new File(["synthetic"], "avatar.png", { type: "image/png" });
beforeEach(async () => {
  await reset(); ui = surface(); ref = React.createRef<OverlayVisibleHandle>();
  await ui.render(React.createElement(GroupSetting, { ref }));
  await act(async () => { ref.current!.openOverlay(); });
});
afterEach(async () => { await ui.dispose(); });
async function upload() {
  expect(f.upload).toBeTypeOf("function"); await f.upload!({ file: file() });
}

describe("actual group edit caller continuations", () => {
  it("keeps a valid avatar upload followed by its exact group metadata edit", async () => {
    await act(async () => { await upload(); });
    expect(f.sdk.uploadFile).toHaveBeenCalledOnce();
    expect(f.sdk.setGroupInfo).toHaveBeenCalledExactlyOnceWith({ groupID: "A", faceURL: "https://objects.fixture.invalid/owner-avatar.png" });
    expect(f.toast).not.toHaveBeenCalled();
  });
  it("does not start a new upload through a retained callback after Drawer close", async () => {
    const old = f.upload!; await act(async () => { ref.current!.closeOverlay(); await old({ file: file() }); });
    expect(f.sdk.uploadFile).not.toHaveBeenCalled(); expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("rejects a new selection during accepted metadata save and releases waiting across close/reopen for an explicit new selection", async () => {
    const metadata = deferred(); f.sdk.setGroupInfo.mockReturnValueOnce(metadata.promise); let pending!: Promise<void>;
    try {
      await act(async () => { pending = upload(); await flush(); });
      expect(f.sdk.uploadFile).toHaveBeenCalledOnce(); expect(f.sdk.setGroupInfo).toHaveBeenCalledOnce();
      // A file picker opened earlier can still call customRequest while the
      // rendered Upload is disabled. Admission must also be synchronous.
      await act(async () => { await f.upload!({ file: new File(["blocked"], "blocked.png", { type: "image/png" }) }); });
      expect(f.sdk.uploadFile).toHaveBeenCalledOnce(); expect(f.sdk.setGroupInfo).toHaveBeenCalledOnce();
      expect(f.uploadDisabled).toBe(true); expect(f.toast).toHaveBeenCalledOnce();
      expect(f.toast.mock.calls[0][0].error).toEqual(new Error("errCode.operationTooFrequent"));
      await act(async () => { ref.current!.closeOverlay(); ref.current!.openOverlay(); });
    } finally { await act(async () => { metadata.resolve({ data: {} }); await pending; }); }
    expect(f.drawerOpen).toBe(true); expect(f.uploadDisabled).toBe(false);
    f.sdk.uploadFile.mockResolvedValueOnce({ data: { url: "https://objects.fixture.invalid/explicit-retry.png" } });
    await act(async () => { await upload(); });
    expect(f.sdk.uploadFile).toHaveBeenCalledTimes(2); expect(f.sdk.setGroupInfo).toHaveBeenCalledTimes(2);
    expect(f.sdk.setGroupInfo).toHaveBeenLastCalledWith({ groupID: "A", faceURL: "https://objects.fixture.invalid/explicit-retry.png" });
    expect(f.toast).toHaveBeenCalledOnce();
  });
  it.each(["close", "close-reopen", "CID", "ABA", "owner"] as const)("does not begin the metadata write after an already-dispatched upload loses %s", async (change) => {
    const slow = deferred(); f.sdk.uploadFile.mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    try {
      await act(async () => { pending = upload(); await flush(); }); expect(f.sdk.uploadFile).toHaveBeenCalledOnce();
      await act(async () => {
        if (change === "close") ref.current!.closeOverlay();
        if (change === "close-reopen") { ref.current!.closeOverlay(); ref.current!.openOverlay(); }
        if (change === "CID") await enter("B");
        if (change === "ABA") { await enter("B"); await enter("A"); }
        if (change === "owner") { f.session.current = f.owner(); f.session.generation++; }
      });
    } finally { await act(async () => { slow.resolve({ data: { url: "https://objects.fixture.invalid/late.png" } }); await pending; }); }
    expect(f.sdk.setGroupInfo).not.toHaveBeenCalled(); expect(f.toast).not.toHaveBeenCalled();
    expect(f.sdk.uploadFile).toHaveBeenCalledOnce();
  });
  it("suppresses a delayed upload failure after actual Drawer close", async () => {
    const slow = deferred(); f.sdk.uploadFile.mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    try {
      await act(async () => { pending = upload(); await flush(); }); expect(f.sdk.uploadFile).toHaveBeenCalledOnce();
      await act(async () => { ref.current!.closeOverlay(); });
    } finally { await act(async () => { slow.reject(new Error("late upload failure")); await pending; }); }
    expect(f.toast).not.toHaveBeenCalled(); expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("does not begin avatar metadata mutation when permission was lost during upload", async () => {
    const slow = deferred(); f.sdk.uploadFile.mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    try {
      await act(async () => { pending = upload(); await flush(); }); expect(f.sdk.uploadFile).toHaveBeenCalledOnce();
      await act(async () => { store.setState({ currentMemberInGroup: member(20) }); });
    } finally { await act(async () => { slow.resolve({ data: { url: "https://objects.fixture.invalid/late.png" } }); await pending; }); }
    expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("keeps the newer completed avatar when the older upload succeeds last", async () => {
    const first = deferred(), second = deferred();
    f.sdk.uploadFile.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    let older!: Promise<void>, newer!: Promise<void>;
    try {
      await act(async () => {
        older = f.upload!({ file: new File(["first"], "first.png", { type: "image/png" }) });
        newer = f.upload!({ file: new File(["second"], "second.png", { type: "image/png" }) });
        await flush();
      });
      expect(f.sdk.uploadFile).toHaveBeenCalledTimes(2);
      await act(async () => { second.resolve({ data: { url: "https://objects.fixture.invalid/second.png" } }); await newer; });
      expect(f.sdk.setGroupInfo).toHaveBeenCalledExactlyOnceWith({ groupID: "A", faceURL: "https://objects.fixture.invalid/second.png" });
    } finally {
      await act(async () => {
        second.resolve({ data: { url: "https://objects.fixture.invalid/second.png" } });
        first.resolve({ data: { url: "https://objects.fixture.invalid/first.png" } });
        await Promise.all([older, newer]);
      });
    }
    expect(f.sdk.setGroupInfo).toHaveBeenCalledOnce(); expect(f.toast).not.toHaveBeenCalled();
  });
  it("does not report an older upload failure after the newer avatar has completed", async () => {
    const first = deferred(), second = deferred();
    f.sdk.uploadFile.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    let older!: Promise<void>, newer!: Promise<void>;
    try {
      await act(async () => {
        older = f.upload!({ file: new File(["first"], "first.png", { type: "image/png" }) });
        newer = f.upload!({ file: new File(["second"], "second.png", { type: "image/png" }) });
        await flush();
      });
      expect(f.sdk.uploadFile).toHaveBeenCalledTimes(2);
      await act(async () => { second.resolve({ data: { url: "https://objects.fixture.invalid/second.png" } }); await newer; });
      expect(f.sdk.setGroupInfo).toHaveBeenCalledExactlyOnceWith({ groupID: "A", faceURL: "https://objects.fixture.invalid/second.png" });
    } finally {
      await act(async () => {
        second.resolve({ data: { url: "https://objects.fixture.invalid/second.png" } });
        first.reject(new Error("superseded upload failure")); await Promise.all([older, newer]);
      });
    }
    expect(f.sdk.setGroupInfo).toHaveBeenCalledOnce(); expect(f.toast).not.toHaveBeenCalled();
  });
  it("does not submit group-name editing that began before the current admin became ordinary", async () => {
    const edit = ui.host.querySelector<HTMLImageElement>('img[alt="edit name"]'); expect(edit).not.toBeNull();
    await act(async () => { edit!.click(); }); expect(ui.host.querySelector("input")).not.toBeNull();
    await act(async () => { store.setState({ currentMemberInGroup: member(20) }); });
    // Existing EditableContent keeps its input when editable turns false. The
    // final action guard must reject without relying on icon visibility.
    const input = ui.host.querySelector<HTMLInputElement>("input"); expect(input).not.toBeNull();
    await act(async () => {
      input!.value = "different group name";
      input!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
      expect(f.inputPending).toBeDefined(); await f.inputPending;
    });
    expect(f.sdk.setGroupInfo).not.toHaveBeenCalled();
  });
  it("keeps the actual verification selector's AllNot=2 payload", async () => {
    const choice = [...ui.host.querySelectorAll<HTMLDivElement>("div.cursor-pointer")].find((item) => item.textContent === "placeholder.applyAll");
    expect(choice).toBeDefined();
    await act(async () => { choice!.click(); await flush(); });
    expect(f.sdk.setGroupInfo).toHaveBeenCalledExactlyOnceWith({ groupID: "A", needVerification: 2 });
  });
});
