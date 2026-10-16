import { vi } from "vitest";

// Transport, account lifecycle and unrelated stores are boundaries. The
// conversation reducer and its existing SDK generation proxy remain real.
const fixture = vi.hoisted(() => {
  type Read = { args: unknown; resolve(value: unknown): void; reject(error: unknown): void };
  const groups: Read[] = [];
  const members: Read[] = [];
  const user = { selfInfo: { userID: "actor-a" } };
  const owner = () => ({ userID: "actor-a", issuer: "https://fixture.invalid", tenant: { serverID: "tenant-a" } });
  const session = { current: owner() as ReturnType<typeof owner> | undefined, generation: 1,
    onChange: vi.fn(() => () => {}), guard: (callback: unknown) => callback };
  const hooks = { onGroupDispatch: undefined as (() => void) | undefined };
  const pending = (reads: Read[], args: unknown) => new Promise((resolve, reject) => reads.push({ args, resolve, reject }));
  const sdk = {
    getSpecifiedGroupsInfo: vi.fn((args: unknown) => { hooks.onGroupDispatch?.(); return pending(groups, args); }),
    getSpecifiedGroupMembersInfo: vi.fn((args: unknown) => pending(members, args)),
    setConversationDraft: vi.fn(async () => ({ data: null })),
    markConversationMessageAsRead: vi.fn(async () => ({ data: null })),
    resetConversationGroupAtType: vi.fn(async () => ({ data: null })),
    typingStatusUpdate: vi.fn(async () => ({ data: null })),
    on: vi.fn(), off: vi.fn(), logout: vi.fn(async () => ({})), login: vi.fn(async () => ({})),
  };
  return { groups, members, user, owner, session, hooks, sdk, toast: vi.fn() };
});
// Vitest rejects an exported variable declaration containing vi.hoisted.
// Export the initialized binding separately without changing any fixture data.
export { fixture };

vi.mock("@/core/session/browser", () => ({ sessionCoordinator: fixture.session }));
vi.mock("@/layout/MainContentWrap", async () => {
  const { bindSessionSdk } = await import("@/core/session/sdk");
  return { IMSDK: bindSessionSdk(fixture.sdk, fixture.session as any) };
});
vi.mock("@/store/user", () => ({ useUserStore: { getState: () => fixture.user } }));
vi.mock("@/store/contact", () => ({ useContactStore: { getState: () => ({ friendList: [] }) } }));
vi.mock("@/store/message", async () => {
  const { create } = await import("zustand");
  const useMessageStore = create<any>((set: (value: { isCheckMode: boolean }) => void) => ({
    isCheckMode: false, jumpLoading: false, jumpClientMsgID: undefined,
    updateJumpClientMsgID: vi.fn(), updateCheckMode: vi.fn((value) => set({ isCheckMode: value })),
    getHistoryMessageListByReq: vi.fn(async () => {}),
    getConversationPreviewImgList: vi.fn(), clearHistoryMessage: vi.fn(),
  }));
  return { useMessageStore };
});
vi.mock("@/store", async () => ({
  ...(await import("@/store/conversation")), ...(await import("@/store/message")),
}));
vi.mock("@/utils/common", () => ({ feedbackToast: fixture.toast,
  escapeHtml: (value: string) => value, getExtraStr: () => "" }));
vi.mock("@/utils/imCommon", () => ({ isGroupSession: (value: number) => value === 3,
  conversationSort: (value: unknown) => value, formatMessageByType: () => "" }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("open-im-sdk-wasm", () => ({
  SessionType: { Single: 1, Group: 3, WorkingGroup: 3, Notification: 4 },
  GroupMemberRole: { Nomal: 20, Admin: 60, Owner: 100 },
  GroupStatus: { Nomal: 0, Baned: 1, Dismissed: 2, Muted: 3 },
  GroupAtType: { AtNormal: 0, AtGroupNotice: 4 },
  MessageType: { AtTextMessage: 106, QuoteMessage: 114 },
}));

import { useConversationStore } from "@/store/conversation";

export const store = useConversationStore;
export const group = (groupID = "A", status = 0) => ({ groupID, status, groupName: groupID } as any);
export const member = (groupID = "A", userID = "actor-a", roleLevel = 20, muteEndTime = 0) =>
  ({ groupID, userID, roleLevel, muteEndTime } as any);
export const conversation = (groupID = "A") => ({ conversationID: "cid-" + groupID,
  conversationType: 3, groupID, userID: "", unreadCount: 0, groupAtType: 0,
  draftText: "kept user draft", showName: groupID } as any);
export const enter = (groupID = "A") => store.getState().updateCurrentConversation(conversation(groupID));
export const flush = async () => { for (let i = 0; i < 5; ++i) await Promise.resolve(); };
export function resetFixture() {
  store.getState().clearConversationStore();
  fixture.session.current = fixture.owner();
  fixture.session.generation += 1;
  fixture.user.selfInfo.userID = "actor-a";
  fixture.groups.length = fixture.members.length = 0;
  fixture.hooks.onGroupDispatch = undefined;
  fixture.toast.mockClear();
  Object.values(fixture.sdk).forEach((value) => value.mockClear());
}
