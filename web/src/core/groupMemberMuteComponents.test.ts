// @vitest-environment jsdom
// Real GroupMemberList -> private MemberItem -> real useOverlayVisible/MuteModal.
// SDK transport, role/list/store boundaries and visual primitives are adapters.
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const f = vi.hoisted(() => ({
  rows: [] as any[], friends: [] as any[], selfID: "actor",
  role: { isAdmin: false, isOwner: true, currentMemberInGroup: { groupID: "group-one" } },
  getMembers: vi.fn(), search: vi.fn(), reset: vi.fn(),
  mute: vi.fn(), setMember: vi.fn(), kick: vi.fn(), confirm: vi.fn(), toast: vi.fn(), profile: vi.fn(),
}));
vi.mock("@/hooks/useGroupMembers", () => ({
  REACH_SEARCH_FLAG: "next-page",
  default: () => ({ fetchState: { loading: false, groupMemberList: f.rows, searchMemberList: [] },
    getMemberData: f.getMembers, searchMember: f.search, resetState: f.reset,
    captureMember: (member: any) => ({ member: { ...member }, current: () => f.rows.some((row) => row.userID === member.userID) }) }),
}));
vi.mock("@/hooks/useCurrentMemberRole", () => ({ useCurrentMemberRole: () => f.role }));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: { current: { userID: "actor", issuer: "A", tenant: { serverID: "A" } }, generation: 1 } }));
vi.mock("@/store", () => {
  const getState = () => ({ currentConversation: { conversationID: "cid-one", groupID: "group-one" },
    currentGroupInfo: { groupID: "group-one", status: 0, lookMemberInfo: 0 },
    currentMemberInGroup: { groupID: "group-one", userID: f.selfID, roleLevel: f.role.isOwner ? 100 : f.role.isAdmin ? 60 : 20 },
    captureConversationSendScope: () => ({ target: { kind: "group", id: "group-one", conversationID: "cid-one" }, current: () => true, canSend: () => true }) });
  return { useUserStore: Object.assign((select: any) => select({ selfInfo: { userID: f.selfID } }), { getState: () => ({ selfInfo: { userID: f.selfID } }) }),
    useConversationStore: Object.assign((select: any) => select(getState()), { getState }) };
});
vi.mock("@/store/contact", () => ({ useContactStore: (select: any) => select({ friendList: f.friends }) }));
vi.mock("@/layout/MainContentWrap", () => ({ IMSDK: {
  changeGroupMemberMute: f.mute, setGroupMemberInfo: f.setMember, kickGroupMember: f.kick,
} }));
vi.mock("@/AntdGlobalComp", () => ({ modal: { confirm: f.confirm } }));
vi.mock("@/utils/common", () => ({ feedbackToast: f.toast }));
vi.mock("ahooks", () => ({ useRequest: (callback: any) => ({
  loading: false, runAsync: (...args: any[]) => callback(...args),
}) }));
vi.mock("i18next", () => ({ t: (key: string, options?: { num: number }) =>
  options?.num === undefined ? key : `${key}:${options.num}` }));
vi.mock("open-im-sdk-wasm", () => ({ GroupMemberRole: { Nomal: 20, Admin: 60, Owner: 100 }, AllowType: { NotAllowed: 1, Allowed: 0 } }));
vi.mock("@ant-design/icons", () => ({ ArrowLeftOutlined: () => null, CheckOutlined: () => null, CloseOutlined: () => null }));
vi.mock("@/components/OIMAvatar", () => ({ default: () => null }));
vi.mock("antd", () => ({
  Spin: ({ children }: any) => React.createElement(React.Fragment, null, children),
  Tooltip: ({ title, children }: any) => React.createElement("div", { "data-tooltip": title }, children),
  Modal: ({ open, children }: any) => open ? React.createElement("div", { role: "dialog" }, children) : null,
  Empty: () => React.createElement("div", null, "empty"),
}));
vi.mock("react-virtuoso", () => ({ Virtuoso: ({ data, itemContent }: any) =>
  React.createElement("div", null, data.map((member: any, index: number) =>
    React.createElement("div", { key: member.userID, "data-member-id": member.userID }, itemContent(index, member)))) }));

import GroupMemberList from "@/pages/chat/queryChat/GroupSetting/GroupMemberList";

const NOW = Date.UTC(2026, 9, 16, 12);
const makeMember = (muteEndTime: number, roleLevel = 20, userID = "peer") => ({
  groupID: "group-one", userID, nickname: "normal member", faceURL: "", roleLevel, muteEndTime,
});
let root: Root, host: HTMLDivElement, disposed: boolean;
let originalUserClick: typeof window.userClick;
async function render() { await act(async () => root.render(React.createElement(GroupMemberList, { isSearching: false }))); }
function muted() {
  const image = host.querySelector<HTMLImageElement>('[data-tooltip="placeholder.mute"] img');
  expect(image).not.toBeNull();
  return image!.getAttribute("src")!.includes("member_mute_active.png");
}
async function click(element: Element | null | undefined) {
  expect(element).not.toBeNull(); expect(element).not.toBeUndefined();
  await act(async () => { element!.dispatchEvent(new MouseEvent("click", { bubbles: true })); });
}
function label(value: string) { return [...host.querySelectorAll("span")].find((span) => span.textContent === value); }
function noWrites() {
  expect(f.mute).not.toHaveBeenCalled(); expect(f.setMember).not.toHaveBeenCalled(); expect(f.kick).not.toHaveBeenCalled();
}
beforeEach(() => {
  vi.resetAllMocks(); vi.useFakeTimers(); vi.setSystemTime(NOW);
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  f.rows = [makeMember(0)]; f.friends = []; f.selfID = "actor";
  f.role = { isOwner: true, isAdmin: false, currentMemberInGroup: { groupID: "group-one" } };
  f.mute.mockResolvedValue({ data: {} }); f.setMember.mockResolvedValue({ data: {} }); f.kick.mockResolvedValue({ data: {} });
  originalUserClick = window.userClick; window.userClick = f.profile;
  host = document.createElement("div"); document.body.append(host); root = createRoot(host); disposed = false;
});
afterEach(async () => {
  if (!disposed) await act(async () => root.unmount());
  host.remove(); window.userClick = originalUserClick; vi.clearAllTimers(); vi.useRealTimers();
});

