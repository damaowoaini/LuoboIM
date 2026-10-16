// @vitest-environment jsdom
// Actual card/quote/merge renderers, retained MergePreview, search row, UserCard
// and exact-ID loadUserCard resolver. Session/navigation, SDK/business/OS and
// surrounding UI chrome are adapted. No HTTP, account, file or relation writes.
import React, { act, createRef, useEffect, useRef, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { OverlayVisibleHandle } from "@/hooks/useOverlayVisible";
const f = vi.hoisted(() => ({
  nav: 0, cid: "source", groupID: undefined as string | undefined, group: undefined as any, member: undefined as any, session: { generation: 1, current: { issuer: "https://synthetic.test", tenant: { serverID: "A" }, userID: "owner" } },
  listeners: new Set<() => void>(), emit: vi.fn(), download: vi.fn(), fetch: vi.fn(), sdk: vi.fn(), business: vi.fn(), feedback: vi.fn(), toChat: vi.fn(), createCard: vi.fn(),
}));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: {
  get current() { return f.session.current; }, get generation() { return f.session.generation; },
  onChange: (cb: () => void) => { f.listeners.add(cb); return () => f.listeners.delete(cb); },
} }));
vi.mock("@/store", () => {
  const state = { selfInfo: { userID: "owner", nickname: "Self" }, appConfig: { allowSendMsgNotFriend: 1 }, imageCache: {}, downloadMap: {}, get currentGroupInfo() { return f.group; }, get currentMemberInGroup() { return f.member; },
    captureConversationViewScope: () => { const epoch = f.nav, cid = f.cid, groupID = f.groupID; return { conversationID: cid, groupID, current: () => f.nav === epoch && f.cid === cid && f.groupID === groupID }; } };
  const hook = Object.assign((select: any) => select(state), { getState: () => state, subscribe: () => () => {} });
  return { useUserStore: hook, useMessageStore: hook, useConversationStore: hook };
});
vi.mock("@/store/contact", () => ({ useContactStore: (select: any) => select({ friendList: [] }) }));
vi.mock("@/layout/MainContentWrap", () => ({ IMSDK: { getUsersInfoWithCache: f.sdk, createCardMessage: f.createCard, on: vi.fn(), off: vi.fn() } }));
vi.mock("@/api/login", () => ({ BusinessAllowType: { Allow: 1, NotAllow: 2 }, getBusinessUserInfo: f.business }));
vi.mock("@/utils/events", () => ({ default: { emit: f.emit } }));
vi.mock("@/utils/common", () => ({ downloadFile: f.download, feedbackToast: f.feedback,
  escapeHtml: (s = "") => String(s).replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;"), sanitizeHtml: (s: string) => s }));
vi.mock("@/utils/imCommon", () => ({ formatLink: (s: string) => s, formatMessageByType: (m: any) => m.contentType === 108 ? m.cardElem.nickname : "text", formatMessageTime: () => "time" }));
vi.mock("@/constants", () => ({ canSearchMessageTypes: [108] }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (s: string) => s }) }));
vi.mock("i18next", () => ({ t: (s: string) => s }));
vi.mock("@/AntdGlobalComp", () => ({ message: { warning: vi.fn() }, modal: { confirm: vi.fn() } }));
vi.mock("open-im-sdk-wasm", () => ({
  MessageType: { TextMessage: 101, PictureMessage: 102, VoiceMessage: 103, VideoMessage: 104, FileMessage: 105, AtTextMessage: 106, MergeMessage: 107, CardMessage: 108, LocationMessage: 109, CustomMessage: 110, QuoteMessage: 114, RevokeMessage: 2101 },
  AllowType: { Allowed: 0, NotAllowed: 1 }, SessionType: { Single: 1 }, GroupJoinSource: { Invitation: 2, QrCode: 3 }, CbEvents: { OnFriendAdded: "friend" },
}));
vi.mock("react-use", () => ({ useCopyToClipboard: () => [null, vi.fn()] }));
vi.mock("@/hooks/useConversationToggle", () => ({ useConversationToggle: () => ({ toSpecifiedConversation: f.toChat }) }));
vi.mock("@/hooks/useMessageFileDownloadState", () => ({ useMessageFileDownloadState: () => ({ tryDownload: vi.fn() }) }));
vi.mock("@/core/usePagedMessageSearch", () => ({ usePagedMessageSearch: () => ({ messageList: [], search: vi.fn() }) }));
vi.mock("react-virtuoso", () => ({ Virtuoso: () => null }));
vi.mock("@ant-design/icons", () => ({ CloseOutlined: ({ onClick }: any) => <button aria-label="close merge" onClick={onClick}>close</button> }));
vi.mock("antd", () => ({
  Avatar: ({ src, children }: any) => src ? <img alt="avatar" src={src} /> : <span>{children}</span>,
  Button: ({ children, onClick, disabled }: any) => <button onClick={onClick} disabled={disabled}>{children}</button>,
  Space: ({ children }: any) => <span>{children}</span>, Tooltip: ({ children }: any) => <span>{children}</span>, Popover: ({ children }: any) => <span>{children}</span>,
  Divider: () => null, Spin: ({ children }: any) => children, Empty: () => null,
  // Real Drawer/Modal may retain a closed subtree: don't hide lifetime bugs by unmounting it.
  Modal: ({ children, open }: any) => <section aria-label="merge portal" hidden={!open}>{children}</section>,
}));
vi.mock("@/components/DraggableModalWrap", () => ({ default: ({ open, children, onCancel }: any) => open ? <section aria-label="profile"><button aria-label="close profile" onClick={onCancel}>close</button>{children}</section> : null }));
vi.mock("@/components/EditableContent", () => ({ default: () => null }));
vi.mock("@/components/UserLoginInfo", () => ({ default: () => null }));
vi.mock("@/components/UserLoginRecordList", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/ChatFooter/SendActionBar/CallPopContent", () => ({ default: () => null }));
vi.mock("@/pages/common/UserCardModal/EditSelfInfo", () => ({ default: React.forwardRef(() => null) }));
vi.mock("@/pages/common/UserCardModal/SendRequest", () => ({ default: () => null }));
vi.mock("@/components/Twemoji", () => ({ default: ({ children }: any) => children }));
vi.mock("@/components/CacheImage", () => ({ default: () => null }));
vi.mock("@/components/JumpToMessageWrap", () => ({ default: React.forwardRef(({ children }: any, _ref) => children) }));
vi.mock("@/pages/chat/queryChat/SearchMessageDrawer/MessageSearchBar", () => ({ default: React.forwardRef(() => null) }));
// The merge modal uses the actual received renderer; unrelated message chrome is adapted.
vi.mock("@/pages/chat/queryChat/MessageItem", async () => {
  const { default: Card } = await import("@/pages/chat/queryChat/MessageItem/CardMessageRenderer");
  return { default: (props: any) => <Card {...props} /> };
});
vi.mock("@/pages/chat/queryChat/MessageItem/CatchMsgRenderer", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/MessageItem/CustomMessageSwitcher", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/MessageItem/FileMessageRenderer", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/MessageItem/LocationMessageRenderer", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/MessageItem/MediaMessageRender", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/MessageItem/TextMessageRender", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/MessageItem/VoiceMessageRender", () => ({ default: () => null }));
import Card from "@/pages/chat/queryChat/MessageItem/CardMessageRenderer";
import ReceivedCard, { MessageCardSourceContext } from "@/components/ReceivedMessageCard";
import Quote from "@/pages/chat/queryChat/MessageItem/QuoteMessageRenderer";
import Merge from "@/pages/chat/queryChat/MessageItem/MergeMessageRenderer";
import MergePreview from "@/pages/common/MergePreviewModal";
import UserCard from "@/pages/common/UserCardModal";
import { NomalMessageItem } from "@/pages/chat/queryChat/SearchMessageDrawer/NomalMessagePane";
import { messageHistoryFence, messagePrivacyLedger, forgetMessage } from "@/core/messagePrivacyRuntime";
let host: HTMLDivElement, root: Root;
const now = 1800000000000;
function wire(extra: any = {}): any { return { clientMsgID: "card", contentType: 108, senderNickname: "Sender", sendTime: now, cardElem: { userID: "703", nickname: "联系人", faceURL: "https://attacker.synthetic.test/private", ex: "{bad" }, ...extra }; }
const render = async (node: React.ReactNode) => { await act(async () => root.render(node)); };
const button = () => host.querySelector<HTMLButtonElement>('button[aria-label^="查看名片"]');
async function clickCard() { expect(button()).not.toBeNull(); await act(async () => button()!.click()); }
function deferred() { let resolve!: (v: any) => void; const promise = new Promise<any>(r => { resolve = r; }); return { promise, resolve }; }
function sourceFromLastClick() { return f.emit.mock.calls.at(-1)![1].sourceCurrent as () => boolean; }
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); vi.clearAllMocks(); vi.useFakeTimers(); vi.setSystemTime(now);
  f.nav++; f.cid = "source"; f.groupID = undefined; f.group = f.member = undefined; f.session.generation++; f.session.current = { issuer: "https://synthetic.test", tenant: { serverID: "A" }, userID: "owner" };
  messagePrivacyLedger.clear(); messageHistoryFence.invalidate();
  f.sdk.mockResolvedValue({ data: [{ publicInfo: { userID: "703", nickname: "Verified" } }] });
  f.business.mockResolvedValue({ data: { users: [{ userID: "703", nickname: "Verified", allowAddFriend: 1 }] } });
  f.emit.mockImplementation(() => undefined); vi.stubGlobal("fetch", f.fetch);
  Object.defineProperty(window, "electronAPI", { configurable: true, value: { fileExists: () => false } });
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.useRealTimers(); vi.unstubAllGlobals(); });

