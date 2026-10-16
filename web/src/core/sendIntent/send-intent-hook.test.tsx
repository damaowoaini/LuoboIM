// @vitest-environment jsdom
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { conversation, deferred, enter, fixture, flush, group, member, message, messages, reset, store } from "./harness";
import { captureSendOrigin, knownGroupSendAllowed } from "@/core/messageSendIntent";
import { captureMessageRetention, messageHistoryFence, messageOwner, messagePrivacyState } from "@/core/messagePrivacyRuntime";
import { useSendMessage } from "@/pages/chat/queryChat/ChatFooter/useSendMessage";

let host: HTMLDivElement, root: Root, hook: ReturnType<typeof useSendMessage>;
function Probe() { hook = useSendMessage(); return null; }
beforeEach(() => { reset(); host = document.createElement("div"); document.body.append(host); root = createRoot(host); act(() => root.render(<Probe />)); });
afterEach(() => { act(() => root.unmount()); host.remove(); });
const current = () => hook.beginCurrentSend()!;
const selected = (id = "target", kind: "user" | "group" = "user") => hook.beginSelectedSend({ kind, id })!;
const afterOnly = (globalThis as typeof globalThis & { process?: { env?: { LUOBO_SEND_INTENT_STAGE?: string } } }).process?.env?.LUOBO_SEND_INTENT_STAGE === "before" ? describe.skip : describe;

