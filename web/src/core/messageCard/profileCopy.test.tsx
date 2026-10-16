// @vitest-environment jsdom
// Actual profile and received-card/source guards; only transport, clipboard and UI chrome are adapted.
import React, { act, createRef, useEffect, useRef, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { OverlayVisibleHandle } from "@/hooks/useOverlayVisible";
const f = vi.hoisted(() => ({
  nav: 0, session: { generation: 1, current: { userID: "owner", issuer: "https://synthetic.test", tenant: { serverID: "A" } } },
  listeners: new Set<() => void>(), emit: vi.fn(), sdk: vi.fn(), business: vi.fn(), copy: vi.fn(), feedback: vi.fn(), toChat: vi.fn(),
}));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: {
  get current() { return f.session.current; }, get generation() { return f.session.generation; },
  onChange: (callback: () => void) => { f.listeners.add(callback); return () => { f.listeners.delete(callback); }; },
} }));
vi.mock("@/store", () => {
  const state = { selfInfo: { userID: "owner", nickname: "Self" }, appConfig: { allowSendMsgNotFriend: 1 },
    currentGroupInfo: undefined, currentMemberInGroup: undefined,
    captureConversationViewScope: () => { const turn = f.nav; return { conversationID: "source", groupID: undefined, current: () => turn === f.nav }; } };
  const hook = Object.assign((select: any) => select(state), { getState: () => state, subscribe: () => () => {} });
  return { useUserStore: hook, useMessageStore: hook, useConversationStore: hook };
});
vi.mock("@/layout/MainContentWrap", () => ({ IMSDK: { getUsersInfoWithCache: f.sdk, on: vi.fn(), off: vi.fn() } }));
vi.mock("@/api/login", () => ({ BusinessAllowType: { Allow: 1, NotAllow: 2 }, getBusinessUserInfo: f.business }));
vi.mock("@/utils/events", () => ({ default: { emit: f.emit } }));
vi.mock("@/utils/common", () => ({ feedbackToast: f.feedback }));
vi.mock("react-use", () => ({ useCopyToClipboard: () => [null, f.copy] }));
vi.mock("@/hooks/useConversationToggle", () => ({ useConversationToggle: () => ({ toSpecifiedConversation: f.toChat }) }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("open-im-sdk-wasm", () => ({ AllowType: { Allowed: 0, NotAllowed: 1 }, SessionType: { Single: 1 },
  CbEvents: { OnFriendAdded: "friend" }, GroupJoinSource: { Invitation: 2, QrCode: 3 } }));
vi.mock("antd", () => ({
  Button: ({ children, onClick, disabled }: any) => <button onClick={onClick} disabled={disabled}>{children}</button>,
  Divider: () => null, Spin: ({ children }: any) => children, Modal: () => null,
  Tooltip: ({ children }: any) => <span>{children}</span>, Space: ({ children }: any) => <span>{children}</span>, Popover: ({ children }: any) => <span>{children}</span>,
}));
vi.mock("@/AntdGlobalComp", () => ({ message: { warning: vi.fn() }, modal: { confirm: vi.fn() } }));
vi.mock("@/components/DraggableModalWrap", () => ({ default: ({ open, children }: any) => open ? <section aria-label="profile">{children}</section> : null }));
vi.mock("@/components/OIMAvatar", () => ({ default: () => <span aria-label="avatar" /> }));
vi.mock("@/components/EditableContent", () => ({ default: ({ value, textClassName }: any) => <span className={textClassName}>{value}</span> }));
vi.mock("@/components/UserLoginInfo", () => ({ default: () => null }));
vi.mock("@/components/UserLoginRecordList", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/ChatFooter/SendActionBar/CallPopContent", () => ({ default: () => null }));
vi.mock("@/pages/common/UserCardModal/EditSelfInfo", () => ({ default: React.forwardRef(function Edit(_props, _ref) { return null; }) }));
vi.mock("@/pages/common/UserCardModal/SendRequest", () => ({ default: () => null }));

import Card from "@/pages/chat/queryChat/MessageItem/CardMessageRenderer";
import UserCard from "@/pages/common/UserCardModal";
import { messageHistoryFence, messagePrivacyLedger } from "@/core/messagePrivacyRuntime";
let host: HTMLDivElement, root: Root;
const now = 1800000000000;
const wire = (privateSource = false) => ({ clientMsgID: "copy-source", contentType: 108, sendTime: now,
  cardElem: { userID: "703", nickname: "联系人" },
  ...(privateSource ? { attachedInfoElem: { isPrivateChat: true, burnDuration: 30, hasReadTime: now } } : {}) } as any);
const render = async (node: React.ReactNode) => { await act(async () => root.render(node)); };
const copyButton = () => host.querySelector<HTMLButtonElement>('button[aria-label="placeholder.copy ID: 703"]')!;
async function openDirect(props: Record<string, unknown> = {}) {
  const ref = createRef<OverlayVisibleHandle>(); await render(<UserCard ref={ref} userID="703" {...props} />);
  await act(async () => ref.current!.openOverlay()); expect(host.textContent).toContain("Verified"); return ref;
}
function ProfileHost({ message }: { message: any }) {
  const ref = useRef<OverlayVisibleHandle>(null); const [params, setParams] = useState<any>();
  f.emit.mockImplementation((kind, value) => { if (kind === "OPEN_USER_CARD") setParams(value); });
  useEffect(() => { if (params) ref.current!.openOverlay(); }, [params]);
  return <><Card message={message} conversationID="source" isSender={false} /><UserCard ref={ref} {...params} /></>;
}
async function openSource(privateSource = false) {
  await render(<ProfileHost message={wire(privateSource)} />);
  const button = host.querySelector<HTMLButtonElement>('button[aria-label^="查看名片"]')!;
  expect(button).not.toBeNull(); await act(async () => button.click()); expect(host.textContent).toContain("Verified");
}
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); vi.clearAllMocks(); vi.useFakeTimers(); vi.setSystemTime(now);
  f.nav++; f.session.generation++; f.session.current = { userID: "owner", issuer: "https://synthetic.test", tenant: { serverID: "A" } };
  messagePrivacyLedger.clear(); messageHistoryFence.invalidate();
  f.sdk.mockResolvedValue({ data: [{ publicInfo: { userID: "703", nickname: "Verified" } }] });
  f.business.mockResolvedValue({ data: { users: [{ userID: "703", nickname: "Verified", allowAddFriend: 1 }] } });
  f.toChat.mockResolvedValue(false); f.emit.mockImplementation(() => undefined);
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.useRealTimers(); });

