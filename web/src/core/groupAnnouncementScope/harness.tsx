import { vi } from "vitest";

const fixture = vi.hoisted(() => {
  const owner = () => ({ userID: "actor", issuer: "https://fixture.invalid", tenant: { serverID: "A" } });
  return { owner, session: { current: owner(), generation: 1, onChange: vi.fn(() => () => {}), guard: (fn: unknown) => fn },
    sdk: { on: vi.fn(), off: vi.fn(), login: vi.fn(), logout: vi.fn(), setGroupInfo: vi.fn(),
      getSpecifiedGroupsInfo: vi.fn(), getSpecifiedGroupMembersInfo: vi.fn() },
    toast: vi.fn(), groups: {} as Record<string, any>, ui: {} as Record<string, any> };
});
export { fixture };
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: fixture.session }));
vi.mock("@/layout/MainContentWrap", async () => {
  const { bindSessionSdk } = await import("@/core/session/sdk");
  const { bindMessageHistory } = await import("@/core/messageHistory");
  const { messageHistoryFence, messageOwner } = await import("@/core/messagePrivacyRuntime");
  return { IMSDK: bindMessageHistory(bindSessionSdk(fixture.sdk, fixture.session as any), messageHistoryFence, messageOwner) };
});
vi.mock("@/store/user", async () => {
  const { create } = await import("zustand");
  return { useUserStore: create(() => ({ selfInfo: { userID: "actor" }, appSettings: { locale: "zhCN" } })) };
});
vi.mock("@/store/contact", () => ({ useContactStore: { getState: () => ({ friendList: [] }) } }));
vi.mock("@/store/message", () => ({ useMessageStore: { getState: () => ({ updateJumpClientMsgID: vi.fn() }) } }));
vi.mock("@/store", async () => ({ ...(await import("@/store/conversation")), ...(await import("@/store/user")) }));
vi.mock("@/utils/common", () => ({ feedbackToast: fixture.toast, escapeHtml: (v: string) => v,
  sanitizeHtml: (v: string) => v, formatBr: (v: string) => v }));
vi.mock("@/utils/imCommon", () => ({ isGroupSession: (n: number) => n === 3,
  conversationSort: (items: unknown) => items, formatMessageTime: (v: number) => String(v) }));
vi.mock("@/utils/events", () => ({ default: { emit: vi.fn(), on: vi.fn(), off: vi.fn() } }));
vi.mock("i18next", () => ({ t: (key: string) => key, default: { on: vi.fn() } }));
vi.mock("open-im-sdk-wasm", () => ({ MessageType: { AtTextMessage: 106, QuoteMessage: 114 },
  SessionType: { Single: 1, Group: 3, WorkingGroup: 3, Notification: 4 },
  GroupStatus: { Nomal: 0, Baned: 1, Dismissed: 2, Muted: 3 },
  GroupMemberRole: { Nomal: 20, Admin: 60, Owner: 100 },
  GroupAtType: { AtGroupNotice: 4 }, OnlineState: { Offline: 0 }, CbEvents: { OnUserStatusChanged: "status" } }));
vi.mock("antd", async () => {
  const React = await import("react");
  const Drawer = (props: any) => {
    if (props.title === "placeholder.groupAnnouncement") fixture.ui.drawer = props;
    const [everOpened, setEverOpened] = React.useState(Boolean(props.open));
    React.useEffect(() => { if (props.open) setEverOpened(true); props.afterOpenChange?.(Boolean(props.open)); }, [props.open]);
    // Real rc-drawer retains children after the first open unless destroyOnClose.
    return everOpened || props.open ? <section data-drawer={props.title} data-open={String(Boolean(props.open))} hidden={!props.open}>{props.children}</section> : null;
  };
  return { Drawer, Divider: ({ children }: any) => <div>{children}</div>,
    Layout: { Header: ({ children }: any) => <header>{children}</header> },
    Tooltip: ({ title, children }: any) => <span title={title}>{children}</span>,
    message: { success: fixture.toast, error: fixture.toast },
    Button: (props: any) => {
      fixture.ui.action = props.onClick;
      return <button disabled={props.loading} onClick={props.onClick}>{props.children}</button>;
    },
    Input: { TextArea: (props: any) => {
      fixture.ui.change = props.onChange;
      return <textarea aria-label="announcement draft" value={props.value ?? ""} disabled={props.disabled} onChange={props.onChange} />;
    } } };
});
vi.mock("@/components/OIMAvatar", () => ({ default: ({ text }: any) => <span>{text}</span> }));
vi.mock("@/components/UserLoginInfo", () => ({ default: () => null }));
vi.mock("@/components/UserLoginRecordList", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/ChatHeader/GroupAnnouncementCard", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/SingleSetting", async () => {
  const React = await import("react");
  return { default: React.forwardRef((_props, ref) => {
    React.useImperativeHandle(ref, () => ({ isOverlayOpen: false, openOverlay: vi.fn(), closeOverlay: vi.fn() })); return null;
  }) };
});
vi.mock("@/pages/chat/queryChat/GroupSetting", async () => {
  const React = await import("react");
  return { default: React.forwardRef((_props, ref) => {
    React.useImperativeHandle(ref, () => ({ isOverlayOpen: false, openOverlay: vi.fn(), closeOverlay: vi.fn() })); return null;
  }) };
});
vi.mock("@/pages/chat/queryChat/SearchMessageDrawer", async () => {
  const React = await import("react");
  return { default: React.forwardRef((_props, ref) => {
    React.useImperativeHandle(ref, () => ({ isOverlayOpen: false, openOverlay: vi.fn(), closeOverlay: vi.fn() })); return null;
  }) };
});

import { useConversationStore } from "@/store/conversation";
import { useUserStore } from "@/store/user";
import { messageHistoryFence } from "@/core/messagePrivacyRuntime";
export const store = useConversationStore;
export const conversation = (id = "A") => ({ conversationID: `cid-${id}`, conversationType: 3, groupID: id,
  userID: "", showName: id, unreadCount: 0 } as any);
export const group = (id = "A", notification = "") => ({ groupID: id, status: 0, notification,
  notificationUserID: `author-${id}`, notificationUpdateTime: 1, memberCount: 2 } as any);
export const member = (id = "A", userID = "actor", roleLevel = 60) => ({ groupID: id, userID, roleLevel,
  muteEndTime: 0, nickname: userID } as any);
export const enter = (id = "A") => store.getState().updateCurrentConversation(conversation(id), true);
export const flush = async () => { await new Promise(resolve => setTimeout(resolve, 0)); for (let i = 0; i < 12; ++i) await Promise.resolve(); };
export const deferred = <T,>() => { let resolve!: (v: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; };
export function reset() {
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  vi.clearAllMocks(); Object.values(fixture.sdk).forEach(mock => mock.mockReset());
  fixture.session.current = fixture.owner(); fixture.session.generation++;
  useUserStore.setState({ selfInfo: { userID: "actor" } } as any);
  store.getState().clearConversationStore(); messageHistoryFence.invalidate(); fixture.ui = {};
  fixture.groups = { A: group("A"), B: group("B") };
  fixture.sdk.setGroupInfo.mockResolvedValue({ data: {} });
  fixture.sdk.getSpecifiedGroupsInfo.mockImplementation(async ([id]) => ({ data: [fixture.groups[id] ?? group(id)] }));
  fixture.sdk.getSpecifiedGroupMembersInfo.mockImplementation(async ({ groupID, userIDList }) => ({ data: [member(groupID, userIDList[0])] }));
  Object.defineProperty(window, "electronAPI", { configurable: true, value: { ipcInvoke: vi.fn() } });
}
