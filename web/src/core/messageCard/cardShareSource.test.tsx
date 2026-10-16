// @vitest-environment jsdom
// Actual CardActionRow -> ChooseModal -> batch -> useSendMessage -> bound
// session/history SDK wrapper. Synthetic transport only; no real messages.
import React, { act, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { deferred, enter, fixture, flush, reset, store, message } from "./shareHarness";
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (s: string) => s }) }));
vi.mock("antd", () => ({ Button: ({ children, onClick }: any) => <button onClick={onClick}>{children}</button>,
  Modal: ({ children, open }: any) => open ? <section>{children}</section> : null, Input: () => null, Upload: () => null,
  Tooltip: ({ title, children }: any) => <span data-tooltip={title}>{children}</span>,
  Space: ({ children }: any) => <span>{children}</span>, Popover: ({ children, onOpenChange, open, content }: any) => <span>{React.cloneElement(children, { onClick: () => onOpenChange?.(!open) })}{open && content}</span> }));
vi.mock("@ant-design/icons", () => ({ CloseOutlined: () => null }));
vi.mock("@/components/OIMAvatar", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/ChatFooter/SendActionBar/CallPopContent", () => ({ default: () => <span role="menu">call options</span> }));
vi.mock("@/pages/common/ChooseModal/ChooseBox", async () => {
  const { forwardRef, useImperativeHandle } = await import("react");
  return { default: forwardRef(function Box(_props, ref) { useImperativeHandle(ref, () => ({
    getCheckedList: () => fixture.selection, getAdditional: () => fixture.additional,
    resetState: () => {}, updatePrevCheckList: () => {},
  })); return <div>recipient selection adapter</div>; }) };
});
import Card from "@/pages/chat/queryChat/MessageItem/CardMessageRenderer";
import CardActionRow from "@/pages/common/UserCardModal/CardActionRow";
import ChooseModal from "@/pages/common/ChooseModal";
import emitter from "@/utils/events";
type RecordedEmit = (type: Parameters<typeof emitter.emit>[0], payload?: unknown) => void;
import { captureSendOrigin } from "@/core/messageSendIntent";
import { messageHistoryFence } from "@/core/messagePrivacyRuntime";
import type { OverlayVisibleHandle } from "@/hooks/useOverlayVisible";
let host: HTMLDivElement, root: Root;
beforeEach(() => { reset(); fixture.selection = [{ userID: "target-1" }]; host = document.createElement("div"); document.body.append(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });
const render = (node: React.ReactNode) => act(() => root.render(node));
const confirm = async () => { await act(async () => { [...host.querySelectorAll("button")].at(-1)!.click(); await flush(); }); };
function openChoice(sourceCurrent?: () => boolean, sourceCanShare?: () => boolean, sourceView?: React.ReactNode) {
  const ref = createRef<OverlayVisibleHandle>();
  const sendOrigin = captureSendOrigin(store.getState().captureConversationSendScope());
  render(<>{sourceView}<ChooseModal ref={ref} state={{ type: "SHARE_CARD", extraData: { userID: "703", nickname: "Verified" }, sendOrigin, sourceCurrent, sourceCanShare }} /></>);
  act(() => ref.current!.openOverlay());
  return ref;
}
describe("message-derived card sharing keeps its original source", () => {
  it("synchronous actual-store policy A-B-A permanently revokes an open picker, and a new explicit card operation works", async () => {
    act(() => enter("G", "group")); await act(flush);
    const card = <Card key="original-card" message={{ ...message("original-card"), contentType: 108, cardElem: { userID: "703", nickname: "联系人" } }} conversationID="cid-group-G" isSender={false} />;
    render(<>{card}</>); expect(host.querySelector('button[aria-label^="查看名片"]')).toBeNull();
    fixture.sdk.getSpecifiedGroupsInfo.mockResolvedValueOnce({ data: [{ groupID: "G", status: 0, lookMemberInfo: 0, applyMemberFriend: 0 }] });
    await act(async () => store.getState().getCurrentGroupInfoByReq("G"));
    expect(host.querySelector('button[aria-label^="查看名片"]')).not.toBeNull();
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label^="查看名片"]')!.click());
    const old = vi.mocked(emitter.emit as RecordedEmit).mock.calls.at(-1)![1] as any;
    expect(old.sourceCurrent()).toBe(true); expect(old.sourceCanShare()).toBe(true);
    let closed = 0; const stop = old.sourceSubscribe(() => { if (!old.sourceCurrent()) closed++; });
    const picker = openChoice(old.sourceCurrent, old.sourceCanShare, card);
    act(() => {
      store.getState().updateCurrentGroupInfo({ groupID: "G", status: 0, lookMemberInfo: 1, applyMemberFriend: 1 } as any);
      expect(old.sourceCurrent()).toBe(false); expect(closed).toBeGreaterThan(0);
      store.getState().updateCurrentGroupInfo({ groupID: "G", status: 0, lookMemberInfo: 0, applyMemberFriend: 0 } as any);
    });
    expect(old.sourceCurrent()).toBe(false); expect(old.sourceCanShare()).toBe(false);
    await confirm(); expect(fixture.sdk.createCardMessage).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
    act(() => picker.current!.closeOverlay()); act(() => host.querySelector<HTMLButtonElement>('button[aria-label^="查看名片"]')!.click());
    const fresh = vi.mocked(emitter.emit as RecordedEmit).mock.calls.at(-1)![1] as any;
    expect(fresh.sourceCurrent).not.toBe(old.sourceCurrent); expect(fresh.sourceCurrent()).toBe(true);
    openChoice(fresh.sourceCurrent, fresh.sourceCanShare, card); await confirm();
    expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce(); expect(old.sourceCurrent()).toBe(false); stop();
  });
  it("actual-store role, status and self-membership ABA revoke old callbacks but value-equal refresh preserves a new one", async () => {
    act(() => enter("G", "group")); await act(flush);
    const g = { groupID: "G", status: 0, lookMemberInfo: 0, applyMemberFriend: 0 } as any;
    act(() => store.getState().updateCurrentGroupInfo(g));
    for (const kind of ["role", "status", "membership"] as const) {
      const m = { ...store.getState().currentMemberInGroup!, roleLevel: 20, joinTime: 1 };
      act(() => store.getState().tryUpdateCurrentMemberInGroup(m));
      render(<Card key={kind} message={{ ...message(kind), contentType: 108, cardElem: { userID: "703", nickname: "联系人" } }} conversationID="cid-group-G" isSender={false} />);
      act(() => host.querySelector<HTMLButtonElement>('button[aria-label^="查看名片"]')!.click());
      const old = vi.mocked(emitter.emit as RecordedEmit).mock.calls.at(-1)![1] as any; expect(old.sourceCurrent()).toBe(true);
      act(() => {
        store.getState().updateCurrentGroupInfo({ ...g }); store.getState().tryUpdateCurrentMemberInGroup({ ...m });
      });
      expect(old.sourceCurrent()).toBe(true); // Mere SDK object/metadata refresh is not a revocation.
      act(() => {
        if (kind === "role") { store.getState().tryUpdateCurrentMemberInGroup({ ...m, roleLevel: 60 }); store.getState().tryUpdateCurrentMemberInGroup(m); }
        if (kind === "status") { store.getState().updateCurrentGroupInfo({ ...g, status: 3 }); store.getState().updateCurrentGroupInfo(g); }
        // Actual Zustand state models the production empty authoritative-member read then a resolved row.
        if (kind === "membership") { store.setState({ currentMemberInGroup: undefined }); store.getState().tryUpdateCurrentMemberInGroup(m); }
      });
      expect(old.sourceCurrent()).toBe(false); expect(old.sourceCanShare()).toBe(false);
    }
  });
  it("a denied retention capability stops card creation even while view remains current", async () => {
    openChoice(() => true, () => false); await confirm();
    expect(fixture.sdk.createCardMessage).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("retention is rechecked inside the real sender after its last target lookup", async () => {
    let retain = true; const reply = deferred<any>(); fixture.sdk.getOneConversation.mockReturnValueOnce(reply.promise);
    openChoice(() => true, () => retain); await confirm(); expect(fixture.sdk.createCardMessage).toHaveBeenCalledOnce();
    retain = false; reply.resolve({ data: { conversationID: "cid-user-target-1", conversationType: 1, userID: "target-1", groupID: "" } }); await act(flush);
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("share, unfriend and call have native keyboard buttons with 44px targets and original actions", async () => {
    let current = true;
    render(<CardActionRow cardInfo={{ userID: "703", nickname: "Verified" }} isFriend isCurrent={() => current} closeOverlay={() => {}} />);
    for (const label of ["share", "unfriend", "call"]) {
      const action = host.querySelector<HTMLButtonElement>(`button[aria-label="placeholder.${label}"]`)!;
      expect(action).not.toBeNull(); expect(action.type).toBe("button"); expect(action.style.minHeight).toBe("44px"); expect(action.style.minWidth).toBe("44px");
      expect(action.className).toContain("focus-visible"); expect(action.querySelector("img")?.getAttribute("width")).toBe("18");
    }
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="placeholder.call"]')!.click()); expect(host.querySelector('[role="menu"]')).not.toBeNull();
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="placeholder.unfriend"]')!.click()); expect(fixture.confirmations).toHaveLength(1);
    current = false; await act(async () => fixture.confirmations[0].onOk());
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("actual read-only navigation scope permits a notification card without a send capability", () => {
    store.getState().updateCurrentConversation({ conversationID: "notification", conversationType: 4, showName: "notice", unreadCount: 0 } as any, true);
    expect(store.getState().captureConversationSendScope()).toBeUndefined();
    const source = store.getState().captureConversationViewScope()!; expect(source.current()).toBe(true);
    render(<Card message={{ ...message(), contentType: 108, cardElem: { userID: "703", nickname: "联系人" } }} conversationID="notification" isSender={false} />);
    const button = host.querySelector<HTMLButtonElement>('button[aria-label^="查看名片"]'); expect(button).not.toBeNull();
    act(() => button!.click()); expect(vi.mocked(emitter.emit as RecordedEmit).mock.calls.at(-1)![1]).toEqual(expect.objectContaining({ userID: "703" }));
    enter("B"); store.getState().updateCurrentConversation({ conversationID: "notification", conversationType: 4 } as any, true);
    expect(source.current()).toBe(false);
  });
  it("CardActionRow passes the message source independently of the profile it closes", () => {
    let profileOpen = true, sourceLive = true;
    const sourceCurrent = () => sourceLive;
    render(<CardActionRow cardInfo={{ userID: "703", nickname: "Verified" }} isCurrent={() => profileOpen && sourceLive}
      sourceCurrent={sourceCurrent} closeOverlay={() => { profileOpen = false; }} />);
    act(() => host.querySelector<HTMLElement>('button[aria-label="placeholder.share"]')!.click());
    const share = vi.mocked(emitter.emit as RecordedEmit).mock.calls.find(([event]) => event === "OPEN_CHOOSE_MODAL")!;
    expect(profileOpen).toBe(false); expect(share[1]).toEqual({ type: "SHARE_CARD", extraData: { userID: "703", nickname: "Verified", faceURL: "", ex: "" }, sourceCurrent, sourceCanShare: undefined });
    expect((share[1] as any).sourceCurrent()).toBe(true); sourceLive = false; expect((share[1] as any).sourceCurrent()).toBe(false);
  });
  it("a current message source permits legitimate explicit cross-chat sharing", async () => {
    openChoice(() => true, () => true); await confirm();
    expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce(); expect(fixture.sdk.sendMessage.mock.calls[0][0].recvID).toBe("target-1");
  });
  it("does not dispatch when the source is invalidated during card construction", async () => {
    let sourceLive = true; const reply = deferred<any>(); fixture.sdk.createCardMessage.mockReturnValueOnce(reply.promise);
    openChoice(() => sourceLive); await confirm(); expect(fixture.sdk.createCardMessage).toHaveBeenCalledOnce();
    sourceLive = false; reply.resolve({ data: message("late-card") }); await act(flush);
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("the actual final send hook rechecks the source after target resolution awaits", async () => {
    let sourceLive = true; const reply = deferred<any>(); fixture.sdk.getOneConversation.mockReturnValueOnce(reply.promise);
    openChoice(() => sourceLive); await confirm(); expect(fixture.sdk.getOneConversation).toHaveBeenCalledOnce();
    sourceLive = false; reply.resolve({ data: { conversationID: "cid-user-target-1", conversationType: 1, userID: "target-1", groupID: "" } }); await act(flush);
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("history clear stops the source-backed share and ordinary no-source shares retain behavior", async () => {
    openChoice(() => true); act(() => messageHistoryFence.invalidate()); await confirm(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
    render(null); enter(); openChoice(); await confirm(); expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
  });
});
