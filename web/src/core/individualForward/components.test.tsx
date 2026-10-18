// @vitest-environment jsdom
import React, { act, createRef, useEffect, useRef, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fixture, reset, enter, store, messages, conversation, group, member, message, deferred, flush } from "./fixture";
vi.mock("antd", () => ({
  Spin: ({ children }: any) => children,
  Button: ({ children, onClick, disabled, loading }: any) => <button type="button" disabled={disabled || loading} onClick={onClick}>{children}</button>,
  Modal: ({ children, open }: any) => open ? <section aria-label="recipient picker">{children}</section> : null,
  Input: () => null, Upload: () => null,
}));
vi.mock("@ant-design/icons", () => ({ CloseOutlined: ({ onClick }: any) => <button aria-label="close picker" onClick={onClick}>close</button> }));
vi.mock("@/components/OIMAvatar", () => ({ default: () => null }));
vi.mock("@/pages/common/ChooseModal/ChooseBox", async () => {
  const { forwardRef, useImperativeHandle } = await import("react");
  return { default: forwardRef(function Box(_props, ref) {
    useImperativeHandle(ref, () => ({ getCheckedList: () => fixture.selection, getAdditional: () => fixture.additional,
      resetState: () => {}, updatePrevCheckList: () => {} })); return <div>recipient selection boundary</div>;
  }) };
});
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
import MassMessageRenderer from "@/pages/chat/queryChat/MessageItem/MassMessageRenderer";
import { legacyTagContent, legacyTagSummary, isLegacyTagVoice, canForwardLegacyTagMessages } from "@/core/legacyTagMessage";
import MultipleActionBar from "@/pages/chat/queryChat/ChatFooter/MultipleActionBar";
import ChooseModal, { type ChooseModalState } from "@/pages/common/ChooseModal";
import type { OverlayVisibleHandle } from "@/hooks/useOverlayVisible";
import { captureIndividualForward, type IndividualForwardSelection } from "@/core/individualForward";
import { captureSendOrigin } from "@/core/messageSendIntent";
import { forgetMessage, messageOwner, messageHistoryFence } from "@/core/messagePrivacyRuntime";
import emitter from "@/utils/events";
type RecordedEmit = (type: Parameters<typeof emitter.emit>[0], payload?: unknown) => void;
let host: HTMLDivElement, root: Root;
const sourceCID = "cid-user-A";
const wire = (id: string) => ({ ...message(id), status: 2, sendTime: 1800000000000, textElem: { content: id }, checked: true });
const render = (node: React.ReactNode) => act(() => root.render(node));
const confirm = async () => { await act(async () => {
  const button = [...host.querySelectorAll<HTMLButtonElement>("button")].find(value => value.textContent === "confirm");
  expect(button).toBeDefined(); button!.click(); await flush();
}); };
function selection(ids = ["one", "two"]) {
  for (const id of ids) fixture.sources.set(id, wire(id));
  return captureIndividualForward(sourceCID, messageOwner(), ids)!;
}
const modalRef = createRef<OverlayVisibleHandle>();
function open(value: IndividualForwardSelection, targets: any[] = [{ userID: "U" }]) {
  fixture.selection = targets;
  const sendOrigin = captureSendOrigin(store.getState().captureConversationSendScope(), value.sourceCID);
  render(<ChooseModal ref={modalRef} state={{ type: "FORWARD_MESSAGE", sourceConversationID: value.sourceCID,
    sourceOwner: value.owner, sourceCurrent: value.current, individualSelection: value, sendOrigin }} />);
  act(() => modalRef.current!.openOverlay());
}
// Unchanged TopSearchBar's event admission is represented at the hosting seam;
// toolbar, picker modal, operation, sender, Zustand and both SDK wrappers are real.
function Host() {
  const ref = useRef<OverlayVisibleHandle>(null);
  const [state, setState] = useState<ChooseModalState>({ type: "CRATE_GROUP" });
  useEffect(() => {
    const handler = (params: ChooseModalState) => setState({ ...params,
      sendOrigin: captureSendOrigin(store.getState().captureConversationSendScope(), params.sourceConversationID) });
    emitter.on("OPEN_CHOOSE_MODAL", handler); return () => emitter.off("OPEN_CHOOSE_MODAL", handler);
  }, []);
  useEffect(() => { if (state.type === "FORWARD_MESSAGE") ref.current!.openOverlay(); }, [state]);
  return <><MultipleActionBar /><ChooseModal ref={ref} state={state} /></>;
}
beforeEach(() => { reset(); host = document.createElement("div"); document.body.append(host); root = createRoot(host); });
afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.restoreAllMocks(); });

