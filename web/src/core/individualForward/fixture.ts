import { vi } from "vitest";
// Native SDK/transport boundary only; conversation store, sender, privacy and
// session/history SDK wrappers stay real. Nothing here sends business traffic.
const fixture = vi.hoisted(() => {
  const owner = () => ({ userID: "actor", issuer: "https://fixture.invalid", tenant: { serverID: "A" } });
  const session = { current: owner() as ReturnType<typeof owner> | undefined, generation: 1,
    onChange: vi.fn(() => () => {}), guard: (callback: unknown) => callback };
  const sdk = { on: vi.fn(), off: vi.fn(), login: vi.fn(), logout: vi.fn(), sendMessage: vi.fn(),
    getOneConversation: vi.fn(), getSpecifiedGroupsInfo: vi.fn(), getSpecifiedGroupMembersInfo: vi.fn(),
    findMessageList: vi.fn(), createForwardMessage: vi.fn(), createTextMessage: vi.fn(), createMergerMessage: vi.fn() };
  return { owner, session, sdk, sources: new Map<string, any>(), selection: [] as any[], additional: "", sequence: 0, toast: vi.fn() };
});
export { fixture };
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: fixture.session }));
vi.mock("@/layout/MainContentWrap", async () => {
  const { bindSessionSdk } = await import("@/core/session/sdk");
  const { bindMessageHistory } = await import("@/core/messageHistory");
  const { messageHistoryFence, messageOwner } = await import("@/core/messagePrivacyRuntime");
  return { IMSDK: bindMessageHistory(bindSessionSdk(fixture.sdk, fixture.session as any), messageHistoryFence, messageOwner) };
});
vi.mock("@/store/user", () => ({ useUserStore: { getState: () => ({ selfInfo: { userID: "actor" } }) } }));
vi.mock("@/store/contact", () => ({ useContactStore: { getState: () => ({ friendList: [] }) } }));
vi.mock("@/store/message", async () => {
  const { create } = await import("zustand");
  return { useMessageStore: create<any>((set: import("zustand").StoreApi<any>["setState"], get: import("zustand").StoreApi<any>["getState"]) => ({ historyMessageList: [], isCheckMode: true,
    updateCheckMode: (isCheckMode: boolean) => set({ isCheckMode, historyMessageList: get().historyMessageList.map((m: any) => ({ ...m, checked: false })) }),
    pushNewMessage: vi.fn(), updateOneMessage: vi.fn(), deleteOneMessage: vi.fn(),
    deleteAndPushOneMessage: vi.fn(), tryAddPreviewImg: vi.fn(), updateJumpClientMsgID: vi.fn() })) };
});
vi.mock("@/store", async () => ({ ...(await import("@/store/conversation")), ...(await import("@/store/message")),
  useUserStore: { getState: () => ({ selfInfo: { userID: "actor" } }) } }));
vi.mock("@/utils/common", () => ({ feedbackToast: fixture.toast }));
vi.mock("@/utils/imCommon", () => ({ isGroupSession: (n: number) => n === 3,
  conversationSort: (items: unknown) => items, formatMessageByType: () => "synthetic summary" }));
vi.mock("i18next", () => ({ t: (key: string, options?: { index?: number }) => key === "placeholder.individualForwardItem" ? `第${options?.index}条` : key, default: { on: vi.fn() } }));
vi.mock("@/AntdGlobalComp", () => ({ message: { warning: fixture.toast, success: fixture.toast, error: fixture.toast } }));
vi.mock("open-im-sdk-wasm", () => ({ MessageStatus: { Sending: 1, Succeed: 2, Failed: 3 },
  MessageType: { TextMessage: 101, PictureMessage: 102, VoiceMessage: 103, VideoMessage: 104, FileMessage: 105,
    AtTextMessage: 106, MergeMessage: 107, CardMessage: 108, LocationMessage: 109, CustomMessage: 110, QuoteMessage: 114, FaceMessage: 115, RevokeMessage: 2101 },
  SessionType: { Single: 1, WorkingGroup: 3 }, GroupType: { WorkingGroup: 2 },
  GroupStatus: { Nomal: 0, Baned: 1, Dismissed: 2, Muted: 3 } }));
import { useConversationStore } from "@/store/conversation";
import { useMessageStore } from "@/store/message";
import { messageHistoryFence, messagePrivacyLedger } from "@/core/messagePrivacyRuntime";
export const store = useConversationStore;
export const messages = useMessageStore;
export const conversation = (name = "A", kind: "user" | "group" = "user") => ({ conversationID: `cid-${kind}-${name}`,
  conversationType: kind === "user" ? 1 : 3, userID: kind === "user" ? name : "", groupID: kind === "group" ? name : "",
  showName: name, unreadCount: 0 } as any);
export const group = (groupID = "G", status = 0) => ({ groupID, status } as any);
export const member = (groupID = "G", userID = "actor", roleLevel = 20, muteEndTime = 0) => ({ groupID, userID, roleLevel, muteEndTime } as any);
export const enter = (name = "A", kind: "user" | "group" = "user") => store.getState().updateCurrentConversation(conversation(name, kind), true);
export const message = (clientMsgID = "synthetic-message") => ({ clientMsgID, contentType: 101, status: 1, textElem: { content: "synthetic" } } as any);
export const flush = async () => { await new Promise(resolve => setTimeout(resolve, 0)); for (let i = 0; i < 12; ++i) await Promise.resolve(); };
export const deferred = <T,>() => { let resolve!: (value: T) => void; let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; };
export function reset() {
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  vi.clearAllMocks(); Object.values(fixture.sdk).forEach(mock => mock.mockReset());
  fixture.session.current = fixture.owner(); fixture.session.generation++;
  store.getState().clearConversationStore(); messageHistoryFence.invalidate(); messagePrivacyLedger.clear();
  fixture.sources.clear(); fixture.sequence = 0; fixture.selection = []; fixture.additional = "";
  messages.setState({ historyMessageList: [], isCheckMode: true });
  fixture.sdk.getOneConversation.mockImplementation(async ({ sourceID, sessionType }) => ({ data: conversation(sourceID, sessionType === 3 ? "group" : "user") }));
  fixture.sdk.getSpecifiedGroupsInfo.mockImplementation(async ([id]) => ({ data: [group(id)] }));
  fixture.sdk.getSpecifiedGroupMembersInfo.mockImplementation(async ({ groupID }) => ({ data: [member(groupID)] }));
  fixture.sdk.findMessageList.mockImplementation(async ([query]) => {
    const source = fixture.sources.get(query.clientMsgIDList[0]);
    return { data: { totalCount: source ? 1 : 0, findResultItems: source ? [{ conversationID: query.conversationID,
      messageCount: 1, messageList: [source] }] : [] } };
  });
  fixture.sdk.sendMessage.mockImplementation(async ({ message }) => ({ data: { ...message, status: 2 } }));
  for (const name of ["createForwardMessage", "createTextMessage", "createMergerMessage"] as const)
    fixture.sdk[name].mockImplementation(async () => ({ data: message(`created-${++fixture.sequence}`) }));
  enter();
}
