import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { vi, expect } from "vitest";
const f = vi.hoisted(() => {
  const listeners = new Map<string, Set<Function>>();
  const session = { current: undefined as any, generation: 1, onChange: vi.fn(() => () => {}),
    guard(callback: (...args: any[]) => any) { const generation = session.generation;
      return (...args: any[]) => generation === session.generation ? callback(...args) : undefined; } };
  return { session, user: { selfInfo: { userID: "actor" } }, friends: [] as any[], rows: [] as any[],
    list: vi.fn(), search: vi.fn(), specified: vi.fn(), groups: vi.fn(), kick: vi.fn(), mute: vi.fn(), admin: vi.fn(),
    confirm: vi.fn(), toast: vi.fn(), profile: vi.fn(), listeners,
    actions: new Map<string, () => any>(), saves: [] as Array<() => Promise<any>>, profileAction: undefined as (() => void) | undefined,
    drawerClose: undefined as (() => void) | undefined, hook: undefined as any,
  };
});
export { f };
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: f.session }));
vi.mock("@/core/messagePrivacyRuntime", () => ({ messageOwner: () => "fixture", captureMessageAccess: () => ({ current: () => true }) }));
vi.mock("@/layout/MainContentWrap", async () => {
  const { bindSessionSdk } = await import("@/core/session/sdk");
  return { IMSDK: bindSessionSdk({ getGroupMemberList: f.list, searchGroupMembers: f.search,
    getSpecifiedGroupMembersInfo: f.specified, getSpecifiedGroupsInfo: f.groups,
    kickGroupMember: f.kick, changeGroupMemberMute: f.mute, setGroupMemberInfo: f.admin,
    on: (event: string, handler: Function) => { const set = f.listeners.get(event) ?? new Set(); set.add(handler); f.listeners.set(event, set); },
    off: (event: string, handler: Function) => f.listeners.get(event)?.delete(handler),
    login: async () => ({}), logout: async () => ({}),
  }, f.session as any) };
});
vi.mock("@/store/user", () => ({ useUserStore: Object.assign((select: any) => select(f.user), { getState: () => f.user }) }));
vi.mock("@/store/contact", () => ({ useContactStore: Object.assign((select: any) => select({ friendList: f.friends }), { getState: () => ({ friendList: f.friends }) }) }));
vi.mock("@/store/message", () => ({ useMessageStore: { getState: () => ({ updateJumpClientMsgID: () => {} }) } }));
vi.mock("@/store", async () => ({ ...(await import("@/store/conversation")), ...(await import("@/store/user")) }));
vi.mock("@/utils/imCommon", () => ({ conversationSort: (value: unknown) => value, isGroupSession: (type: number) => type === 3 }));
vi.mock("@/utils/common", () => ({ feedbackToast: f.toast }));
vi.mock("@/AntdGlobalComp", () => ({ modal: { confirm: f.confirm } }));
vi.mock("ahooks", () => ({ useRequest: (callback: any) => ({ loading: false, runAsync: (...args: any[]) => callback(...args) }),
  useLatest: (value: any) => { const ref = React.useRef(value); ref.current = value; return ref; } }));
vi.mock("i18next", () => ({ t: (key: string, args?: any) => args?.num === undefined ? key : `${key}:${args.num}` }));
vi.mock("open-im-sdk-wasm", () => ({ GroupMemberRole: { Nomal: 20, Admin: 60, Owner: 100 }, AllowType: { Allowed: 0, NotAllowed: 1 },
  GroupStatus: { Nomal: 0, Baned: 1, Dismissed: 2, Muted: 3 }, MessageType: { AtTextMessage: 106, QuoteMessage: 114 },
  CbEvents: { OnGroupMemberInfoChanged: "changed", OnGroupMemberAdded: "added", OnGroupMemberDeleted: "deleted", OnJoinedGroupAdded: "joined" } }));
