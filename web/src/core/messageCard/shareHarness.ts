import { vi } from "vitest";

const fixture = vi.hoisted(() => {
  const owner = () => ({ userID: "actor", issuer: "https://fixture.invalid", tenant: { serverID: "A" } });
  const session = { current: owner() as ReturnType<typeof owner> | undefined, generation: 1,
    onChange: vi.fn(() => () => {}), guard: (callback: unknown) => callback };
  const sdk = {
    on: vi.fn(), off: vi.fn(), login: vi.fn(async () => ({})), logout: vi.fn(async () => ({})),
    sendMessage: vi.fn(), isJoinGroup: vi.fn(), getOneConversation: vi.fn(), getSpecifiedGroupsInfo: vi.fn(),
    getSpecifiedGroupMembersInfo: vi.fn(), createFileMessageByFile: vi.fn(), createImageMessageByFile: vi.fn(),
    createForwardMessage: vi.fn(), createTextMessage: vi.fn(), createCardMessage: vi.fn(), createFaceMessage: vi.fn(),
    createTextAtMessage: vi.fn(), createQuoteMessage: vi.fn(), createMergerMessage: vi.fn(), createCustomMessage: vi.fn(),
    setConversationDraft: vi.fn(), typingStatusUpdate: vi.fn(), fileMapSet: vi.fn(),
  };
  return { owner, session, sdk, confirmations: [] as any[], drop: {} as any,
    insert: vi.fn(), toast: vi.fn(), selection: [] as any[], additional: "", sequence: 0,
    save: vi.fn(), readFile: vi.fn(), editorChange: undefined as undefined | ((text: string) => void) };
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
  return { useMessageStore: create(() => ({ pushNewMessage: vi.fn(), updateOneMessage: vi.fn(),
    deleteAndPushOneMessage: vi.fn(), tryAddPreviewImg: vi.fn(), updateJumpClientMsgID: vi.fn() })) };
});
vi.mock("@/store", async () => ({ ...(await import("@/store/conversation")), ...(await import("@/store/message")),
  useUserStore: { getState: () => ({ selfInfo: { userID: "actor" } }) } }));
vi.mock("@/utils/common", () => ({ feedbackToast: fixture.toast, bytesToSize: (n: number) => String(n),
  base64toFile: () => new File(["synthetic"], "capture.png", { type: "image/png" }), escapeHtml: (s: string) => s, getExtraStr: () => "" }));
vi.mock("@/utils/imCommon", () => ({ isGroupSession: (n: number) => n === 3,
  conversationSort: (items: unknown) => items, formatMessageByType: () => "synthetic summary" }));
vi.mock("i18next", () => ({ t: (key: string) => key, default: { on: vi.fn() } }));
vi.mock("@/utils/events", () => ({ default: { emit: vi.fn(), on: vi.fn(), off: vi.fn() } }));
vi.mock("@/AntdGlobalComp", () => ({ modal: { confirm: (options: any) => fixture.confirmations.push(options) },
  message: { warning: fixture.toast, success: fixture.toast, error: fixture.toast } }));
vi.mock("ahooks", async (original) => ({ ...(await original<typeof import("ahooks")>()),
  useDrop: (_target: unknown, handlers: unknown) => { fixture.drop = handlers; } }));
vi.mock("open-im-sdk-wasm", () => ({ AllowType: { Allowed: 0, NotAllowed: 1 }, MessageStatus: { Sending: 1, Succeed: 2, Failed: 3 },
  MessageType: { PictureMessage: 102, VideoMessage: 104, FileMessage: 105, AtTextMessage: 106, QuoteMessage: 114 },
  SessionType: { Single: 1, WorkingGroup: 3 }, GroupType: { WorkingGroup: 2 },
  GroupStatus: { Nomal: 0, Baned: 1, Dismissed: 2, Muted: 3 } }));

import { useConversationStore } from "@/store/conversation";
import { useMessageStore } from "@/store/message";
import { messageHistoryFence } from "@/core/messagePrivacyRuntime";
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
  store.getState().clearConversationStore(); messageHistoryFence.invalidate();
  fixture.confirmations.length = fixture.selection.length = 0; fixture.additional = ""; fixture.drop = {};
  fixture.save.mockReset().mockResolvedValue("/task-synthetic/file"); fixture.readFile.mockReset().mockResolvedValue(new File(["synthetic"], "file.bin"));
  Object.defineProperty(window, "electronAPI", { configurable: true, value: { saveFileToDisk: fixture.save, getFileByPath: fixture.readFile, subscribe: vi.fn(() => () => {}), ipcInvoke: vi.fn(), fileExists: () => false, getDataPath: () => "" } });
  fixture.sdk.isJoinGroup.mockResolvedValue({ data: true });
  fixture.sdk.getOneConversation.mockImplementation(async ({ sourceID, sessionType }) => ({ data: conversation(sourceID, sessionType === 3 ? "group" : "user") }));
  fixture.sdk.getSpecifiedGroupsInfo.mockImplementation(async ([id]) => ({ data: [group(id)] }));
  fixture.sdk.getSpecifiedGroupMembersInfo.mockImplementation(async ({ groupID }) => ({ data: [member(groupID)] }));
  fixture.sdk.sendMessage.mockImplementation(async ({ message }) => ({ data: { ...message, status: 2 } }));
  for (const key of ["createTextMessage", "createFileMessageByFile", "createForwardMessage", "createImageMessageByFile", "createCardMessage", "createFaceMessage", "createTextAtMessage", "createQuoteMessage", "createMergerMessage", "createCustomMessage"] as const)
    fixture.sdk[key].mockImplementation(async () => ({ data: message(`created-${++fixture.sequence}`) }));
  enter();
}
