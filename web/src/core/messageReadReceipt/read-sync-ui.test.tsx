// @vitest-environment jsdom
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it } from "vitest";
import { deferred, enter, fixture, flush, found, messages, now, reset, wire } from "./harness";
import useConversationState from "@/pages/chat/queryChat/useConversationState";
import ReadSyncStatus, { MessageReadBoundary } from "@/pages/chat/queryChat/ReadSyncStatus";
import { messageOwner, messagePrivacyLedger, messagePrivacyState } from "@/core/messagePrivacyRuntime";
import { vi } from "vitest";

let root: Root | undefined, host: HTMLDivElement;
let state: ReturnType<typeof useConversationState>;
function Screen() {
  state = useConversationState();
  const rows = messages(value => value.historyMessageList);
  return <>
    <ReadSyncStatus result={state.readSync} pending={state.readTimePending} canRecover={state.canRecoverReadTime}
      busy={state.readRetryBusy} recover={state.recoverReadTime} />
    {rows.map(message => <MessageReadBoundary key={message.clientMsgID} message={message} conversationID={state.currentConversation?.conversationID}>
      <p data-body={message.clientMsgID}>{message.textElem?.content}</p>
    </MessageReadBoundary>)}
  </>;
}
function mount() { host = document.createElement("div"); document.body.append(host); root = createRoot(host); act(() => root!.render(<Screen />)); }
function stop() { if (root) act(() => root!.unmount()); root = undefined; host?.remove(); }
const button = () => host.querySelector<HTMLButtonElement>('button[aria-label="重新获取读取时间"]')!;
const body = (id = "incoming") => host.querySelector(`[data-body="${id}"]`);
const row = (id = "incoming") => messages.getState().historyMessageList.find(message => message.clientMsgID === id) as any;
const actualTime = (message: any, time: number) => ({ ...message, attachedInfoElem: { ...message.attachedInfoElem, hasReadTime: time } });
beforeEach(() => { reset(); vi.useFakeTimers(); vi.setSystemTime(now); });
afterEach(() => { stop(); vi.useRealTimers(); });

it("hides acknowledged private plaintext on lookup failure without deleting it or retrying the read ACK", async () => {
  const incoming = wire("incoming", true);
  const ordinary = { ...wire("ordinary", true), isRead: true, attachedInfoElem: { isPrivateChat: false } };
  messages.setState({ historyMessageList: [incoming, ordinary] }); enter("peer", 1);
  fixture.sdk.findMessageList.mockRejectedValueOnce(new Error("synthetic lookup unavailable"));
  mount(); await act(flush);
  expect(state.readSync?.state).toBe("unconfirmed");
  expect(host.querySelector('[aria-label="消息读取状态"]')?.textContent).toContain("读取时间未能确认");
  expect(body()).toBeNull(); expect(body("ordinary")).not.toBeNull();
  expect(row().hasReadTime).toBeUndefined(); expect(row().textElem.content).toBe(incoming.textElem.content);
  expect(messagePrivacyLedger.observe(messageOwner(), "si-peer", row()).expired).toBe(false);
  expect(button().disabled).toBe(false);
  act(() => { vi.advanceTimersByTime(5000); }); await act(flush);
  expect(fixture.sdk.findMessageList).toHaveBeenCalledTimes(1);
  expect(fixture.sdk.markConversationMessageAsRead).toHaveBeenCalledTimes(1);
});

