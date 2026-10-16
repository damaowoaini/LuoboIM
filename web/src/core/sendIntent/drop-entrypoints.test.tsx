// @vitest-environment jsdom
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { deferred, enter, fixture, flush, reset, store, conversation, message } from "./harness";
vi.mock("@/components/EditableDiv", () => ({ insertAtCursor: fixture.insert }));
import { useDropAndPaste } from "@/pages/chat/queryChat/useDropAndPaste";
import { useDropFileAndDom } from "@/pages/chat/ConversationSider/useDropFileAndDom";
import { useDropDomOnly } from "@/pages/chat/queryChat/ChatFooter/useDropDomOnly";
import { useSendMessage } from "@/pages/chat/queryChat/ChatFooter/useSendMessage";
import { messageOwner, messagePrivacyState } from "@/core/messagePrivacyRuntime";
let root: Root, host: HTMLDivElement;
function Drop() { useDropAndPaste({ currentConversation: store(state => state.currentConversation), getIsCanSendMessage: () => true }); return null; }
function Sidebar() { useDropFileAndDom({ domRef: { current: host }, currentConversation: conversation("target") }); return null; }
function DomOnly() { const sending = useSendMessage(); useDropDomOnly({ domRef: { current: host }, sendMessage: sending.sendMessage,
  beginCurrentSend: sending.beginCurrentSend }); return null; }
beforeEach(() => { reset(); host = document.createElement("div"); document.body.append(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });
const mount = (Component: React.ComponentType) => act(() => root.render(<Component />));
const files = [new File(["synthetic"], "test.bin", { type: "application/octet-stream" })];
const confirm = async () => { const result = fixture.confirmations.at(-1).onOk(); if (result) await result.catch(() => {}); await flush(); };

describe("same public hooks before/after: retained dialogs and local file awaits", () => {
  it("does not send a modal confirmed for A after navigation to B", async () => {
    mount(Drop); await act(async () => fixture.drop.onFiles(files)); expect(fixture.confirmations).toHaveLength(1);
    act(() => enter("B")); await act(confirm);
    expect(fixture.sdk.createFileMessageByFile).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("does not borrow B after A-confirmed local disk preparation finishes", async () => {
    mount(Drop); await act(async () => fixture.drop.onFiles(files)); const disk = deferred<string>(); fixture.save.mockReturnValueOnce(disk.promise);
    let pending: Promise<unknown> | undefined; act(() => { pending = fixture.confirmations[0].onOk(); }); await flush(); expect(fixture.save).toHaveBeenCalledOnce();
    act(() => enter("B")); disk.resolve("/task-synthetic/test.bin"); await act(async () => { await pending?.catch(() => {}); await flush(); });
    expect(fixture.sdk.createFileMessageByFile).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("preserves normal A file sends after a same-CID unread refresh", async () => {
    mount(Drop); await act(async () => fixture.drop.onFiles(files)); act(() => store.getState().updateCurrentConversation({ ...conversation(), unreadCount: 9 }));
    await act(confirm); expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce(); expect(fixture.sdk.sendMessage.mock.calls[0][0]).toMatchObject({ recvID: "A", groupID: "" });
  });
  it("preserves explicit non-active sidebar recipient", async () => {
    mount(Sidebar); await act(async () => fixture.drop.onFiles(files)); act(() => enter("B")); await act(confirm);
    expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce(); expect(fixture.sdk.sendMessage.mock.calls[0][0]).toMatchObject({ recvID: "target", groupID: "" });
  });
  it("does not forward a retained DOM modal into B", async () => {
    mount(DomOnly); const source = { ...message(), contentType: 105, fileElem: { fileName: "file.bin", fileSize: 9 } };
    act(() => fixture.drop.onDom({ message: source, conversationID: "cid-user-A", sourceOwner: messageOwner() }));
    act(() => { enter("B"); enter("A"); }); await act(confirm);
    expect(fixture.sdk.createForwardMessage).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("keeps source privacy bound to the dragged original CID", async () => {
    mount(DomOnly); const source = { ...message("dragged-original"), contentType: 105, fileElem: { fileName: "file.bin", fileSize: 9 } };
    act(() => fixture.drop.onDom({ message: source, conversationID: "source-cid", sourceOwner: messageOwner() }));
    messagePrivacyState({ ...source, attachedInfoElem: { isPrivateChat: true } }, "source-cid"); await act(confirm);
    expect(fixture.sdk.createForwardMessage).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
});