describe("real group member Unix-millisecond mute UI", () => {
  it.each([
    ["zero is normal", NOW, 0, false],
    ["expired millisecond timestamp is normal", NOW, NOW - 1, false],
    ["exact deadline is normal", NOW, NOW, false],
    ["future millisecond timestamp is muted", NOW, NOW + 60_000, true],
    ["expiry beyond signed Unix-second 2038 boundary stays normal", 2_147_483_648_000, 2_147_483_647_999, false],
    ["future beyond signed Unix-second 2038 boundary stays muted", 2_147_483_648_000, 2_147_483_648_001, true],
  ] as const)("%s", async (_name, now, end, expected) => {
    vi.setSystemTime(now); f.rows = [makeMember(end)]; await render(); expect(muted()).toBe(expected); noWrites();
  });

  it("changes an already-mounted row to normal exactly at expiry without a native unmute", async () => {
    f.rows = [makeMember(NOW + 1_000)]; await render(); expect(muted()).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(999); }); expect(muted()).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); }); expect(muted()).toBe(false);
    expect(vi.getTimerCount()).toBe(0); noWrites();
  });
  it("re-arms a far-future deadline within the browser timer limit, without overflow or writes", async () => {
    f.rows = [makeMember(Date.UTC(2038, 0, 19, 3, 14, 8))]; await render(); expect(muted()).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(2_147_483_647); });
    expect(muted()).toBe(true); expect(vi.getTimerCount()).toBe(1); noWrites();
  });
  it("replaces the old expiry timer when an SDK row changes its deadline", async () => {
    f.rows = [makeMember(NOW + 1_000)]; await render();
    f.rows = [makeMember(NOW + 2_000)]; await render();
    await act(async () => { await vi.advanceTimersByTimeAsync(1_000); }); expect(muted()).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(1_000); }); expect(muted()).toBe(false); noWrites();
  });
  it("cancels only the local row timer on unmount", async () => {
    f.rows = [makeMember(NOW + 1_000)]; await render(); expect(vi.getTimerCount()).toBe(1);
    await act(async () => root.unmount()); disposed = true;
    expect(vi.getTimerCount()).toBe(0); noWrites();
  });

  it.each([
    ["owner may manage another normal member", true, false, 20, "peer", true, true],
    ["owner may manage another administrator", true, false, 60, "peer", true, true],
    ["owner cannot manage self", true, false, 100, "actor", false, false],
    ["administrator may manage a normal member", false, true, 20, "peer", true, false],
    ["administrator cannot manage another administrator", false, true, 60, "peer", false, false],
    ["administrator cannot manage the owner", false, true, 100, "peer", false, false],
    ["normal member has no management actions", false, false, 20, "peer", false, false],
  ] as const)("%s", async (_name, owner, admin, targetRole, userID, tools, adminButton) => {
    f.role.isOwner = owner; f.role.isAdmin = admin; f.rows = [makeMember(NOW + 60_000, targetRole, userID)]; await render();
    expect(Boolean(host.querySelector('[data-tooltip="placeholder.mute"]'))).toBe(tools);
    expect(Boolean(host.querySelector('[data-tooltip="placeholder.kick"]'))).toBe(tools);
    expect(Boolean(host.querySelector('[data-tooltip="placeholder.setAdministrator"]'))).toBe(adminButton); noWrites();
  });

  it("keeps an expired row's explicit ten-minute mute action and seconds request payload", async () => {
    f.rows = [makeMember(NOW - 1)]; await render(); expect(muted()).toBe(false);
    await click(host.querySelector('[data-tooltip="placeholder.mute"] img'));
    expect(host.querySelector('[role="dialog"]')).not.toBeNull(); noWrites();
    await click(label("date.minute:10")); await click(label("placeholder.save"));
    expect(f.mute).toHaveBeenCalledExactlyOnceWith({ groupID: "group-one", userID: "peer", mutedSeconds: 600 });
  });
  it("sends cancel-mute zero only after the user's explicit option and save", async () => {
    f.rows = [makeMember(NOW + 1_000)]; await render(); expect(muted()).toBe(true);
    await click(host.querySelector('[data-tooltip="placeholder.mute"] img'));
    await click(label("placeholder.cancelMute")); noWrites(); await click(label("placeholder.save"));
    expect(f.mute).toHaveBeenCalledExactlyOnceWith({ groupID: "group-one", userID: "peer", mutedSeconds: 0 });
  });
  it("preserves normal display name, profile target and initial member read", async () => {
    f.friends = [{ userID: "peer", remark: "my member remark" }]; await render();
    expect(host.textContent).toContain("my member remark");
    const name = [...host.querySelectorAll("div")].reverse().find((item) => item.textContent === "my member remark");
    await click(name); expect(f.profile).toHaveBeenCalledExactlyOnceWith("peer", "group-one");
    expect(f.getMembers).toHaveBeenCalledExactlyOnceWith(true); noWrites();
  });
});
