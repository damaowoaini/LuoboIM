// @vitest-environment jsdom
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { deferred, emit, enter, fixture, flush, found, messages, now, reset, store, wire } from "./harness";
import { useMessageReceipt } from "@/pages/chat/queryChat/useMessageReceipt";
import useConversationState from "@/pages/chat/queryChat/useConversationState";
import { OWN_READ_TIME_EVENT, pendingReadTimes, captureMessageReadScope } from "@/core/messageReadReceipt";
import { messageHistoryFence, messagePrivacyState } from "@/core/messagePrivacyRuntime";
let root: Root | undefined, host: HTMLDivElement;
function Reader() { useMessageReceipt(); useConversationState(); return null; }
function mount() { host = document.createElement("div"); document.body.append(host); root = createRoot(host); act(() => root!.render(<Reader />)); }
function stop() { if (root) act(() => root!.unmount()); root = undefined; host?.remove(); }
const row = (id = "incoming") => messages.getState().historyMessageList.find(message => message.clientMsgID === id) as any;
const event = (extra = {}) => ({ schemaVersion: 1, ownerID: "actor", peerID: "peer", conversationID: "si-peer", sessionType: 1,
  receiptID: "synthetic-server-receipt", committedRows: [{ clientMsgID: "incoming", seq: 2, sendID: "peer", recvID: "actor", readTime: now - 2000 }], ...extra });
beforeEach(() => { reset(); vi.useFakeTimers(); vi.setSystemTime(now); });
afterEach(() => { stop(); vi.useRealTimers(); });

it("uses the distinct durable own event in either ACK order without a second mark or lookup and keeps the earliest time", async () => {
  for (const order of ["ACK-before-echo", "echo-before-ACK"]) {
    stop(); reset();
    const ack = deferred<any>(); fixture.sdk.markConversationMessageAsRead.mockReturnValueOnce(ack.promise);
    const incoming = wire("incoming", true); messages.setState({ historyMessageList: [incoming] }); enter("peer", 1);
    fixture.sdk.findMessageList.mockResolvedValue(found({ ...incoming, isRead: true })); mount();
    if (order === "ACK-before-echo") {
      await act(async () => { ack.resolve({ data: null }); await flush(); });
      expect(messagePrivacyState(row(), "si-peer").readTimePending).toBe(true);
      act(() => emit(OWN_READ_TIME_EVENT, event()));
    } else {
      act(() => emit(OWN_READ_TIME_EVENT, event()));
      await act(async () => { ack.resolve({ data: null }); await flush(); });
    }
    expect(row().isRead).toBe(true); expect(row().hasReadTime).toBe(now - 2000);
    expect(messagePrivacyState(row(), "si-peer").expiresAt).toBe(now + 28000);
    expect(pendingReadTimes(captureMessageReadScope()).count).toBe(0);
    for (const time of [now + 10000, now - 5000, now + 20000]) {
      act(() => emit(OWN_READ_TIME_EVENT, event({ committedRows: [{ ...event().committedRows[0], readTime: time }] })));
    }
    expect(row().hasReadTime).toBe(now - 5000); expect(messagePrivacyState(row(), "si-peer").expiresAt).toBe(now + 25000);
    expect(fixture.sdk.markConversationMessageAsRead).toHaveBeenCalledTimes(1);
    expect(fixture.sdk.findMessageList).toHaveBeenCalledTimes(1);
  }
});
it("rejects a malformed or foreign own batch before any projection and never promotes an unknown row", async () => {
  const incoming = wire("incoming", true), other = { ...wire("other", true), seq: 3 };
  messages.setState({ historyMessageList: [incoming, other] }); enter("peer", 0); mount(); await act(flush);
  const good = event(), first = good.committedRows[0];
  const invalid = [
    { schemaVersion: 2 }, { ownerID: "foreign" }, { peerID: "foreign" }, { conversationID: "foreign" }, { sessionType: 3 }, { receiptID: "" },
    { committedRows: [] }, { committedRows: [first, first] },
    { committedRows: [first, { ...first, clientMsgID: "other" }] },
    ...[0, 1.5, "1792195200000", Number.NaN, Number.POSITIVE_INFINITY, Math.floor(now / 1000)].map(readTime => ({ committedRows: [{ ...first, readTime }] })),
    ...[{ seq: 0 }, { seq: "2" }, { sendID: "actor", recvID: "peer" }, { recvID: "foreign" }].map(change => ({ committedRows: [{ ...first, ...change }] })),
    { committedRows: [first, { ...first, clientMsgID: "other", seq: 4 }] },
  ];
  for (const bad of invalid) {
    act(() => emit(OWN_READ_TIME_EVENT, event(bad)));
    expect(row().hasReadTime).toBeUndefined(); expect(row("other").hasReadTime).toBeUndefined();
  }
  act(() => emit(OWN_READ_TIME_EVENT, event({ committedRows: [{ ...first, clientMsgID: "not-loaded", seq: 99 }] })));
  expect(row().hasReadTime).toBeUndefined(); expect(fixture.sdk.markConversationMessageAsRead).toHaveBeenCalledTimes(1);
  expect(fixture.sdk.findMessageList).toHaveBeenCalledTimes(1);
});
it("does not let retained own callbacks borrow a new navigation owner history or mounted lifetime", async () => {
  messages.setState({ historyMessageList: [wire("incoming", true)] }); mount(); await act(flush);
  const old = [...fixture.listeners.get(OWN_READ_TIME_EVENT)!][0];
  act(() => { enter("B"); enter(); messages.setState({ historyMessageList: [wire("incoming", true)] }); });
  act(() => old({ data: event() })); expect(row().hasReadTime).toBeUndefined();
  act(() => emit(OWN_READ_TIME_EVENT, event())); expect(row().hasReadTime).toBe(now - 2000);
  const priorOwner = [...fixture.listeners.get(OWN_READ_TIME_EVENT)!][0];
  fixture.session.current = fixture.owner(); fixture.session.generation++;
  messages.setState({ historyMessageList: [wire("incoming", true)] }); act(() => priorOwner({ data: event() })); expect(row().hasReadTime).toBeUndefined();
  act(() => enter()); const beforeClear = [...fixture.listeners.get(OWN_READ_TIME_EVENT)!][0];
  act(() => { messageHistoryFence.invalidate(); messages.setState({ historyMessageList: [wire("incoming", true)] }); beforeClear({ data: event() }); });
  expect(row().hasReadTime).toBeUndefined(); stop(); act(() => beforeClear({ data: event() })); expect(row().hasReadTime).toBeUndefined();
});