it("recovers only genuine SDK deadlines through explicit lookup and keeps an already expired private body hidden", async () => {
  const incoming = wire("incoming", true), expired = wire("expired", true);
  messages.setState({ historyMessageList: [incoming, expired] }); enter("peer", 1);
  mount(); await act(flush); // The genuine ACK's exact lookup has no rows/timestamp.
  expect(state.readSync?.state).toBe("acknowledged"); expect(body()).toBeNull();
  fixture.sdk.findMessageList.mockResolvedValueOnce({ data: { totalCount: 2, findResultItems: [{ conversationID: "si-peer", conversationType: 1,
    messageCount: 2, messageList: [actualTime(incoming, now - 2000), actualTime(expired, now - 31000)] }] } });
  await act(async () => { button().click(); await flush(); });
  expect(fixture.sdk.markConversationMessageAsRead).toHaveBeenCalledTimes(1);
  expect(fixture.sdk.findMessageList).toHaveBeenLastCalledWith([{ conversationID: "si-peer", clientMsgIDList: ["incoming", "expired"] }]);
  expect(state.readSync?.state).toBe("applied"); expect(body()).not.toBeNull(); expect(body("expired")).toBeNull();
  expect(messagePrivacyState(row(), "si-peer").remainingSeconds).toBe(28);
  expect(messagePrivacyState(row("expired"), "si-peer").expired).toBe(true);
  act(() => { vi.advanceTimersByTime(29000); }); await act(flush);
  expect(body()).toBeNull(); expect(row().hasReadTime).toBe(now - 2000);
});

it("admits one recovery lookup per original identity and refuses retained navigation or owner callbacks", async () => {
  const incoming = wire("incoming", true);
  messages.setState({ historyMessageList: [incoming] }); enter("peer", 1); mount(); await act(flush);
  const pending = deferred<any>(); fixture.sdk.findMessageList.mockReturnValueOnce(pending.promise);
  const saved = state.recoverReadTime;
  act(() => { void saved(); void saved(); }); await act(flush);
  expect(fixture.sdk.findMessageList).toHaveBeenCalledTimes(2); expect(button().disabled).toBe(true);
  act(() => { enter("other"); enter("peer", 0); messages.setState({ historyMessageList: [{ ...incoming, isRead: true }] }); });
  await act(async () => { pending.resolve(found(actualTime(incoming, now))); await flush(); });
  expect(row().hasReadTime).toBeUndefined(); expect(body()).toBeNull();
  await act(async () => { await saved(); await flush(); });
  expect(fixture.sdk.findMessageList).toHaveBeenCalledTimes(2);
  stop(); mount(); await act(flush);
  const ownerPending = deferred<any>(); fixture.sdk.findMessageList.mockReturnValueOnce(ownerPending.promise);
  const priorOwner = state.recoverReadTime; act(() => { void priorOwner(); }); await act(flush);
  fixture.session.current = fixture.owner(); fixture.session.generation++;
  act(() => { messages.setState({ historyMessageList: [{ ...incoming, isRead: true }] }); });
  await act(async () => { ownerPending.resolve(found(actualTime(incoming, now))); await flush(); await priorOwner(); });
  expect(row().hasReadTime).toBeUndefined(); expect(body()).toBeNull();
  expect(fixture.sdk.findMessageList).toHaveBeenCalledTimes(3);
  expect(fixture.sdk.markConversationMessageAsRead).toHaveBeenCalledTimes(1);
});

it("keeps the private view hold across reentry and never promotes a bare isRead flag to an ACK or expiry", async () => {
  const incoming = wire("incoming", true);
  messages.setState({ historyMessageList: [incoming] }); enter("peer", 1); mount(); await act(flush);
  expect(body()).toBeNull(); stop();
  enter("other"); enter("peer", 0);
  const unknown = { ...wire("no-observed-ack", true), isRead: true };
  // Deliberately refill without isRead on the known-ACK record. The session
  // identity-only barrier, not transient React state or this flag, must win.
  messages.setState({ historyMessageList: [incoming, unknown] }); mount(); await act(flush);
  expect(body()).toBeNull(); expect(body("no-observed-ack")).toBeNull(); expect(button().disabled).toBe(false);
  fixture.sdk.findMessageList.mockResolvedValueOnce(found(actualTime(incoming, now - 1000)));
  await act(async () => { button().click(); await flush(); });
  expect(fixture.sdk.findMessageList).toHaveBeenLastCalledWith([{ conversationID: "si-peer", clientMsgIDList: ["incoming"] }]);
  expect(body()).not.toBeNull(); expect(body("no-observed-ack")).toBeNull(); expect(button().disabled).toBe(true);
  expect(row("no-observed-ack").hasReadTime).toBeUndefined();
  expect(fixture.sdk.markConversationMessageAsRead).toHaveBeenCalledTimes(1);
});
