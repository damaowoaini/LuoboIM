// @vitest-environment jsdom
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { deferred, emit, enter, fixture, flush, found, messages, now, reset, store, wire } from "./harness";
import { useMessageReceipt } from "@/pages/chat/queryChat/useMessageReceipt";
import useConversationState from "@/pages/chat/queryChat/useConversationState";
import { captureMessageReadScope, markReadAndHydrate } from "@/core/messageReadReceipt";
import { messageHistoryFence, messagePrivacyState } from "@/core/messagePrivacyRuntime";
let root: Root | undefined, host: HTMLDivElement;
let readState: ReturnType<typeof useConversationState>;
function Receipts() { useMessageReceipt(); return null; }
function OwnRead() { readState = useConversationState(); return <output>{readState.readSync?.state}</output>; }
function mount(Component: React.ComponentType = Receipts) { host = document.createElement("div"); document.body.append(host); root = createRoot(host); act(() => root!.render(<Component />)); }
function stop() { if (root) act(() => root!.unmount()); root = undefined; host?.remove(); }
const row = (id = "outgoing") => messages.getState().historyMessageList.find(message => message.clientMsgID === id) as any;
const receipt = (overrides = {}) => ({ userID: "peer", groupID: "", sessionType: 1, msgIDList: ["outgoing"], readTime: now, ...overrides });
beforeEach(() => { reset(); vi.useFakeTimers(); vi.setSystemTime(now); });
afterEach(() => { stop(); vi.useRealTimers(); });