afterOnly("real sender / real Zustand scope / real owner and history wrappers", () => {
  it("allows same-CID unread clones without conversation-object identity", async () => {
    const intent = current(); act(() => store.getState().updateCurrentConversation({ ...conversation(), unreadCount: 8 }));
    expect((await hook.sendMessage({ message: message(), intent })).state).toBe("sent");
    expect(fixture.sdk.sendMessage).toHaveBeenCalledWith(expect.objectContaining({ recvID: "A", groupID: "", conversationID: "cid-user-A" }));
  });
  for (const roundTrip of [false, true]) it(`does not revive old current-chat intent after A→B${roundTrip ? "→A" : ""}`, async () => {
    const intent = current(); act(() => { enter("B"); if (roundTrip) enter("A"); });
    expect((await hook.sendMessage({ message: message(), intent })).state).toBe("stale"); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("keeps selected non-active user/group namespaces distinct and never projects into A", async () => {
    for (const kind of ["user", "group"] as const) expect((await hook.sendMessage({ message: message(kind), intent: selected("same-id", kind) })).state).toBe("sent");
    expect(fixture.sdk.sendMessage.mock.calls.map(([p]) => [p.recvID, p.groupID, p.conversationID])).toEqual([
      ["same-id", "", "cid-user-same-id"], ["", "same-id", "cid-group-same-id"]]);
    expect(messages.getState().pushNewMessage).not.toHaveBeenCalled(); expect(messages.getState().updateOneMessage).not.toHaveBeenCalled();
    expect(messages.getState().tryAddPreviewImg).not.toHaveBeenCalled();
  });
  it("returns observed ACK as sent after navigation while suppressing all late view projection", async () => {
    const reply = deferred<any>(); fixture.sdk.sendMessage.mockReturnValueOnce(reply.promise);
    const pending = hook.sendMessage({ message: message(), intent: current() }); await flush(); expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
    act(() => enter("B")); reply.resolve({ data: { ...message(), status: 2 } });
    expect((await pending).state).toBe("sent"); expect(messages.getState().updateOneMessage).not.toHaveBeenCalled(); expect(messages.getState().tryAddPreviewImg).not.toHaveBeenCalled();
  });
  it("settles an observed same-view ACK even when mute changes after native dispatch", async () => {
    act(() => enter("G", "group")); await act(flush);
    const reply = deferred<any>(); fixture.sdk.sendMessage.mockReturnValueOnce(reply.promise);
    const pending = hook.sendMessage({ message: message(), intent: current() }); await flush();
    expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
    act(() => store.getState().tryUpdateCurrentMemberInGroup(member("G", "actor", 20, Date.now() + 10000)));
    reply.resolve({ data: { ...message(), status: 2 } });
    expect((await pending).state).toBe("sent");
    expect(messages.getState().updateOneMessage).toHaveBeenCalledWith(expect.objectContaining({ clientMsgID: message().clientMsgID, status: 2 }));
  });
  it("reports a wrapper-discarded owner-stale native reply as unconfirmed, never as zero-dispatch stale", async () => {
    const reply = deferred<any>(); fixture.sdk.sendMessage.mockReturnValueOnce(reply.promise);
    const pending = hook.sendMessage({ message: message(), intent: current() }); await flush();
    fixture.session.generation++; fixture.session.current = { ...fixture.owner(), tenant: { serverID: "B" } };
    reply.resolve({ data: { ...message(), status: 2 } }); expect((await pending).state).toBe("unconfirmed");
    expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce(); expect(messages.getState().updateOneMessage).not.toHaveBeenCalled();
  });
  it("rejects malformed ACK without replay", async () => {
    fixture.sdk.sendMessage.mockResolvedValueOnce({ data: { ...message("other"), status: 2 } });
    expect((await hook.sendMessage({ message: message(), intent: current() })).state).toBe("unconfirmed"); expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
  });
  it("binds resolved target CID and rejects a newly selected target already being cleared", async () => {
    const finish = messageHistoryFence.beginClear(messageOwner(), "cid-user-target");
    try {
      // This is a NEW operation while target clear is active, not an old
      // render-origin correctly invalidated by beginClear's global revision.
      act(() => root.render(<Probe />));
      const intent = selected(); expect(intent).toBeDefined();
      expect((await hook.sendMessage({ message: message(), intent })).state).toBe("stale");
      expect(fixture.sdk.getOneConversation).toHaveBeenCalledWith({ sourceID: "target", sessionType: 1 });
    }
    finally { finish(); }
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("does not borrow a new history revision after asynchronous recipient resolution", async () => {
    const reply = deferred<any>(); fixture.sdk.getOneConversation.mockReturnValueOnce(reply.promise);
    const pending = hook.sendMessage({ message: message(), intent: selected() });
    const finish = messageHistoryFence.beginClear(messageOwner(), "cid-user-target"); finish(); reply.resolve({ data: conversation("target") });
    expect((await pending).state).toBe("stale"); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("rejects wrong resolved recipient identity instead of using active target", async () => {
    fixture.sdk.getOneConversation.mockResolvedValueOnce({ data: conversation("replacement") });
    expect((await hook.sendMessage({ message: message(), intent: selected() })).state).toBe("stale"); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("retains original-source privacy scope separately from explicit destination", async () => {
    const source = message("source"); const retention = captureMessageRetention(source, "cid-user-A"); const intent = selected();
    messagePrivacyState({ ...source, attachedInfoElem: { isPrivateChat: true } }, "cid-user-A");
    expect((await hook.sendMessage({ message: message(), intent, sourceRetention: retention })).state).toBe("stale"); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("checks final SDK-cache member identity and mutation during group reads", async () => {
    const reply = deferred<any>(); fixture.sdk.getSpecifiedGroupMembersInfo.mockReturnValueOnce(reply.promise);
    const pending = hook.sendMessage({ message: message(), intent: selected("G", "group") }); await flush();
    reply.resolve({ data: [member("G", "wrong-actor")] }); expect((await pending).state).toBe("stale"); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("does not downgrade current mute event to a stale SDK-cache allowance", async () => {
    act(() => enter("G", "group")); await act(flush); const intent = current();
    const reply = deferred<any>(); fixture.sdk.getSpecifiedGroupMembersInfo.mockReturnValueOnce(reply.promise);
    const pending = hook.sendMessage({ message: message(), intent }); await flush();
    act(() => store.getState().tryUpdateCurrentMemberInGroup(member("G", "actor", 20, Date.now() + 10000)));
    reply.resolve({ data: [member()] }); expect((await pending).state).toBe("stale"); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("allows ordinary new private root messages and does not mistake encryption for retention prohibition", async () => {
    expect((await hook.sendMessage({ message: { ...message(), attachedInfoElem: { isPrivateChat: true } }, intent: current() })).state).toBe("sent");
    expect((await hook.sendMessage({ message: { ...message("ordinary-encrypted"), keyVersion: 2 }, intent: current() })).state).toBe("sent");
  });
  it("does not authenticate a hand-made intent or owner mutation", async () => {
    const origin = captureSendOrigin(store.getState().captureConversationSendScope()); const intent = origin.currentIntent()!;
    expect((await hook.sendMessage({ message: message(), intent: { ...intent } })).state).toBe("stale");
    fixture.session.current!.issuer = "https://different.invalid";
    expect((await hook.sendMessage({ message: message(), intent })).state).toBe("stale"); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("matches local role/status/millisecond contract without claiming authority", () => {
    const now = Date.now(); expect(knownGroupSendAllowed(group(), member("G", "actor", 20, now), "G", "actor", now)).toBe(false);
    expect(knownGroupSendAllowed(group(), member("G", "actor", 20, now - 1), "G", "actor", now)).toBe(true);
    expect(knownGroupSendAllowed(group("G", 3), member("G", "actor", 100, now + 1000), "G", "actor", now)).toBe(true);
    expect(knownGroupSendAllowed(group("G", 3), member("G", "actor", 20), "G", "actor", now)).toBe(false);
  });
});
