// @vitest-environment jsdom
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { deferred, enter, fixture, flush, found, messages, now, reset, store, wire } from "./harness";
import useConversationState from "@/pages/chat/queryChat/useConversationState";

let root: Root | undefined, host: HTMLDivElement;
let state: ReturnType<typeof useConversationState>;
function App() { state = useConversationState(); return <output>{state.readSync?.state}</output>; }
function mount() { host = document.createElement("div"); document.body.append(host); root = createRoot(host); act(() => root!.render(<App />)); }
const tick = async () => { await act(async () => { vi.advanceTimersByTime(2100); await flush(); }); };
const row = (id: string) => messages.getState().historyMessageList.find(message => message.clientMsgID === id) as any;
const timed = (message: any) => ({ ...message, attachedInfoElem: { ...message.attachedInfoElem, hasReadTime: now } });
beforeEach(() => { reset(); vi.useFakeTimers(); vi.setSystemTime(now); });
afterEach(() => { if (root) act(() => root!.unmount()); root = undefined; host?.remove(); vi.useRealTimers(); });

it("marks a new exact incoming C2C row in an already open zero-unread conversation through the real hook", async () => {
  const old = { ...wire("already-read", true), seq: 1, isRead: true, attachedInfoElem: { isPrivateChat: false } };
  messages.setState({ historyMessageList: [old] }); enter("peer", 0); mount(); await tick();
  expect(fixture.sdk.markConversationMessageAsRead).not.toHaveBeenCalled();
  const incoming = { ...wire("new-live", true), seq: 3, isAppend: true };
  fixture.sdk.findMessageList.mockResolvedValueOnce(found(timed(incoming)));
  act(() => messages.getState().pushNewMessage(incoming)); await tick();
  expect(store.getState().currentConversation?.unreadCount).toBe(0);
  expect(fixture.sdk.markConversationMessageAsRead).toHaveBeenCalledExactlyOnceWith("si-peer");
  expect(fixture.sdk.findMessageList).toHaveBeenCalledExactlyOnceWith([{ conversationID: "si-peer", clientMsgIDList: ["new-live"] }]);
  expect(row("new-live").hasReadTime).toBe(now); expect(state.readSync?.state).toBe("applied");
});

it("waits for loaded incoming IDs before the initial read ACK can clear an unread single chat", async () => {
  const ack = deferred<any>(); fixture.sdk.markConversationMessageAsRead.mockReturnValueOnce(ack.promise);
  enter("peer", 1); mount(); await tick();
  expect(fixture.sdk.markConversationMessageAsRead).not.toHaveBeenCalled();
  expect(store.getState().currentConversation?.unreadCount).toBe(1);
  const incoming = wire("loaded-private", true);
  fixture.sdk.findMessageList.mockResolvedValueOnce(found(timed(incoming)));
  act(() => messages.setState({ historyMessageList: [incoming] })); await tick();
  expect(fixture.sdk.markConversationMessageAsRead).toHaveBeenCalledTimes(1);
  expect(fixture.sdk.findMessageList).not.toHaveBeenCalled(); expect(row("loaded-private").isRead).toBe(false);
  await act(async () => { ack.resolve({ data: null }); await flush(); });
  expect(fixture.sdk.findMessageList).toHaveBeenCalledExactlyOnceWith([{ conversationID: "si-peer", clientMsgIDList: ["loaded-private"] }]);
  expect(row("loaded-private").hasReadTime).toBe(now); expect(store.getState().currentConversation?.unreadCount).toBe(0);
});

it("does not replay a failed incoming batch for badge changes row reorder or refill and admits a genuinely new ID", async () => {
  const failed = wire("failed-read", true);
  messages.setState({ historyMessageList: [failed] }); enter("peer", 1);
  fixture.sdk.markConversationMessageAsRead.mockRejectedValueOnce(new Error("synthetic unknown read ACK"));
  mount(); await tick(); expect(state.readSync?.state).toBe("failed");
  expect(fixture.sdk.markConversationMessageAsRead).toHaveBeenCalledTimes(1);
  const old = { ...wire("read-history-page", true), seq: 1, isRead: true, attachedInfoElem: { isPrivateChat: false } };
  act(() => {
    messages.setState({ historyMessageList: [old, { ...failed }] });
    store.getState().updateCurrentConversation({ ...store.getState().currentConversation!, unreadCount: 2, latestMsgSendTime: 9 });
  }); await tick();
  act(() => messages.setState({ historyMessageList: [old] })); await tick();
  act(() => messages.setState({ historyMessageList: [{ ...failed }, old] })); await tick();
  expect(fixture.sdk.markConversationMessageAsRead).toHaveBeenCalledTimes(1);
  expect(fixture.sdk.findMessageList).not.toHaveBeenCalled(); expect(row("failed-read").hasReadTime).toBeUndefined();
  const fresh = { ...wire("really-new", true), seq: 3 };
  fixture.sdk.findMessageList.mockResolvedValueOnce({ data: { totalCount: 2, findResultItems: [{ conversationID: "si-peer", conversationType: 1,
    messageCount: 2, messageList: [timed(failed), timed(fresh)] }] } });
  act(() => messages.getState().pushNewMessage(fresh)); await tick();
  expect(fixture.sdk.markConversationMessageAsRead).toHaveBeenCalledTimes(2);
  expect(new Set(fixture.sdk.findMessageList.mock.calls[0][0][0].clientMsgIDList)).toEqual(new Set(["failed-read", "really-new"]));
  expect(row("really-new").hasReadTime).toBe(now);
});