describe("profile ID copying preserves original-message retention", () => {
  it("keeps ordinary contact copying as a labelled 44px native keyboard button with header room", async () => {
    await openDirect(); const button = copyButton();
    expect(button).not.toBeNull(); expect(button.tagName).toBe("BUTTON"); expect(button.type).toBe("button"); expect(button.tabIndex).toBe(0);
    expect(button.getAttribute("aria-label")).toBe("placeholder.copy ID: 703");
    expect(button.style.minHeight).toBe("44px"); expect(button.style.minWidth).toBe("44px"); expect(button.disabled).toBe(false);
    expect(button.parentElement!.parentElement!.className).toContain("min-h-[80px]");
    await act(async () => button.click()); expect(f.copy).toHaveBeenCalledExactlyOnceWith("703");
    expect(f.feedback).toHaveBeenCalledExactlyOnceWith({ msg: "toast.copySuccess" });
    expect(host.querySelector('[aria-label="profile"] .select-text')).not.toBeNull();
  });
  it("keeps a private source readable and its view actions usable while disabling copy and text selection", async () => {
    await openSource(true); const button = copyButton();
    expect(button.disabled).toBe(true); await act(async () => button.click()); expect(f.copy).not.toHaveBeenCalled(); expect(f.feedback).not.toHaveBeenCalled();
    const profile = host.querySelector<HTMLElement>('[aria-label="profile"]')!;
    expect(profile.textContent).toContain("Verified"); expect(profile.querySelector(".select-text")).toBeNull();
    expect(profile.querySelector<HTMLElement>('[style*="user-select"]')?.style.userSelect).toBe("none");
    const send = [...profile.querySelectorAll("button")].find(item => item.textContent === "placeholder.sendMessage")!;
    expect(send.disabled).toBe(false); await act(async () => send.click()); expect(f.toChat).toHaveBeenCalledOnce();
  });
  it("rechecks late retention denial at dispatch before the existing refresh makes the button disabled", async () => {
    let retain = true; await openDirect({ sourceCurrent: () => true, sourceCanShare: () => retain });
    const button = copyButton(); expect(button.disabled).toBe(false); retain = false;
    await act(async () => button.click()); expect(f.copy).not.toHaveBeenCalled(); expect(f.feedback).not.toHaveBeenCalled();
    await act(async () => vi.advanceTimersByTimeAsync(250)); expect(copyButton().disabled).toBe(true);
    expect(host.querySelector('[aria-label="profile"] .select-text')).toBeNull();
  });
  it("rejects an enabled source button synchronously when the original history is cleared", async () => {
    await openSource(); const button = copyButton(); expect(button.disabled).toBe(false);
    await act(async () => { messageHistoryFence.invalidate(); button.click(); });
    expect(f.copy).not.toHaveBeenCalled(); expect(f.feedback).not.toHaveBeenCalled();
  });
  it("rejects an old owner's enabled button before session notifications close the panel", async () => {
    await openSource(); const button = copyButton(); expect(button.disabled).toBe(false);
    await act(async () => {
      f.session.current = { userID: "other", issuer: "https://other.synthetic.test", tenant: { serverID: "B" } }; f.session.generation++;
      button.click(); for (const listener of [...f.listeners]) listener();
    });
    expect(f.copy).not.toHaveBeenCalled(); expect(f.feedback).not.toHaveBeenCalled();
  });
});