it("projects the exact peer C2C readTime and never extends an already known private deadline", () => {
  messages.setState({ historyMessageList: [wire()] }); mount();
  act(() => emit("single-read", [receipt()]));
  expect(row().hasReadTime).toBe(now); expect(row().isRead).toBe(true);
  expect(messagePrivacyState(row(), "si-peer").expiresAt).toBe(now + 30000);
  act(() => emit("single-read", [receipt({ readTime: now + 10000 })]));
  expect(row().hasReadTime).toBe(now);
  for (const bad of [{ userID: "foreign" }, { sessionType: 3 }, { groupID: "foreign-group" }, { msgIDList: ["other"] }]) {
    messages.setState({ historyMessageList: [wire()] }); act(() => emit("single-read", [receipt(bad)]));
    expect(row().isRead).toBe(false); expect(row().hasReadTime).toBeUndefined();
  }
  messages.setState({ historyMessageList: [wire("untimed")] });
  act(() => emit("single-read", [receipt({ msgIDList: ["untimed"], readTime: "1792195200000" })]));
  expect(row("untimed").isRead).toBe(true); expect(row("untimed").hasReadTime).toBeUndefined();
  expect(messagePrivacyState(row("untimed"), "si-peer").expiresAt).toBeUndefined();
});
it("rejects retained real-store A-B-A, owner-generation, clear and unmounted C2C callbacks", () => {
  messages.setState({ historyMessageList: [wire()] }); mount();
  const old = [...fixture.listeners.get("single-read")!][0];
  act(() => { enter("B"); enter(); messages.setState({ historyMessageList: [wire()] }); });
  act(() => old({ data: [receipt()] })); expect(row().hasReadTime).toBeUndefined();
  act(() => emit("single-read", [receipt()])); expect(row().hasReadTime).toBe(now);
  const priorOwner = [...fixture.listeners.get("single-read")!][0];
  fixture.session.current = fixture.owner(); fixture.session.generation++;
  messages.setState({ historyMessageList: [wire()] }); act(() => priorOwner({ data: [receipt()] }));
  expect(row().hasReadTime).toBeUndefined();
  act(() => { enter(); });
  const beforeClear = [...fixture.listeners.get("single-read")!][0];
  act(() => {
    messageHistoryFence.invalidate();
    expect(messages.getState().historyMessageList).toEqual([]);
    // The real fence clears the store. A synthetic post-clear refill lets the
    // retained pre-clear callback prove it cannot mutate this replacement row.
    messages.setState({ historyMessageList: [wire()] });
    beforeClear({ data: [receipt()] });
  }); expect(row().hasReadTime).toBeUndefined();
  stop(); act(() => beforeClear({ data: [receipt()] })); expect(row().hasReadTime).toBeUndefined();
});
it("preserves the real current-group receipt projection without granting a C2C peer", () => {
  act(() => store.getState().updateCurrentConversation({ conversationID: "sg-group", conversationType: 3, groupID: "group", userID: "", unreadCount: 0, groupAtType: 0 } as any, true));
  messages.setState({ historyMessageList: [{ ...wire("group-msg"), sessionType: 3, groupID: "group" }] }); mount();
  act(() => emit("group-read", { conversationID: "sg-group", groupMessageReadInfo: [{ clientMsgID: "group-msg", readMembers: [{ userID: "actor" }], hasReadCount: 2, unreadCount: 1 }] }));
  expect(row("group-msg").isRead).toBe(true); expect(row("group-msg").attachedInfoElem.groupHasReadInfo).toEqual({ hasReadCount: 2, unreadCount: 1 });
  expect(row("group-msg").hasReadTime).toBeUndefined();
});
it("waits for the real mark-read ACK then uses only the installed exact lookup and actual SDK time", async () => {
  const ack = deferred<any>(); fixture.sdk.markConversationMessageAsRead.mockReturnValueOnce(ack.promise);
  const incoming = wire("incoming", true); messages.setState({ historyMessageList: [incoming] }); enter("peer", 1);
  fixture.sdk.findMessageList.mockResolvedValue(found({ ...incoming, isRead: true, attachedInfoElem: undefined, attachedInfo: JSON.stringify({ isPrivateChat: true, burnDuration: 30, hasReadTime: now - 2000 }) }));
  mount(OwnRead); expect(fixture.sdk.findMessageList).not.toHaveBeenCalled();
  await act(async () => { ack.resolve({ data: null }); await flush(); });
  expect(fixture.sdk.markConversationMessageAsRead).toHaveBeenCalledTimes(1);
  expect(fixture.sdk.findMessageList).toHaveBeenCalledExactlyOnceWith([{ conversationID: "si-peer", clientMsgIDList: ["incoming"] }]);
  expect(row("incoming").hasReadTime).toBe(now - 2000); expect(store.getState().currentConversation?.unreadCount).toBe(0);
  expect(readState.readSync).toEqual({ state: "applied", acknowledged: true, projected: 1 });
});
it("does not borrow a new navigation or mounted lifetime after mark-read or exact-lookup awaits", async () => {
  const ack = deferred<any>(); fixture.sdk.markConversationMessageAsRead.mockReturnValueOnce(ack.promise);
  messages.setState({ historyMessageList: [wire("incoming", true)] }); enter("peer", 1); mount(OwnRead);
  act(() => { enter("B"); enter("peer", 0); messages.setState({ historyMessageList: [wire("incoming", true)] }); });
  await act(async () => { ack.resolve({ data: null }); await flush(); });
  expect(fixture.sdk.findMessageList).not.toHaveBeenCalled(); expect(row("incoming").hasReadTime).toBeUndefined();
  stop(); reset();
  const lookup = deferred<any>(); fixture.sdk.findMessageList.mockReturnValueOnce(lookup.promise);
  const incoming = wire("incoming", true); messages.setState({ historyMessageList: [incoming] }); enter("peer", 1); mount(OwnRead);
  await act(flush); expect(fixture.sdk.findMessageList).toHaveBeenCalledTimes(1); stop();
  await act(async () => { lookup.resolve(found({ ...incoming, attachedInfoElem: { ...incoming.attachedInfoElem, hasReadTime: now } })); await flush(); });
  expect(row("incoming").hasReadTime).toBeUndefined();
});
it("rejects foreign exact-lookup buckets or IDs and leaves malformed or absent SDK read time unconfirmed", async () => {
  const incoming = wire("incoming", true); messages.setState({ historyMessageList: [incoming] });
  fixture.sdk.findMessageList.mockResolvedValue({ data: { totalCount: 1, findResultItems: [{ conversationID: "foreign", conversationType: 1, messageCount: 1, messageList: [{ ...incoming, clientMsgID: "foreign-id" }] }] } });
  const ack = vi.fn(); const first = await markReadAndHydrate(captureMessageReadScope()!, fixture.sdk as any, ack);
  expect(first.state).toBe("unconfirmed"); expect(ack).toHaveBeenCalledTimes(1); expect(row("incoming").hasReadTime).toBeUndefined();
  messages.setState({ historyMessageList: [incoming] });
  fixture.sdk.findMessageList.mockResolvedValue(found({ ...incoming, attachedInfoElem: { ...incoming.attachedInfoElem, hasReadTime: "not-a-time" } }));
  const second = await markReadAndHydrate(captureMessageReadScope()!, fixture.sdk as any, ack);
  expect(second).toEqual({ state: "acknowledged", acknowledged: true, projected: 0 });
  expect(row("incoming").hasReadTime).toBeUndefined(); expect(messagePrivacyState(row("incoming"), "si-peer").expiresAt).toBeUndefined();
});
it("exposes failed mark-read versus known ACK with lookup failure without retrying the same batch", async () => {
  fixture.sdk.markConversationMessageAsRead.mockRejectedValueOnce(new Error("synthetic mark-read failure"));
  messages.setState({ historyMessageList: [wire("incoming", true)] }); enter("peer", 1); mount(OwnRead); await act(flush);
  expect(readState.readSync?.state).toBe("failed"); expect(fixture.sdk.findMessageList).not.toHaveBeenCalled();
  expect(store.getState().currentConversation?.unreadCount).toBe(1);
  act(() => { vi.advanceTimersByTime(2000); store.getState().updateCurrentConversation({ ...store.getState().currentConversation! }); }); await act(flush);
  expect(fixture.sdk.markConversationMessageAsRead).toHaveBeenCalledTimes(1);
  stop(); reset(); fixture.sdk.findMessageList.mockRejectedValueOnce(new Error("synthetic lookup failure"));
  messages.setState({ historyMessageList: [wire("incoming", true)] }); enter("peer", 1); mount(OwnRead); await act(flush);
  expect(readState.readSync?.state).toBe("unconfirmed"); expect(store.getState().currentConversation?.unreadCount).toBe(0);
  expect(row("incoming").isRead).toBe(true); expect(row("incoming").hasReadTime).toBeUndefined();
});