vi.mock("@ant-design/icons", () => ({ ArrowLeftOutlined: () => null, CheckOutlined: () => null, CloseOutlined: () => null }));
vi.mock("@/components/OIMAvatar", () => ({ default: () => null }));
// Preserve actual callbacks from real MemberItem/Modal elements. Do not replace
// business components or pretend new props existed in the old implementation.
function findSave(value: any) {
  if (!React.isValidElement(value)) return;
  const props = value.props as any;
  if (props.children === "placeholder.save") f.saves.push(props.onClick);
  React.Children.forEach(props.children, findSave);
}
vi.mock("antd", () => ({
  Spin: ({ children }: any) => {
    const value = children?.props?.children?.[0];
    if (React.isValidElement(value) && typeof (value.props as any).onClick === "function") f.profileAction = (value.props as any).onClick;
    return React.createElement(React.Fragment, null, children);
  },
  Tooltip: ({ title, children }: any) => { f.actions.set(title, children.props.onClick); return React.createElement("div", { "data-tooltip": title }, children); },
  Modal: ({ open, children }: any) => { if (!open) return null; findSave(children); return React.createElement("div", { role: "dialog" }, children); },
  Drawer: ({ onClose, children }: any) => { f.drawerClose = onClose; return React.createElement("div", null, children); }, // Retains children while closing, like exit animation.
  Empty: () => React.createElement("div", null, "empty"),
}));
vi.mock("react-virtuoso", () => ({ Virtuoso: ({ data, itemContent }: any) => React.createElement("div", null,
  data.map((member: any, index: number) => React.createElement("div", { key: member.userID }, itemContent(index, member)))) }));
vi.mock("@/pages/chat/queryChat/GroupSetting/GroupSettings", () => ({ default: ({ updateTravel }: any) =>
  React.createElement("button", { onClick: updateTravel }, "show-members") }));
vi.mock("@/pages/chat/queryChat/GroupSetting/GroupMemberListHeader", () => ({ default: ({ back2Settings }: any) =>
  React.createElement("button", { onClick: back2Settings }, "back-settings") }));

import { useConversationStore } from "@/store/conversation";
export const store = useConversationStore;
export const owner = () => ({ userID: "actor", issuer: "issuer-A", tenant: { serverID: "tenant-A" } });
export const member = (userID = "peer", roleLevel = 20, patch: any = {}) => ({ groupID: "A", userID, roleLevel,
  nickname: userID, faceURL: "", muteEndTime: 0, ...patch });
export const group = (id = "A") => ({ groupID: id, status: 0, lookMemberInfo: 0, applyMemberFriend: 0 });
export const conversation = (id = "A") => ({ conversationID: "cid-" + id, conversationType: 3, groupID: id, userID: "" } as any);
export const flush = async () => { for (let n = 0; n < 8; n++) await Promise.resolve(); };
export async function enter(id = "A") { store.getState().updateCurrentConversation(conversation(id)); await flush(); }
export function emit(name: string, data: any) { for (const handler of f.listeners.get(name) ?? []) handler({ data }); }
export function deferred() { let resolve!: (value: any) => void, reject!: (value: any) => void;
  const promise = new Promise<any>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
export async function reset() {
  vi.resetAllMocks(); f.listeners.clear(); f.actions.clear(); f.saves = []; f.profileAction = undefined; f.drawerClose = undefined;
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  f.session.current = owner(); f.session.generation++; f.user.selfInfo.userID = "actor"; f.rows = [member()]; f.friends = [];
  f.list.mockImplementation(async () => ({ data: f.rows.map((m) => ({ ...m })) }));
  f.search.mockImplementation(async () => ({ data: f.rows.map((m) => ({ ...m })) }));
  f.specified.mockImplementation(async ({ groupID, userIDList }: any) => ({ data: userIDList.map((id: string) => member(id, id === "actor" ? 100 : 20, { groupID })) }));
  f.groups.mockImplementation(async (ids: string[]) => ({ data: ids.map(group) }));
  f.kick.mockResolvedValue({ data: {} }); f.mute.mockResolvedValue({ data: {} }); f.admin.mockResolvedValue({ data: {} });
  store.getState().clearConversationStore(); await enter();
  window.userClick = f.profile;
}
export function action(name: string) { const callback = f.actions.get("placeholder." + name); expect(callback).toBeTypeOf("function"); return callback!; }
export function confirmation() { expect(f.confirm).toHaveBeenCalledOnce(); return f.confirm.mock.calls[0][0].onOk as () => Promise<unknown>; }
export function save() { const callback = f.saves.at(-1); expect(callback).toBeTypeOf("function"); return callback!; }
export async function clickLabel(host: HTMLElement, value: string) {
  const element = [...host.querySelectorAll("span")].find((item) => item.textContent === value);
  expect(element).toBeDefined(); await act(async () => element!.click());
}
export function surface() {
  const host = document.createElement("div"); document.body.append(host); const root: Root = createRoot(host);
  let disposed = false;
  return { host, root, render: async (node: React.ReactNode) => { await act(async () => { root.render(node); await flush(); }); },
    dispose: async () => { if (!disposed) { await act(async () => root.unmount()); disposed = true; host.remove(); } } };
}
