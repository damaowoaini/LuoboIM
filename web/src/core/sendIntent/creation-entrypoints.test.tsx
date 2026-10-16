// @vitest-environment jsdom
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { deferred, enter, fixture, flush, message, messages, member, reset, store } from "./harness";
const ui = vi.hoisted(() => ({ upload: undefined as undefined | ((options: any) => Promise<unknown>) }));
vi.mock("antd", () => ({ Button: ({ children, onClick }: any) => <button onClick={onClick}>{children}</button>,
  Popover: ({ children }: any) => <>{children}</>, Spin: () => null,
  Upload: ({ children, customRequest }: any) => { ui.upload = customRequest; return <div>{children}</div>; } }));
vi.mock("@ant-design/icons", () => ({ DownOutlined: () => null, ExclamationCircleFilled: () => null, LoadingOutlined: () => null }));
vi.mock("@/pages/chat/queryChat/ChatFooter/AtSearchPanel", async () => ({
  ForwardAtSearchPanel: (await import("react")).forwardRef(() => null), SnapPreviewGroup: () => null }));
vi.mock("@/pages/chat/queryChat/ChatFooter/SendActionBar/CallPopContent", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/ChatFooter/SendActionBar/CutPopContent", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/ChatFooter/SendActionBar/EmojiPopContent", () => ({ default: () => null }));
vi.mock("@/components/Twemoji", () => ({ parseTwemoji: (text: string) => text }));
vi.mock("@/components/EditableDiv", async () => {
  const { forwardRef, useRef, useImperativeHandle } = await import("react");
  return { default: forwardRef(function Editor({ html, onChange, ...props }: any, ref) {
    const el = useRef<HTMLDivElement>(null); useImperativeHandle(ref, () => ({ el, emitChange: () => {} }));
    fixture.editorChange = value => onChange({ target: { value } });
    return <div id={props.id} ref={el} dangerouslySetInnerHTML={{ __html: html }} />;
  }), insertAtCursor: fixture.insert, restoreSelection: () => {}, deleteBeforeAt: () => {} };
});
import Footer from "@/pages/chat/queryChat/ChatFooter";
import MessageSuffix from "@/pages/chat/queryChat/MessageItem/MessageSuffix";
import ActionBar from "@/pages/chat/queryChat/ChatFooter/SendActionBar";
import { useFileMessage } from "@/pages/chat/queryChat/ChatFooter/SendActionBar/useFileMessage";
import { useSendMessage } from "@/pages/chat/queryChat/ChatFooter/useSendMessage";
let host: HTMLDivElement, root: Root;
beforeEach(() => { reset(); ui.upload = undefined; host = document.createElement("div"); document.body.append(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });
function Bar() { const send = useSendMessage(); const files = useFileMessage();
  return <ActionBar sendMessage={send.sendMessage} beginCurrentSend={send.beginCurrentSend} createFileMessage={files.createFileMessage} updateHtml={() => {}} inputFocus={() => {}} />; }
const click = (element: Element | null) => { expect(element).not.toBeNull(); act(() => (element as HTMLElement).click()); };

describe("actual composer, upload and retry callbacks", () => {
  it("captures footer intent before the debounce timer, so A text cannot become B text", async () => {
    act(() => root.render(<Footer />)); act(() => fixture.editorChange!("frozen A text"));
    click(([...host.querySelectorAll("button")].at(-1) ?? null)); act(() => enter("B"));
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 250)); await flush(); });
    expect(fixture.sdk.createTextMessage).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("does not clear the composer after an unknown text acknowledgement", async () => {
    fixture.sdk.sendMessage.mockRejectedValueOnce(new Error("synthetic unknown ACK"));
    act(() => root.render(<Footer />)); act(() => fixture.editorChange!("kept draft")); click(([...host.querySelectorAll("button")].at(-1) ?? null));
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 250)); await flush(); });
    expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce(); expect(host.querySelector("#editable-div")?.textContent).toBe("kept draft");
  });
  it("does not clear a draft edited away and back to equal text while an ACK waits", async () => {
    const reply = deferred<any>(); fixture.sdk.sendMessage.mockReturnValueOnce(reply.promise);
    act(() => root.render(<Footer />)); act(() => fixture.editorChange!("same text")); click(([...host.querySelectorAll("button")].at(-1) ?? null));
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 250)); await flush(); });
    expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
    act(() => fixture.editorChange!("new edit")); act(() => fixture.editorChange!("same text"));
    reply.resolve({ data: { ...fixture.sdk.sendMessage.mock.calls[0][0].message, status: 2 } }); await act(flush);
    expect(host.querySelector("#editable-div")?.textContent).toBe("same text");
  });
  it("clears unchanged same-view text only after a valid ACK", async () => {
    act(() => root.render(<Footer />)); act(() => fixture.editorChange!("ordinary text")); click(([...host.querySelectorAll("button")].at(-1) ?? null));
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 250)); await flush(); });
    expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce(); expect(host.querySelector("#editable-div")?.textContent).toBe("");
  });
  it("freezes a toolbar file-picker origin before local disk preparation", async () => {
    act(() => root.render(<Bar />)); click(host.querySelector('button[aria-label="placeholder.file"]'));
    const disk = deferred<string>(); fixture.save.mockReturnValueOnce(disk.promise);
    const file = new File(["synthetic"], "file.bin", { type: "application/octet-stream" });
    const pending = ui.upload!({ file }).catch(() => {}); await flush(); act(() => { enter("B"); enter("A"); });
    disk.resolve("/task-synthetic/file.bin"); await act(async () => { await pending; await flush(); });
    expect(fixture.sdk.createFileMessageByFile).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });
  it("does not resend an A failed file after asynchronous local-file recovery crosses to B", async () => {
    const source = { ...message(), status: 3, contentType: 105, fileElem: { uuid: "uuid", filePath: "/task-synthetic/file.bin" } };
    act(() => root.render(<MessageSuffix message={source} conversationID="cid-user-A" isSender />));
    const disk = deferred<File>(); fixture.readFile.mockReturnValueOnce(disk.promise); click(host.querySelector('button[aria-label="发送失败，重试这条消息"]'));
    await flush(); act(() => enter("B")); disk.resolve(new File(["synthetic"], "file.bin")); await act(flush);
    expect(fixture.sdk.fileMapSet).not.toHaveBeenCalled(); expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
  });  it("preserves a normal same-chat file retry without adding another optimistic bubble", async () => {
    const source = { ...message(), status: 3, contentType: 105, fileElem: { uuid: "uuid", filePath: "/task-synthetic/file.bin" } };
    act(() => root.render(<MessageSuffix message={source} conversationID="cid-user-A" isSender />));
    click(host.querySelector('button[aria-label="发送失败，重试这条消息"]')); await act(flush);
    expect(fixture.sdk.fileMapSet).toHaveBeenCalledOnce(); expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
    expect(fixture.sdk.sendMessage.mock.calls[0][0]).toMatchObject({ recvID: "A", groupID: "" });
  });
  const retryAfterOnly = (globalThis as typeof globalThis & { process?: { env?: { LUOBO_SEND_INTENT_STAGE?: string } } }).process?.env?.LUOBO_SEND_INTENT_STAGE === "before" ? it.skip : it;
  const mountGroupRetry = async () => {
    act(() => enter("G", "group")); await act(flush);
    act(() => root.render(<MessageSuffix message={{ ...message(), status: 3 }} conversationID="cid-group-G" isSender />));
    fixture.sdk.getSpecifiedGroupsInfo.mockClear(); fixture.sdk.getSpecifiedGroupMembersInfo.mockClear();
  };
  retryAfterOnly("restores same-view retry to Failed when final group-cache read rejects without dispatch", async () => {
    await mountGroupRetry(); fixture.sdk.getSpecifiedGroupsInfo.mockRejectedValueOnce(new Error("synthetic read failure"));
    click(host.querySelector('button[aria-label="发送失败，重试这条消息"]')); await act(flush);
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
    expect(vi.mocked(messages.getState().updateOneMessage).mock.calls.map(([value]: any) => value.status)).toEqual([1, 3]);
  });
  retryAfterOnly("restores same-view retry to Failed when final member permission denies without dispatch", async () => {
    await mountGroupRetry(); fixture.sdk.getSpecifiedGroupMembersInfo.mockResolvedValueOnce({ data: [member("G", "actor", 20, Date.now() + 10000)] });
    click(host.querySelector('button[aria-label="发送失败，重试这条消息"]')); await act(flush);
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
    expect(vi.mocked(messages.getState().updateOneMessage).mock.calls.map(([value]: any) => value.status)).toEqual([1, 3]);
  });
  for (const boundary of ["navigation", "owner"] as const) retryAfterOnly(`never restores an old retry bubble after ${boundary} changes while group read waits`, async () => {
    await mountGroupRetry(); const reply = deferred<any>(); fixture.sdk.getSpecifiedGroupsInfo.mockReturnValueOnce(reply.promise);
    click(host.querySelector('button[aria-label="发送失败，重试这条消息"]')); await act(flush);
    expect(fixture.sdk.getSpecifiedGroupsInfo).toHaveBeenCalledOnce();
    expect(messages.getState().updateOneMessage).toHaveBeenCalledWith(expect.objectContaining({ status: 1 }));
    vi.mocked(messages.getState().updateOneMessage).mockClear();
    if (boundary === "navigation") act(() => { enter("B"); enter("G", "group"); });
    else { fixture.session.generation++; fixture.session.current = { ...fixture.owner(), tenant: { serverID: "B" } }; }
    reply.resolve({ data: [{ groupID: "G", status: 0 }] }); await act(flush);
    expect(fixture.sdk.sendMessage).not.toHaveBeenCalled(); expect(messages.getState().updateOneMessage).not.toHaveBeenCalled();
  });

});