describe("actual individual-forward entry, picker and sender", () => {
  it("offers a keyboard button, visible twenty-source bound and separate ID-only ordered intent", async () => {
    const emit = vi.spyOn(emitter, "emit");
    const sources = Array.from({ length: 20 }, (_, i) => wire(`source-${19 - i}`));
    messages.setState({ historyMessageList: sources });
    render(<MultipleActionBar />);
    const button = host.querySelector<HTMLButtonElement>('button[aria-label="placeholder.individualForward"]')!;
    expect(button.type).toBe("button"); expect(button.style.minHeight).toBe("44px"); expect(button.style.minWidth).toBe("44px");
    expect(button.className).toContain("focus-visible"); expect(host.textContent).toContain("20/20");
    expect(button.parentElement!.className).toContain("flex-wrap");
    act(() => button.click());
    const payload = (emit.mock.calls as Parameters<RecordedEmit>[]).find(([event]) => event === "OPEN_CHOOSE_MODAL")![1] as ChooseModalState;
    expect(payload.individualSelection!.sourceIDs).toEqual(Array.from({ length: 20 }, (_, i) => `source-${i}`));
    expect(payload.extraData).toBeUndefined(); expect(payload.sourceConversationID).toBe(sourceCID);
    expect(Object.keys(payload.individualSelection!)).not.toContain("messageList");
    expect(messages.getState().isCheckMode).toBe(false); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("does not truncate or open a picker for zero or twenty-one selected sources", () => {
    const emit = vi.spyOn(emitter, "emit"); render(<MultipleActionBar />);
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="placeholder.individualForward"]')!.click());
    act(() => messages.setState({ historyMessageList: Array.from({ length: 21 }, (_, i) => wire(String(i))) }));
    expect(host.textContent).toContain("21/20");
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="placeholder.individualForward"]')!.click());
    expect(emit).not.toHaveBeenCalled(); expect(fixture.toast).toHaveBeenCalledTimes(2);
    expect(fixture.sdk.findMessageList).not.toHaveBeenCalled();
  });
  it("takes the real toolbar path through fresh exact-CID lookup and distinct user/group sends", async () => {
    fixture.sources.set("one", wire("one")); fixture.sources.set("two", wire("two"));
    fixture.selection = [{ userID: "U" }, { groupID: "G" }]; fixture.additional = "note";
    messages.setState({ historyMessageList: [wire("two"), wire("one")] }); render(<Host />);
    await act(async () => { host.querySelector<HTMLButtonElement>('button[aria-label="placeholder.individualForward"]')!.click(); await flush(); });
    await confirm();
    expect(fixture.sdk.createForwardMessage.mock.calls.map(([source]) => source.clientMsgID)).toEqual(["one", "two", "one", "two"]);
    expect(fixture.sdk.createMergerMessage).not.toHaveBeenCalled();
    expect(fixture.sdk.sendMessage.mock.calls.map(([p]) => [p.recvID, p.groupID])).toEqual([["U", ""], ["U", ""], ["U", ""], ["", "G"], ["", "G"], ["", "G"]]);
    expect(fixture.sdk.findMessageList.mock.calls.every(([query]) => query[0].conversationID === sourceCID && query[0].clientMsgIDList.length === 1)).toBe(true);
    expect(fixture.sdk.createTextMessage.mock.calls).toEqual([["note"], ["note"]]);
    expect(host.querySelectorAll("li")).toHaveLength(6);
    expect([...host.querySelectorAll("li")].every(row => row.textContent!.includes("placeholder.individualForwardState.acknowledged"))).toBe(true);
  });
  it("preserves explicit targets through navigation and ordinary same-CID metadata refresh", async () => {
    const value = selection(["one"]), reply = deferred<any>();
    fixture.sdk.findMessageList.mockReturnValueOnce(reply.promise); open(value); await confirm();
    act(() => store.getState().updateCurrentConversation({ ...conversation(), unreadCount: 7 }, true));
    act(() => enter("B"));
    reply.resolve({ data: { totalCount: 1, findResultItems: [{ conversationID: sourceCID, messageCount: 1, messageList: [wire("one")] }] } });
    await act(flush);
    expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce(); expect(fixture.sdk.sendMessage.mock.calls[0][0].recvID).toBe("U");
    expect(messages.getState().updateOneMessage).not.toHaveBeenCalled();
  });
  it("does not read or create for a foreign actor returned by final group permission lookup", async () => {
    const value = selection(); fixture.sdk.getSpecifiedGroupMembersInfo.mockResolvedValue({ data: [member("G", "foreign")] });
    open(value, [{ groupID: "G" }]); await confirm();
    expect(fixture.sdk.findMessageList).not.toHaveBeenCalled(); expect(fixture.sdk.createForwardMessage).not.toHaveBeenCalled();
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled(); expect(value.receipts()[0].state).toBe("rejected");
  });
  it("checks the resolved target CID when a target clear was already in progress at capture", async () => {
    const endClear = messageHistoryFence.beginClear(messageOwner(), "cid-user-U");
    try {
      const value = selection(); open(value); await confirm();
      expect(fixture.sdk.getOneConversation).toHaveBeenCalledOnce();
      expect(fixture.sdk.findMessageList).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
      expect(value.receipts()[0].state).toBe("rejected");
    } finally { await act(async () => { endClear(); await flush(); }); }
  });
  it("refuses a resolved recipient identity mismatch instead of borrowing the active chat", async () => {
    const value = selection(); fixture.sdk.getOneConversation.mockResolvedValue({ data: conversation("foreign") });
    open(value); await confirm();
    expect(fixture.sdk.createForwardMessage).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("honors source revocation during creation and never sends its late prepared message", async () => {
    const value = selection(), reply = deferred<any>(); fixture.sdk.createForwardMessage.mockReturnValueOnce(reply.promise);
    open(value); await confirm(); expect(fixture.sdk.createForwardMessage).toHaveBeenCalledOnce();
    act(() => forgetMessage(sourceCID, "one")); reply.resolve({ data: message("late-forward") }); await act(flush);
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled(); expect(value.receipts().every(row => row.state === "not-attempted")).toBe(true);
  });
  it("keeps ACK, unknown and not-attempted rows visible and disables replay of this panel", async () => {
    const value = selection(); fixture.sdk.sendMessage.mockResolvedValueOnce({ data: { ...message("created-1"), status: 2 } })
      .mockRejectedValueOnce(new Error("synthetic unknown receipt"));
    open(value, [{ userID: "U" }, { userID: "V" }]); await confirm();
    expect(value.receipts().map(row => row.state)).toEqual(["acknowledged", "unconfirmed", "not-attempted", "not-attempted"]);
    expect(host.querySelectorAll("li")).toHaveLength(4); expect(fixture.sdk.sendMessage).toHaveBeenCalledTimes(2);
    const button = [...host.querySelectorAll<HTMLButtonElement>("button")].find(value => value.textContent === "confirm")!;
    expect(button.disabled).toBe(true); act(() => button.click());
    act(() => modalRef.current!.closeOverlay()); act(() => modalRef.current!.openOverlay()); await confirm();
    expect(fixture.sdk.sendMessage).toHaveBeenCalledTimes(2);
    expect(fixture.toast.mock.calls.some(([v]) => String(v).includes("sendSuccess"))).toBe(false);
  });
  it("treats an actual session-wrapper discarded ACK as unconfirmed and writes no new-owner UI", async () => {
    const value = selection(), reply = deferred<any>(); fixture.sdk.sendMessage.mockReturnValueOnce(reply.promise);
    open(value); await confirm(); expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
    expect(host.querySelector("li")!.textContent).toContain("placeholder.individualForwardState.processing");
    act(() => { fixture.session.generation++; fixture.session.current = { ...fixture.owner(), userID: "other" }; });
    reply.resolve({ data: { ...message("created-1"), status: 2 } }); await act(flush);
    expect(value.receipts()[0].state).toBe("unconfirmed"); expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
    expect(messages.getState().updateOneMessage).not.toHaveBeenCalled(); expect(host.querySelectorAll("li")).toHaveLength(0);
  });
  it("settles an observed ACK after a same-view mute and refuses the next source before creation", async () => {
    enter("G", "group"); store.setState({ currentGroupInfo: group(), currentMemberInGroup: member() });
    const value = selection(), reply = deferred<any>(); fixture.sdk.sendMessage.mockReturnValueOnce(reply.promise);
    open(value, [{ groupID: "G" }]); await confirm(); expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
    act(() => store.getState().updateCurrentGroupInfo(group("G", 3)));
    reply.resolve({ data: { ...message("created-1"), status: 2 } }); await act(flush);
    expect(value.receipts().map(row => row.state)).toEqual(["acknowledged", "rejected"]);
    expect(messages.getState().updateOneMessage).toHaveBeenCalledWith(expect.objectContaining({ clientMsgID: "created-1", status: 2 }));
    expect(fixture.sdk.createForwardMessage).toHaveBeenCalledOnce();
  });
  it("does not revive source lookup after a clear finishes with a fresh history revision", async () => {
    const value = selection(), reply = deferred<any>(); fixture.sdk.findMessageList.mockReturnValueOnce(reply.promise);
    open(value); await confirm();
    act(() => { const end = messageHistoryFence.beginClear(messageOwner(), sourceCID); end(); });
    reply.resolve({ data: { totalCount: 1, findResultItems: [{ conversationID: sourceCID, messageCount: 1, messageList: [wire("one")] }] } });
    await act(flush); expect(fixture.sdk.createForwardMessage).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("does not project or dispatch old preparation into a replacement picker operation", async () => {
    const old = selection(), reply = deferred<any>(); fixture.sdk.createForwardMessage.mockReturnValueOnce(reply.promise);
    open(old); await confirm(); const next = selection(["three"]); open(next, [{ userID: "V" }]);
    reply.resolve({ data: message("late-old") }); await act(flush);
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled(); expect(next.started()).toBe(false); expect(host.querySelectorAll("li")).toHaveLength(0);
  });
  it("rechecks metadata-only source retention at the additional text sender's final target await", async () => {
    const value = selection(["one"]), reply = deferred<any>(); fixture.additional = "note";
    let reads = 0;
    fixture.sdk.getOneConversation.mockImplementation(async ({ sourceID, sessionType }) => {
      if (++reads === 4) return reply.promise;
      return { data: conversation(sourceID, sessionType === 3 ? "group" : "user") };
    });
    open(value); await confirm(); expect(reads).toBe(4); expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
    act(() => forgetMessage(sourceCID, "one")); reply.resolve({ data: conversation("U") }); await act(flush);
    expect(value.receipts().map(row => row.state)).toEqual(["acknowledged", "rejected"]);
    expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
  });
  // R2_NEW_SCENARIOS_BEGIN
  it("stops pending preparation with no later send and shows bounded recipient names and source ordinals", async () => {
    const ids = ["internal-source-uuid-first", "internal-source-uuid-second"];
    const value = selection(ids), reply = deferred<any>(); fixture.additional = "note";
    fixture.sdk.createForwardMessage.mockReturnValueOnce(reply.promise);
    const longName = "<b>当前收件人</b>" + "名".repeat(100);
    open(value, [{ userID: "U", nickname: longName }]); await confirm();
    expect(fixture.sdk.createForwardMessage).toHaveBeenCalledOnce();
    const stop = host.querySelector<HTMLButtonElement>('button[aria-label="placeholder.individualForwardStop"]')!;
    expect(stop.type).toBe("button"); expect(stop.style.minHeight).toBe("44px"); expect(stop.style.minWidth).toBe("44px");
    expect(stop.className).toContain("focus-visible"); act(() => stop.click()); expect(stop.disabled).toBe(true);
    expect(value.current()).toBe(true); // Stop does not retire the source/view lease or hide receipts.
    const labels = [...host.querySelectorAll("li")].map(row => row.textContent!);
    expect(labels).toHaveLength(3); expect(labels[0]).toContain(longName.slice(0, 80));
    expect(labels[0]).not.toContain("名".repeat(100)); expect(host.querySelector("b")).toBeNull();
    expect(labels[0]).toContain("第1条"); expect(labels[1]).toContain("第2条"); expect(labels[2]).toContain("placeholder.individualForwardAdditional");
    expect(ids.every(id => !host.textContent!.includes(id))).toBe(true);
    reply.resolve({ data: message("late-created") }); await act(flush);
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled(); expect(fixture.sdk.createTextMessage).not.toHaveBeenCalled();
    expect(value.receipts().map(row => [row.state, row.reason])).toEqual([
      ["not-attempted", "stopped"], ["not-attempted", "stopped"], ["not-attempted", "stopped"],
    ]);
    expect(value.receipts().filter(row => row.kind === "message").map(row => row.sourceID)).toEqual(ids);
  });
  it("settles the native tuple after stop, keeps its ACK visible and permits a later explicit reselection", async () => {
    const value = selection(), reply = deferred<any>(); fixture.additional = "note";
    fixture.sdk.sendMessage.mockReturnValueOnce(reply.promise);
    open(value, [{ userID: "U", nickname: "当前收件人" }]); await confirm();
    expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="placeholder.individualForwardStop"]')!.click());
    expect(value.current()).toBe(true); expect(host.querySelector("li")!.textContent).toContain("placeholder.individualForwardState.processing");
    reply.resolve({ data: { ...message("created-1"), status: 2 } }); await act(flush);
    expect(value.receipts().map(row => row.state)).toEqual(["acknowledged", "not-attempted", "not-attempted"]);
    expect(host.querySelectorAll("li")).toHaveLength(3);
    expect(host.querySelector("li")!.textContent).toContain("placeholder.individualForwardState.acknowledged");
    expect(host.textContent).toContain("placeholder.individualForwardConfirmedTotal: 1/3");
    expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce(); expect(fixture.sdk.createTextMessage).not.toHaveBeenCalled();
    await confirm(); expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
    fixture.additional = ""; const next = selection(["one"]); open(next, [{ userID: "U", nickname: "当前收件人" }]); await confirm();
    expect(next.receipts()[0].state).toBe("acknowledged"); expect(fixture.sdk.sendMessage).toHaveBeenCalledTimes(2);
  });
  it("stops the final sender's pending target read without claiming a native cancellation", async () => {
    const value = selection(), reply = deferred<any>(); let reads = 0;
    fixture.sdk.getOneConversation.mockImplementation(async ({ sourceID, sessionType }) => {
      if (++reads === 2) return reply.promise;
      return { data: conversation(sourceID, sessionType === 3 ? "group" : "user") };
    });
    open(value); await confirm(); expect(reads).toBe(2); expect(fixture.sdk.createForwardMessage).toHaveBeenCalledOnce();
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="placeholder.individualForwardStop"]')!.click());
    reply.resolve({ data: conversation("U") }); await act(flush);
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
    expect(value.receipts().map(row => [row.state, row.reason])).toEqual([["not-attempted", "stopped"], ["not-attempted", "stopped"]]);
    expect(host.querySelectorAll("li")).toHaveLength(2);
  });
  // R2_NEW_SCENARIOS_END
});

describe("legacy tag903 projection and actual forwarding", () => {
  const tag = (data: unknown, id = "tag903") => Object.freeze({ ...wire(id), contentType: 110,
    customElem: Object.freeze({ data: JSON.stringify({ customType: 903, data }) }) });
  const sound = { sourceUrl: "https://objects.synthetic.test/tag.ogg", duration: 3 };
  const openForward = (extraData: unknown) => {
    fixture.selection = [{ userID: "U" }];
    const ref = createRef<OverlayVisibleHandle>();
    const sendOrigin = captureSendOrigin(store.getState().captureConversationSendScope(), sourceCID);
    render(<ChooseModal ref={ref} state={{ type: "FORWARD_MESSAGE", extraData, sourceConversationID: sourceCID,
      sourceOwner: messageOwner(), sendOrigin }} />);
    act(() => ref.current!.openOverlay());
  };
  it("projects tag903 text and HTTPS audio without changing the custom wire", () => {
    const text = tag({ textElem: { content: "<img src=x onerror=alert(1)>" } });
    expect(legacyTagContent(text)).toEqual({ kind: "text", text: "<img src=x onerror=alert(1)>" });
    expect(legacyTagSummary(text)).toBe("<img src=x onerror=alert(1)>");
    render(<MassMessageRenderer message={text} isSender={false} conversationID={sourceCID} />);
    expect(host.textContent).toBe("<img src=x onerror=alert(1)>"); expect(host.querySelector("img")).toBeNull();
    const voice = tag({ soundElem: sound }); const raw = voice.customElem.data;
    expect(legacyTagContent(voice)).toEqual({ kind: "audio", durationSeconds: 3,
      playableAudio: { sourceUrl: sound.sourceUrl, durationSeconds: 3 } });
    expect(legacyTagSummary(voice)).toBe("[标签语音] 3 秒");
    expect(voice.contentType).toBe(110); expect(voice.customElem.data).toBe(raw); expect(voice.soundElem).toBeUndefined();
    expect(legacyTagContent({ ...voice, contentType: 103 })).toBeUndefined();
  });
  it("keeps every non-null text element ahead of tag903 audio", () => {
    const text = tag({ textElem: { content: "text wins" }, soundElem: sound });
    expect(legacyTagContent(text)).toEqual({ kind: "text", text: "text wins" });
    render(<MassMessageRenderer message={text} isSender={false} conversationID={sourceCID} />);
    expect(host.textContent).toBe("text wins"); expect(isLegacyTagVoice(text)).toBe(true);
    expect(canForwardLegacyTagMessages(text)).toBe(false);
    for (const textElem of [{}, { content: 4 }, "invalid", false]) {
      const source = tag({ textElem, soundElem: sound });
      expect(legacyTagContent(source)).toBeUndefined(); expect(isLegacyTagVoice(source)).toBe(true);
      expect(canForwardLegacyTagMessages(source)).toBe(false);
      render(<MassMessageRenderer message={source} isSender={false} conversationID={sourceCID} />);
      expect(host.textContent).toBe("messageDescription.catchMessage"); expect(host.querySelector('[role="button"]')).toBeNull();
    }
  });
  it("rejects unsafe tag903 media and invalid durations while retaining the voice forward prohibition", () => {
    for (const sourceUrl of ["http://objects.synthetic.test/a", "javascript:alert(1)", "data:audio/ogg;base64,AA==",
      "file:///tmp/a", "/local/a", "https:objects.synthetic.test/a", "https://user:secret@objects.synthetic.test/a", "https://", 5]) {
      const source = tag({ soundElem: { ...sound, sourceUrl, soundPath: "/tmp/untrusted.ogg" } });
      expect(legacyTagContent(source)?.kind).toBe("audio");
      expect(legacyTagContent(source)).not.toHaveProperty("playableAudio.sourceUrl");
      expect(canForwardLegacyTagMessages(source)).toBe(false);
      render(<MassMessageRenderer message={source} isSender={false} conversationID={sourceCID} />);
      expect(host.querySelector('[role="button"]')).toBeNull();
    }
    for (const duration of [-1, 1.5, "3", true, 1e20, Number.MAX_SAFE_INTEGER + 1]) {
      const source = tag({ soundElem: { ...sound, duration } });
      expect(legacyTagContent(source)).not.toHaveProperty("playableAudio.sourceUrl");
      expect(canForwardLegacyTagMessages(source)).toBe(false);
    }
    for (const duration of [undefined, null, 0, 3]) {
      expect(legacyTagContent(tag({ soundElem: { ...sound, duration } }))).toHaveProperty("playableAudio.sourceUrl", sound.sourceUrl);
    }
    expect(legacyTagContent(tag({ soundElem: { soundPath: "/tmp/untrusted.ogg", duration: 3 } }))).not.toHaveProperty("playableAudio.sourceUrl");
    expect(legacyTagContent({ contentType: 110, customElem: { data: "{" } })).toBeUndefined();
    expect(legacyTagContent({ contentType: 110, customElem: { data: "x".repeat(65537) } })).toBeUndefined();
  });
  it("loads tag903 audio only after keyboard admission and releases it at expiry and unmount", async () => {
    vi.useFakeTimers(); const now = 1800000000000; vi.setSystemTime(now);
    const audios: { src: string; preload: string; onplay: (() => void) | null; play: ReturnType<typeof vi.fn>; pause: ReturnType<typeof vi.fn>; removeAttribute: ReturnType<typeof vi.fn> }[] = [];
    vi.stubGlobal("Audio", class {
      src = ""; preload = ""; onended: (() => void) | null = null; onpause: (() => void) | null = null; onplay: (() => void) | null = null;
      constructor() { audios.push(this); }
      play = vi.fn(async () => { this.onplay?.(); }); pause = vi.fn(() => { this.onpause?.(); });
      removeAttribute = vi.fn((name: string) => { if (name === "src") this.src = ""; });
    });
    try {
      const source = { ...tag({ soundElem: sound }), attachedInfoElem: { isPrivateChat: true, burnDuration: 1, hasReadTime: now } };
      render(<MassMessageRenderer message={source} isSender={false} conversationID={sourceCID} />);
      const audio = audios[0]; expect(audio.preload).toBe("none"); expect(audio.src).toBe(""); expect(audio.play).not.toHaveBeenCalled();
      const control = host.querySelector<HTMLElement>('[role="button"]')!; expect(control.tabIndex).toBe(0);
      await act(async () => { control.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })); });
      expect(audio.play).toHaveBeenCalledOnce(); expect(audio.src).toBe(sound.sourceUrl); expect(audios).toHaveLength(1);
      await act(async () => { host.querySelector<HTMLElement>('[role="button"]')!.dispatchEvent(new KeyboardEvent("keydown", { key: " ", bubbles: true, cancelable: true })); });
      expect(audio.pause).toHaveBeenCalled(); expect(audio.play).toHaveBeenCalledOnce();
      await act(async () => { await vi.advanceTimersByTimeAsync(1250); });
      expect(host.textContent).toContain("消息已过期"); expect(audio.src).toBe(""); expect(audios).toHaveLength(1);
      expect(audio.removeAttribute).toHaveBeenCalledWith("src");
      await act(async () => root.render(null)); expect(audio.onplay).toBeNull();
    } finally { await act(async () => root.render(null)); vi.unstubAllGlobals(); vi.useRealTimers(); }
  });
  it("does not load or play a tag903 projection after issuer generation or source-history retirement", async () => {
    const play = vi.fn(async () => {}); const audios: { src: string }[] = [];
    vi.stubGlobal("Audio", class {
      src = ""; preload = ""; onended = null; onpause = null; onplay = null;
      constructor() { audios.push(this); } play = play; pause = vi.fn();
      removeAttribute = vi.fn((name: string) => { if (name === "src") this.src = ""; });
    });
    try {
      const changes = [() => { fixture.session.current!.issuer = "https://other.synthetic.test"; },
        () => { fixture.session.generation++; }, () => { messageHistoryFence.invalidate(); }];
      for (const [index, change] of changes.entries()) {
        fixture.session.current = fixture.owner(); fixture.session.generation++;
        render(<MassMessageRenderer key={index} message={tag({ soundElem: sound }, `retired-${index}`)} isSender={false} conversationID={sourceCID} />);
        const control = host.querySelector<HTMLElement>('[role="button"]')!;
        act(change);
        await act(async () => { control.click(); });
      }
      expect(play).not.toHaveBeenCalled(); expect(audios.every(audio => audio.src === "")).toBe(true);
    } finally { await act(async () => root.render(null)); vi.unstubAllGlobals(); }
  });
  it("rejects actual single merge and nested-history forwards by tag903 voice identity before SDK creation", async () => {
    for (const extra of [tag({ soundElem: sound }), tag({ soundElem: { sourceUrl: "javascript:bad" } }),
      tag({ textElem: {}, soundElem: sound }),
      { title: "history", messageList: [wire("normal"), tag({ soundElem: sound })], summaryList: [] },
      { ...wire("old-merge"), contentType: 107, mergeElem: { multiMessage: [tag({ soundElem: sound })] } }]) {
      openForward(extra); await confirm();
    }
    expect(fixture.sdk.createForwardMessage).not.toHaveBeenCalled(); expect(fixture.sdk.createMergerMessage).not.toHaveBeenCalled();
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled(); expect(fixture.toast).toHaveBeenCalledWith("标签语音不能转发");
  });
  it("rejects a freshly read tag903 voice in the actual individual-forward operation", async () => {
    for (const sourceUrl of [sound.sourceUrl, "javascript:bad"]) {
      const value = selection(["fresh-tag903"]); fixture.sources.set("fresh-tag903", tag({ soundElem: { sourceUrl } }, "fresh-tag903"));
      open(value); await confirm();
      expect(value.receipts()[0]).toMatchObject({ state: "rejected", reason: "prepare" });
    }
    expect(fixture.sdk.findMessageList).toHaveBeenCalledTimes(2); expect(fixture.sdk.createForwardMessage).not.toHaveBeenCalled();
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("preserves tag903 text priority and ordinary actual forwarding", async () => {
    const mixed = tag({ textElem: { content: "text wins" }, soundElem: sound });
    openForward(mixed); await confirm();
    expect(fixture.sdk.createForwardMessage).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
    const source = tag({ textElem: { content: "text wins" } });
    openForward(source); await confirm();
    expect(fixture.sdk.createForwardMessage.mock.calls[0][0]).toEqual(source); expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
    openForward(wire("ordinary")); await confirm(); expect(fixture.sdk.createForwardMessage).toHaveBeenCalledTimes(2);
    expect(fixture.sdk.sendMessage).toHaveBeenCalledTimes(2); expect(source.contentType).toBe(110); expect(source.soundElem).toBeUndefined();
  });
});
