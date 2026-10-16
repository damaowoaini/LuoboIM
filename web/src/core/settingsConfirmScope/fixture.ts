import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { vi, expect } from "vitest";
const f = vi.hoisted(() => {
  const owner = () => ({ userID: "actor", issuer: "issuer-A", tenant: { serverID: "A" } });
  const session = { current: owner() as ReturnType<typeof owner> | undefined, generation: 1, onChange: vi.fn(() => () => {}),
    guard(callback: (...args: any[]) => any) { const generation = session.generation;
      return (...args: any[]) => generation === session.generation ? callback(...args) : undefined; } };
  const sdk = { on: vi.fn(), off: vi.fn(), login: vi.fn(async () => ({})), logout: vi.fn(async () => ({})),
    getSpecifiedGroupsInfo: vi.fn(), getSpecifiedGroupMembersInfo: vi.fn(),
    changeGroupMute: vi.fn(), dismissGroup: vi.fn(), quitGroup: vi.fn(), clearConversationAndDeleteAllMsg: vi.fn(), setGroupInfo: vi.fn(),
    getAdvancedHistoryMessageList: vi.fn() };
  return { owner, session, sdk, user: { selfInfo: { userID: "actor" } }, confirmations: [] as any[],
    close: vi.fn(), toast: vi.fn(), extraClear: vi.fn(), api: undefined as any,
    drawerClose: undefined as (() => void) | undefined, drawerOpen: false };
});
export { f };
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: f.session }));
vi.mock("@/layout/MainContentWrap", async () => {
  const { bindSessionSdk } = await import("@/core/session/sdk");
  const { bindMessageHistory } = await import("@/core/messageHistory");
  const { messageHistoryFence, messageOwner } = await import("@/core/messagePrivacyRuntime");
  return { IMSDK: bindMessageHistory(bindSessionSdk(f.sdk, f.session as any), messageHistoryFence, messageOwner) };
});
vi.mock("@/store/user", () => ({ useUserStore: Object.assign((select: any) => select(f.user), { getState: () => f.user }) }));
vi.mock("@/store/contact", () => ({ useContactStore: { getState: () => ({ friendList: [] }) } }));
vi.mock("@/store/message", async () => {
  const { create } = await import("zustand");
  return { useMessageStore: create(() => ({ clearHistoryMessage: f.extraClear, updateJumpClientMsgID: () => {} })) };
});
vi.mock("@/store", async () => ({ ...(await import("@/store/conversation")), ...(await import("@/store/user")), ...(await import("@/store/message")) }));
vi.mock("@/utils/imCommon", () => ({ conversationSort: (items: unknown) => items, isGroupSession: (type: number) => type === 3 }));
vi.mock("@/utils/common", () => ({ feedbackToast: f.toast }));
vi.mock("@/utils/events", () => ({ default: { emit: vi.fn() } }));
vi.mock("@/AntdGlobalComp", () => ({ modal: { confirm: (options: any) => f.confirmations.push(options) } }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("open-im-sdk-wasm", () => ({ GroupStatus: { Nomal: 0, Baned: 1, Dismissed: 2, Muted: 3 },
  GroupMemberRole: { Nomal: 20, Admin: 60, Owner: 100 }, MessageType: { AtTextMessage: 106, QuoteMessage: 114 },
  AllowType: { Allowed: 0, NotAllowed: 1 }, GroupVerificationType: { AllNot: 2 },
  MessageReceiveOptType: { Nomal: 0, NotNotify: 2 } }));
vi.mock("antd", () => ({ App: { useApp: () => ({ modal: { confirm: (options: any) => f.confirmations.push(options) } }) },
  Drawer: ({ children, onClose, open }: any) => { f.drawerClose = onClose; f.drawerOpen = open; return React.createElement("div", null, children); },
  Button: ({ children, onClick }: any) => React.createElement("button", { onClick }, children),
  Upload: ({ children }: any) => React.createElement("div", null, children),
  Popover: ({ children }: any) => React.createElement(React.Fragment, null, children), Divider: () => null,
}));
vi.mock("@ant-design/icons", () => ({ RightOutlined: () => null }));
vi.mock("react-use", () => ({ useCopyToClipboard: () => [{}, vi.fn()] }));
vi.mock("uuid", () => ({ v4: () => "unused-upload-intent" }));
vi.mock("@/components/OIMAvatar", () => ({ default: () => null }));
vi.mock("@/components/EditableContent", () => ({ default: () => null }));
vi.mock("@/components/SettingRow", () => ({ default: ({ title, rowClick, tryChange, value, hidden, children }: any) =>
  hidden ? null : React.createElement("div", null, React.createElement("button", { onClick: rowClick ?? (() => tryChange?.(!value)) }, title), children) }));
vi.mock("@/pages/chat/queryChat/GroupSetting/GroupMemberRow", () => ({ default: ({ updateTravel }: any) => React.createElement("button", { onClick: updateTravel }, "members") }));
vi.mock("@/pages/chat/queryChat/GroupSetting/MemberPermissionSelectContent", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/GroupSetting/VerificationSelectContent", () => ({ default: () => null, verificationMenuList: [] }));
vi.mock("@/pages/chat/queryChat/MsgDestructSetting", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/GroupSetting/GroupMemberList", () => ({ default: React.forwardRef(() => React.createElement("div", null, "member-list-boundary")) }));
vi.mock("@/pages/chat/queryChat/GroupSetting/GroupMemberListHeader", () => ({ default: ({ back2Settings }: any) => React.createElement("button", { onClick: back2Settings }, "settings") }));

import { useConversationStore } from "@/store/conversation";
import { messageHistoryFence as realMessageHistoryFence, captureMessageAccess as realCaptureMessageAccess } from "@/core/messagePrivacyRuntime";
export const messageHistoryFence = realMessageHistoryFence;
export const captureMessageAccess = realCaptureMessageAccess;
export const store = useConversationStore;
export const group = (groupID = "A", status = 0) => ({ groupID, status, needVerification: 0, groupName: groupID } as any);
export const member = (roleLevel = 100, patch: any = {}) => ({ groupID: "A", userID: "actor", roleLevel, muteEndTime: 0, ...patch } as any);
export const conversation = (name = "A", kind: "group" | "user" = "group") => ({ conversationID: `cid-${kind}-${name}`,
  conversationType: kind === "group" ? 3 : 1, groupID: kind === "group" ? name : "", userID: kind === "user" ? name : "" } as any);
export const flush = async () => { for (let n = 0; n < 12; n++) await Promise.resolve(); };
export async function enter(name = "A", kind: "group" | "user" = "group") { store.getState().updateCurrentConversation(conversation(name, kind), true); await flush(); }
export function deferred() { let resolve!: (value: any) => void, reject!: (error: unknown) => void;
  const promise = new Promise<any>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
export async function reset() {
  vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  f.session.current = f.owner(); f.session.generation++; f.user.selfInfo.userID = "actor";
  f.confirmations.length = 0; f.drawerClose = undefined; f.drawerOpen = false;
  f.sdk.getSpecifiedGroupsInfo.mockImplementation(async ([id]: string[]) => ({ data: [group(id)] }));
  f.sdk.getSpecifiedGroupMembersInfo.mockImplementation(async ({ groupID }: any) => ({ data: [member(100, { groupID })] }));
  for (const key of ["changeGroupMute", "dismissGroup", "quitGroup", "clearConversationAndDeleteAllMsg", "setGroupInfo"] as const) f.sdk[key].mockResolvedValue({ data: {} });
  f.extraClear.mockImplementation(() => messageHistoryFence.invalidate());
  messageHistoryFence.invalidate(); store.getState().clearConversationStore(); await enter();
}
export function onOk() { expect(f.confirmations).toHaveLength(1); expect(f.confirmations[0].onOk).toBeTypeOf("function"); return f.confirmations[0].onOk as () => Promise<unknown>; }
export function surface() { const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); let disposed = false;
  return { host, render: async (node: React.ReactNode) => { await act(async () => { root.render(node); await flush(); }); },
    dispose: async () => { if (!disposed) { await act(async () => root.unmount()); disposed = true; host.remove(); } } }; }
export async function click(host: HTMLElement, label: string) { const button = [...host.querySelectorAll("button")].find((b) => b.textContent === label);
  expect(button).toBeDefined(); await act(async () => { button!.click(); await flush(); }); }
