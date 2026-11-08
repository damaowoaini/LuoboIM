// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
const mocks=vi.hoisted(()=>({
 session:{generation:0,current:{issuer:"https://route.synthetic.test",tenant:{serverID:"A"},userID:"same-user"}},
 sdk:{getAdvancedHistoryMessageList:vi.fn(),getAdvancedHistoryMessageListReverse:vi.fn(),searchLocalMessages:vi.fn()},
 conversation:{currentConversation:{conversationID:"same-conversation"}},toast:vi.fn(),
}));
vi.mock("open-im-sdk-wasm",()=>({MessageType:{PictureMessage:102,VideoMessage:104},SessionType:{Single:1,Notification:4}}));
vi.mock("@/core/session/browser",()=>({sessionCoordinator:mocks.session}));
vi.mock("@/layout/MainContentWrap",()=>({IMSDK:mocks.sdk}));
vi.mock("@/utils/common",()=>({feedbackToast:mocks.toast}));
vi.mock("@/store/contact",()=>({useContactStore:{getState:()=>({friendList:[]})}}));
vi.mock("@/store/user",()=>({useUserStore:{getState:()=>({imageCache:{}})}}));
vi.mock("@/store/conversation",()=>({useConversationStore:{getState:()=>mocks.conversation,setState:vi.fn()}}));
import { useMessageStore } from "@/store/message";
import { forgetMessage, messageHistoryFence } from "./messagePrivacyRuntime";
const message=(id:string)=>({clientMsgID:id,sendTime:1800000000000,sessionType:1,seq:2,textElem:{content:id}} as any);
const response=(id:string)=>({data:{messageList:[message(id)],lastMinSeq:1,isEnd:true}});
beforeEach(()=>{vi.clearAllMocks();mocks.session.generation++;mocks.session.current.tenant.serverID="A";mocks.conversation.currentConversation.conversationID="same-conversation";useMessageStore.setState({historyMessageList:[],previewImgList:[],lastMinSeq:0,hasMore:true});});

describe("actual history store stale results",()=>{
 it("does not refill a cleared timeline or show an old error after its in-flight history request settles",async()=>{
  let finish!:(value:unknown)=>void;mocks.sdk.getAdvancedHistoryMessageList.mockImplementation(()=>new Promise(resolve=>finish=resolve));
  const pending=useMessageStore.getState().getHistoryMessageListByReq();
  useMessageStore.getState().clearHistoryMessage();finish(response("cleared-secret"));await pending;
  expect(useMessageStore.getState().historyMessageList).toEqual([]);expect(mocks.toast).not.toHaveBeenCalled();
 });
 it("rejects same-conversation IDs from a retired tenant/session before any store commit",async()=>{
  let finish!:(value:unknown)=>void;mocks.sdk.getAdvancedHistoryMessageList.mockImplementation(()=>new Promise(resolve=>finish=resolve));
  const pending=useMessageStore.getState().getHistoryMessageListByReq();mocks.session.generation++;mocks.session.current.tenant.serverID="B";
  useMessageStore.setState({historyMessageList:[message("tenant-b")]});finish(response("tenant-a-secret"));await pending;
  expect(useMessageStore.getState().historyMessageList.map(item=>item.clientMsgID)).toEqual(["tenant-b"]);
 });
 it("filters explicit deletion tombstones from a late history reply",async()=>{
  let finish!:(value:unknown)=>void;mocks.sdk.getAdvancedHistoryMessageList.mockImplementation(()=>new Promise(resolve=>finish=resolve));
  const pending=useMessageStore.getState().getHistoryMessageListByReq();forgetMessage("same-conversation","deleted");finish(response("deleted"));await pending;
  expect(useMessageStore.getState().historyMessageList).toEqual([]);
 });
 it("uses both actual history directions for a source jump and rejects its result across clear",async()=>{
  let finish!:(value:unknown)=>void;mocks.sdk.getAdvancedHistoryMessageList.mockImplementation(()=>new Promise(resolve=>finish=resolve));mocks.sdk.getAdvancedHistoryMessageListReverse.mockResolvedValue(response("later"));
  const pending=useMessageStore.getState().getTwoWayHistoryMessage({message:message("target"),conversationID:"same-conversation"});
  messageHistoryFence.invalidate();finish(response("earlier"));await pending;
  expect(mocks.sdk.getAdvancedHistoryMessageList).toHaveBeenCalledOnce();expect(mocks.sdk.getAdvancedHistoryMessageListReverse).toHaveBeenCalledOnce();expect(useMessageStore.getState().historyMessageList).toEqual([]);
 });
});
