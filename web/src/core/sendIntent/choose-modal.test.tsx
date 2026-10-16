// @vitest-environment jsdom
import React, { act, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { deferred, enter, fixture, flush, reset, store, member } from "./harness";
vi.mock("antd", async () => ({ Button: ({ children, onClick }: any) => <button onClick={onClick}>{children}</button>,
  Modal: ({ children, open }: any) => open ? <section>{children}</section> : null, Input: () => null, Upload: () => null }));
vi.mock("@ant-design/icons", () => ({ CloseOutlined: () => null }));
vi.mock("@/components/OIMAvatar", () => ({ default: () => null }));
vi.mock("@/pages/common/ChooseModal/ChooseBox", async () => {
  const { forwardRef, useImperativeHandle } = await import("react");
  return { default: forwardRef(function Box(_props, ref) { useImperativeHandle(ref, () => ({ getCheckedList: () => fixture.selection,
    getAdditional: () => fixture.additional, resetState: () => {}, updatePrevCheckList: () => {} })); return <div>recipient selection adapter</div>; }) };
});
import ChooseModal, { type ChooseModalState } from "@/pages/common/ChooseModal";
import type { OverlayVisibleHandle } from "@/hooks/useOverlayVisible";
import { captureSendOrigin } from "@/core/messageSendIntent";
import { messages, message } from "./harness";
import { messageOwner, messagePrivacyState } from "@/core/messagePrivacyRuntime";
let host: HTMLDivElement, root: Root;
beforeEach(() => { reset(); host = document.createElement("div"); document.body.append(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });
const open = (type: ChooseModalState["type"], extra?: unknown, sourceConversationID?: string) => {
  const ref = createRef<OverlayVisibleHandle>();
  const origin = captureSendOrigin(store.getState().captureConversationSendScope?.(), sourceConversationID);
  act(() => root.render(<ChooseModal ref={ref} state={{ type, sendOrigin: origin, sourceConversationID, sourceOwner: messageOwner(), extraData: extra ?? (type === "SHARE_CARD" ? { userID: "card", nickname: "card" } : undefined) }} />));
  act(() => ref.current!.openOverlay());
};
const confirm = async () => { await act(async () => { [...host.querySelectorAll("button")].at(-1)!.click(); await flush(); }); };

describe("actual ChooseModal, batch delivery and send hook", () => {
  it("does not retarget an open SELECT_CARD modal after A→B→A", async () => {
    fixture.selection = [{ userID: "card", nickname: "card" }]; open("SELECT_CARD"); act(() => { enter("B"); enter("A"); }); await confirm();
    expect(fixture.sdk.createCardMessage).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("does not dispatch a card that finished creation after navigation", async () => {
    fixture.selection = [{ userID: "card" }]; open("SELECT_CARD"); const reply = deferred<any>(); fixture.sdk.createCardMessage.mockReturnValueOnce(reply.promise);
    await confirm(); expect(fixture.sdk.createCardMessage).toHaveBeenCalledOnce(); act(() => enter("B")); reply.resolve({ data: { clientMsgID: "card-created", status: 1 } });
    await act(flush); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("preserves explicit multi-target user/group forwarding and additional messages outside active A", async () => {
    fixture.selection = [{ userID: "target-1" }, { groupID: "G" }]; fixture.additional = "additional"; open("SHARE_CARD"); await confirm();
    expect(fixture.sdk.sendMessage.mock.calls.map(([p]) => [p.recvID, p.groupID])).toEqual([["target-1", ""], ["target-1", ""], ["", "G"], ["", "G"]]);
    expect(messages.getState().pushNewMessage).not.toHaveBeenCalled(); expect(messages.getState().tryAddPreviewImg).not.toHaveBeenCalled();
  });
  it("does not append or repeat a recipient whose primary ACK is unknown", async () => {
    fixture.selection = [{ userID: "target-1" }, { userID: "target-2" }]; fixture.additional = "additional";
    fixture.sdk.sendMessage.mockRejectedValueOnce(new Error("synthetic unknown acknowledgement")); open("SHARE_CARD"); await confirm();
    expect(fixture.sdk.sendMessage.mock.calls.map(([p]) => p.recvID)).toEqual(["target-1", "target-2", "target-2"]);
  });
  it("retains the original history source CID when forwarding to other selected chats", async () => {
    fixture.selection = [{ userID: "target-1" }]; const source = message("source-in-search");
    messagePrivacyState({ ...source, attachedInfoElem: { isPrivateChat: true } }, "search-source-cid");
    open("FORWARD_MESSAGE", source, "search-source-cid"); await confirm();
    expect(fixture.sdk.createForwardMessage).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("keeps ordinary historical-source forwards functional outside the source/active chat", async () => {
    fixture.selection = [{ userID: "target-1" }, { groupID: "G" }]; const source = message("ordinary-search");
    open("FORWARD_MESSAGE", source, "search-source-cid"); await confirm();
    expect(fixture.sdk.createForwardMessage).toHaveBeenCalledTimes(2);
    expect(fixture.sdk.sendMessage.mock.calls.map(([p]) => [p.recvID, p.groupID])).toEqual([["target-1", ""], ["", "G"]]);
  });
  const afterOnly = (globalThis as typeof globalThis & { process?: { env?: { LUOBO_SEND_INTENT_STAGE?: string } } }).process?.env?.LUOBO_SEND_INTENT_STAGE === "before" ? it.skip : it;
  afterOnly("checks group membership before creating any forwarded message", async () => {
    fixture.selection = [{ groupID: "G" }]; fixture.sdk.getSpecifiedGroupMembersInfo.mockResolvedValue({ data: [member("G", "foreign")] }); open("SHARE_CARD"); await confirm();
    expect(fixture.sdk.createCardMessage).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
});
