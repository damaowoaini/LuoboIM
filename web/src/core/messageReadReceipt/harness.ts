import { vi } from "vitest";
const fixture = vi.hoisted(() => {
  const owner = () => ({ userID: "actor", issuer: "https://synthetic.invalid", tenant: { serverID: "tenant" } });
  const session = { current: owner() as ReturnType<typeof owner> | undefined, generation: 1,
    onChange: vi.fn(() => () => {}), guard: (callback: unknown) => callback };
  const listeners = new Map<string, Set<Function>>();
  const sdk = {
    on: vi.fn((name: string, callback: Function) => { const list = listeners.get(name) ?? new Set(); list.add(callback); listeners.set(name, list); }),
    off: vi.fn((name: string, callback: Function) => listeners.get(name)?.delete(callback)),
    login: vi.fn(async () => ({})), logout: vi.fn(async () => ({})),
    markConversationMessageAsRead: vi.fn(), findMessageList: vi.fn(), resetConversationGroupAtType: vi.fn(),
    getSpecifiedGroupsInfo: vi.fn(), getSpecifiedGroupMembersInfo: vi.fn(), setConversationDraft: vi.fn(),
  };
  return { owner, session, listeners, sdk, user: { selfInfo: { userID: "actor" } } };
});
export { fixture };
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: fixture.session }));
vi.mock("@/layout/MainContentWrap", async () => {
  const { bindSessionSdk } = await import("@/core/session/sdk");
  return { IMSDK: bindSessionSdk(fixture.sdk, fixture.session as any) };
});
vi.mock("@/store/user", () => {
  const useUserStore = Object.assign((selector: Function) => selector(fixture.user), { getState: () => fixture.user });
  return { useUserStore };
});
vi.mock("@/store/contact", () => ({ useContactStore: { getState: () => ({ friendList: [] }) } }));
vi.mock("@/store", async () => ({ ...(await import("@/store/conversation")), ...(await import("@/store/message")), ...(await import("@/store/user")) }));
vi.mock("@/utils/common", () => ({ feedbackToast: vi.fn(), escapeHtml: (value: string) => value, getExtraStr: () => "" }));
vi.mock("@/utils/imCommon", () => ({ isGroupSession: (kind: number) => kind === 3, conversationSort: (rows: unknown) => rows, formatMessageByType: () => "" }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("open-im-sdk-wasm", () => ({ CbEvents: { OnRecvC2CReadReceipt: "single-read", OnRecvGroupReadReceipt: "group-read" },
  SessionType: { Single: 1, WorkingGroup: 3, Notification: 4 }, GroupAtType: { AtNormal: 0, AtGroupNotice: 4 },
  GroupMemberRole: { Nomal: 20, Admin: 60, Owner: 100 }, GroupStatus: { Nomal: 0, Baned: 1, Dismissed: 2, Muted: 3 },
  MessageType: { TextMessage: 101, AtTextMessage: 106, QuoteMessage: 114 } }));

import { useConversationStore } from "@/store/conversation";
import { useMessageStore } from "@/store/message";
import { messageHistoryFence, messagePrivacyLedger } from "@/core/messagePrivacyRuntime";
export const store = useConversationStore, messages = useMessageStore;
export const now = Date.UTC(2026, 9, 17);
export const conversation = (peer = "peer", unreadCount = 0) => ({ conversationID: "si-" + peer, userID: peer,
  groupID: "", conversationType: 1, groupAtType: 0, unreadCount, latestMsgSendTime: 1, latestMsg: "", showName: peer } as any);
export const enter = (peer = "peer", unreadCount = 0) => store.getState().updateCurrentConversation(conversation(peer, unreadCount), true);
export const wire = (id = "outgoing", incoming = false) => ({ clientMsgID: id, sendID: incoming ? "peer" : "actor",
  recvID: incoming ? "actor" : "peer", groupID: "", sessionType: 1, seq: 2, status: 2, contentType: 101, isRead: false,
  attachedInfoElem: { isPrivateChat: true, burnDuration: 30, hasReadTime: 0 }, textElem: { content: "synthetic" } } as any);
export const found = (message: any) => ({ data: { totalCount: 1, findResultItems: [{ conversationID: "si-peer", conversationType: 1, messageCount: 1, messageList: [message] }] } });
export const emit = (name: string, data: unknown) => { for (const callback of [...(fixture.listeners.get(name) ?? [])]) callback({ data }); };
export const flush = async () => { for (let i = 0; i < 18; i++) await Promise.resolve(); };
export const deferred = <T,>() => { let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((a, b) => { resolve = a; reject = b; }); return { promise, resolve, reject }; };
export function reset() {
  fixture.listeners.clear(); Object.values(fixture.sdk).forEach(mock => mock.mockReset());
  fixture.sdk.on.mockImplementation((name: string, callback: Function) => { const list = fixture.listeners.get(name) ?? new Set(); list.add(callback); fixture.listeners.set(name, list); });
  fixture.sdk.off.mockImplementation((name: string, callback: Function) => fixture.listeners.get(name)?.delete(callback));
  fixture.sdk.markConversationMessageAsRead.mockResolvedValue({ data: null });
  fixture.sdk.resetConversationGroupAtType.mockResolvedValue({ data: null });
  fixture.sdk.getSpecifiedGroupsInfo.mockResolvedValue({ data: [] }); fixture.sdk.getSpecifiedGroupMembersInfo.mockResolvedValue({ data: [] });
  fixture.sdk.findMessageList.mockResolvedValue({ data: { totalCount: 0, findResultItems: [] } });
  fixture.session.current = fixture.owner(); fixture.session.generation++; fixture.user.selfInfo.userID = "actor";
  store.getState().clearConversationStore(); messageHistoryFence.invalidate(); messagePrivacyLedger.clear(); enter();
  messages.setState({ historyMessageList: [], previewImgList: [] });
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
}