describe("received cards and original-message provenance", () => {
  it("ordinary renderer never constructs an image or desktop download from received faceURL", async () => {
    await render(<Card message={wire()} conversationID="source" isSender={false} />);
    expect(button()?.tagName).toBe("BUTTON"); expect(button()?.type).toBe("button");
    expect(button()?.style.minHeight).toBe("44px"); expect(button()?.style.minWidth).toBe("44px");
    expect(host.querySelector("img")).toBeNull(); expect(f.download).not.toHaveBeenCalled(); expect(f.fetch).not.toHaveBeenCalled();
    await clickCard(); expect(f.emit.mock.calls[0]).toEqual(["OPEN_USER_CARD", { userID: "703", groupID: undefined, notAdd: false, sourceCurrent: expect.any(Function), sourceCanShare: expect.any(Function), sourceSubscribe: expect.any(Function) }]);
    expect(sourceFromLastClick()()).toBe(true); expect(f.emit.mock.calls[0][1].sourceCanShare()).toBe(true); // Read scope has no send permission at all.
  });
  it("actual search row uses the same safe renderer, preserving exact ID", async () => {
    await render(<NomalMessageItem message={wire()} conversationID="source" closeOverlay={vi.fn()} />);
    await clickCard(); expect(f.emit.mock.calls[0][1].userID).toBe("703");
    expect(host.querySelector('img[src^="https://attacker"]')).toBeNull(); expect(f.download).not.toHaveBeenCalled();
  });
  it("quoted card discards the URL and rejects malformed cardElem without formatter crash", async () => {
    const quoted = wire(); const outer = wire({ clientMsgID: "outer", contentType: 114, quoteElem: { text: "reply", quoteMessage: quoted } });
    await render(<Quote message={outer} conversationID="source" isSender={false} />);
    expect(host.querySelector("img")).toBeNull(); await clickCard(); const current = sourceFromLastClick();
    await act(async () => forgetMessage("source", "outer")); expect(current()).toBe(false);
    await render(<Quote key="malformed" message={wire({ contentType: 114, quoteElem: { quoteMessage: wire({ cardElem: undefined }) } })} conversationID="source" isSender={false} />);
    expect(button()).toBeNull(); expect(host.textContent).toContain("名片无效"); expect(f.download).not.toHaveBeenCalled();
  });
  it("invalid cards show only a safe placeholder with no clickable target", async () => {
    await render(<Card message={wire({ cardElem: { userID: "703", nickname: "unsafe\u202e" } })} conversationID="source" isSender={false} />);
    expect(host.textContent).not.toContain("unsafe"); expect(button()).toBeNull(); expect(f.emit).not.toHaveBeenCalled();
  });
  it("renders URL-like nickname as inert text in ordinary and quoted cards", async () => {
    const nickname = "https://name.synthetic.test/?q=<literal>";
    const card = wire({ cardElem: { userID: "https:703", nickname, faceURL: "file:///synthetic", ex: "<script>" } });
    await render(<Card message={card} conversationID="source" isSender={false} />);
    expect(host.textContent).toContain(nickname); expect(host.querySelector("a, img")).toBeNull(); await clickCard();
    expect(f.emit.mock.calls[0][1].userID).toBe("https:703");
    await render(<Quote message={wire({ contentType: 114, quoteElem: { quoteMessage: card } })} conversationID="source" isSender={false} />);
    expect(host.textContent).toContain(nickname); expect(host.querySelector("a, img, literal")).toBeNull();
    expect(f.fetch).not.toHaveBeenCalled(); expect(f.download).not.toHaveBeenCalled();
  });
  it("retained source callback rejects account/issuer ABA, without needing a rerender", async () => {
    await render(<Card message={wire()} conversationID="source" isSender={false} />); await clickCard(); const current = sourceFromLastClick();
    f.session.current = { ...f.session.current, issuer: "https://new.synthetic.test" }; f.session.generation += 2;
    expect(current()).toBe(false); f.session.current = { ...f.session.current, issuer: "https://synthetic.test" }; expect(current()).toBe(false);
  });
  it("retained source callback rejects navigation A to B to A and original-history clear", async () => {
    await render(<Card message={wire()} conversationID="source" isSender={false} />); await clickCard(); const current = sourceFromLastClick();
    f.cid = "other"; f.nav++; f.cid = "source"; f.nav++; expect(current()).toBe(false);
    await render(<Card key="fresh" message={wire()} conversationID="source" isSender={false} />); await clickCard(); const fresh = sourceFromLastClick();
    await act(async () => messageHistoryFence.invalidate()); expect(fresh()).toBe(false);
  });
  it("rejects a retained identity after CID props change, message mutation or unmount", async () => {
    const message = wire(); await render(<Card message={message} conversationID="source" isSender={false} />); await clickCard(); const first = sourceFromLastClick();
    message.cardElem.userID = "704"; expect(first()).toBe(false);
    await render(<Card message={wire()} conversationID="other" isSender={false} />); expect(first()).toBe(false); expect(button()).toBeNull();
    await render(<Card key="fresh" message={wire()} conversationID="source" isSender={false} />); await clickCard(); const live = sourceFromLastClick();
    await render(null); expect(live()).toBe(false);
  });
  it("expires a private card at its original read time without retaining a click grant", async () => {
    await render(<Card message={wire({ attachedInfoElem: { isPrivateChat: true, burnDuration: 1, hasReadTime: now } })} conversationID="source" isSender={false} />);
    await clickCard(); const current = sourceFromLastClick(); await act(async () => vi.advanceTimersByTimeAsync(1100));
    expect(current()).toBe(false); expect(button()).toBeNull();
  });
  it("actual merge renderer sends a fixed original CID and retention source to its host", async () => {
    const show = vi.fn(); const message = wire({ contentType: 107, mergeElem: { title: "记录", abstractList: [], multiMessage: [wire()] } });
    await render(<Merge message={message} conversationID="source" isSender={false} showMergeModal={show} />);
    await act(async () => host.firstElementChild!.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect(show).toHaveBeenCalledOnce(); expect(show.mock.calls[0][0]).toBe(message.mergeElem); expect(show.mock.calls[0][1].conversationID).toBe("source");
    await act(async () => messageHistoryFence.invalidate()); expect(show.mock.calls[0][1].current()).toBe(false);
  });
  it("merge close/reopen invalidates an old child card while a fresh open remains readable", async () => {
    const ref = createRef<OverlayVisibleHandle>(); let allowed = true;
    const source = { conversationID: "source", current: () => allowed, canShare: () => allowed };
    const data = { title: "合并记录", abstractList: [], multiMessage: [wire()] } as any;
    await render(<MergePreview ref={ref} mergeData={data} source={source} />); await act(async () => ref.current!.openOverlay()); await clickCard();
    const old = sourceFromLastClick(); await act(async () => ref.current!.closeOverlay()); expect(old()).toBe(false);
    await act(async () => ref.current!.openOverlay()); await clickCard(); const fresh = sourceFromLastClick();
    expect(old()).toBe(false); expect(fresh()).toBe(true); allowed = false; expect(fresh()).toBe(false);
  });
});

function ProfileHost({ message, showSource = true }: { message: any; showSource?: boolean }) {
  const ref = useRef<OverlayVisibleHandle>(null); const [params, setParams] = useState<any>();
  f.emit.mockImplementation((kind, value) => { if (kind === "OPEN_USER_CARD") setParams(value); });
  useEffect(() => { if (params) ref.current!.openOverlay(); }, [params]);
  return <>{showSource && <Card message={message} conversationID="source" isSender={false} />}<UserCard ref={ref} {...params} /></>;
}
describe("actual exact-ID profile resolver keeps message provenance", () => {
  it("explicit valid click hydrates exact SDK and business identity, never received faceURL", async () => {
    await render(<ProfileHost message={wire()} />); expect(f.sdk).not.toHaveBeenCalled(); await clickCard();
    expect(f.sdk).toHaveBeenCalledExactlyOnceWith({ userIDList: ["703"], groupID: undefined });
    expect(f.business).toHaveBeenCalledExactlyOnceWith(["703"]); expect(host.textContent).toContain("Verified");
    expect(host.querySelector('img[src^="https://attacker"]')).toBeNull(); expect(f.download).not.toHaveBeenCalled();
    await act(async () => vi.advanceTimersByTimeAsync(750));
    expect(host.querySelector('[aria-label="profile"]')).not.toBeNull(); expect(f.sdk).toHaveBeenCalledOnce();
  });
  it("does not continue late SDK hydration after original history was cleared", async () => {
    const old = deferred(); f.sdk.mockReturnValueOnce(old.promise); await render(<ProfileHost message={wire()} />); await clickCard();
    await act(async () => messageHistoryFence.invalidate()); expect(host.querySelector('[aria-label="profile"]')).toBeNull(); await act(async () => old.resolve({ data: [{ publicInfo: { userID: "703", nickname: "Late" } }] }));
    expect(f.business).not.toHaveBeenCalled(); expect(host.textContent).not.toContain("Late");
    await act(async () => vi.advanceTimersByTimeAsync(250)); expect(host.querySelector('[aria-label="profile"]')).toBeNull();
  });
  it("automatically closes an already hydrated panel when the source row unmounts", async () => {
    const message = wire(); await render(<ProfileHost message={message} />); await clickCard(); expect(host.querySelector('[aria-label="profile"]')).not.toBeNull();
    await render(<ProfileHost message={message} showSource={false} />); await act(async () => vi.advanceTimersByTimeAsync(250));
    expect(host.querySelector('[aria-label="profile"]')).toBeNull(); expect(f.toChat).not.toHaveBeenCalled();
  });
  it("drops a stale business response and closes on a navigation ABA", async () => {
    const old = deferred(); f.business.mockReturnValueOnce(old.promise); await render(<ProfileHost message={wire()} />); await clickCard();
    f.nav += 2; await act(async () => old.resolve({ data: { users: [{ userID: "703", nickname: "Late policy", allowAddFriend: 1 }] } }));
    await act(async () => vi.advanceTimersByTimeAsync(250)); expect(host.querySelector('[aria-label="profile"]')).toBeNull(); expect(host.textContent).not.toContain("Late policy");
  });
  it("preserves normal non-message exact-ID profile behavior without a source callback", async () => {
    const ref = createRef<OverlayVisibleHandle>(); await render(<UserCard ref={ref} userID="703" />); await act(async () => ref.current!.openOverlay());
    expect(host.textContent).toContain("Verified"); await act(async () => messageHistoryFence.invalidate()); await act(async () => vi.advanceTimersByTimeAsync(250));
    expect(host.querySelector('[aria-label="profile"]')).not.toBeNull(); expect(f.sdk).toHaveBeenCalledOnce();
  });
});


describe("view and retention remain distinct for private card sources", () => {
  it("reads a live private card profile but cannot emit a share or create a new card", async () => {
    const privateCard = wire({ attachedInfoElem: { isPrivateChat: true, burnDuration: 30, hasReadTime: now } });
    await render(<ProfileHost message={privateCard} />); await clickCard();
    const event = f.emit.mock.calls[0][1]; expect(event.sourceCurrent()).toBe(true); expect(event.sourceCanShare()).toBe(false);
    expect(host.textContent).toContain("Verified"); expect(host.querySelector('[aria-label="profile"]')).not.toBeNull();
    const share = host.querySelector<HTMLButtonElement>('button[aria-label="placeholder.share"]'); expect(share).not.toBeNull(); expect(share!.disabled).toBe(true);
    await act(async () => share!.click()); expect(f.emit.mock.calls.filter(([kind]) => kind === "OPEN_CHOOSE_MODAL")).toHaveLength(0);
    expect(f.createCard).not.toHaveBeenCalled();
  });
  it("retention includes original root and inherited merge sources, without revoking view", async () => {
    const privateRoot = wire({ clientMsgID: "private-root", attachedInfoElem: { isPrivateChat: true } });
    for (const kind of ["root", "inherited"] as const) {
      await render(<MessageCardSourceContext.Provider value={{ conversationID: "source", current: () => true, canShare: () => kind !== "inherited" }}>
        <ReceivedCard key={kind} message={wire()} conversationID="source" rootMessage={kind === "root" ? privateRoot : undefined} />
      </MessageCardSourceContext.Provider>);
      await clickCard(); const event = f.emit.mock.calls.at(-1)![1]; expect(event.sourceCurrent()).toBe(true); expect(event.sourceCanShare()).toBe(false);
    }
  });
});

function enterGroup(look = 0, apply = 0) {
  f.groupID = "original-group";
  f.group = { groupID: f.groupID, status: 0, lookMemberInfo: look, applyMemberFriend: apply };
  f.member = { groupID: f.groupID, userID: "owner", roleLevel: 20, muteEndTime: now + 999999 };
}
describe("original group policy for received cards", () => {
  it("restricted group profile is not hydrated, including a spoofed payload groupID", async () => {
    enterGroup(1); await render(<ProfileHost message={wire({ groupID: "attacker-group" })} />);
    expect(button()).toBeNull(); expect(f.sdk).not.toHaveBeenCalled(); expect(f.business).not.toHaveBeenCalled();
  });
  it("known group source passes its original groupID and prevents friend application", async () => {
    enterGroup(0, 1); await render(<ProfileHost message={wire({ groupID: "attacker-group" })} />); await clickCard();
    expect(f.sdk).toHaveBeenCalledExactlyOnceWith({ userIDList: ["703"], groupID: "original-group" });
    expect(f.emit.mock.calls[0][1]).toEqual(expect.objectContaining({ groupID: "original-group", notAdd: true }));
    expect([...host.querySelectorAll("button")].some(x => x.textContent === "placeholder.addFriends")).toBe(false);
  });
  it("does not borrow foreign group metadata and becomes readable after the existing source resolves", async () => {
    enterGroup(); f.group = { ...f.group, groupID: "foreign-group" }; const message = wire();
    await render(<Card message={message} conversationID="source" isSender={false} />); expect(button()).toBeNull();
    f.group = { ...f.group, groupID: "original-group" }; await act(async () => vi.advanceTimersByTimeAsync(250));
    await clickCard(); expect(f.emit.mock.calls[0][1].groupID).toBe("original-group");
  });
  it("a late permission change closes the opened panel and invalidates its retained source", async () => {
    enterGroup(); await render(<ProfileHost message={wire()} />); await clickCard(); const current = sourceFromLastClick();
    expect(host.querySelector('[aria-label="profile"]')).not.toBeNull(); f.group = { ...f.group, applyMemberFriend: 1 };
    expect(current()).toBe(false); await act(async () => vi.advanceTimersByTimeAsync(250)); expect(host.querySelector('[aria-label="profile"]')).toBeNull();
  });
});
