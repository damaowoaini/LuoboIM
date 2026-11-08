// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
const mocks=vi.hoisted(()=>({
 session:{generation:0,current:{issuer:"https://route.synthetic.test",tenant:{serverID:"A"},userID:"same-user"}},
 sdk:{getAdvancedHistoryMessageList:vi.fn(),getAdvancedHistoryMessageListReverse:vi.fn(),searchLocalMessages:vi.fn()},
 conversation:{currentConversation:{conversationID:"same-conversation"}},toast:vi.fn(),
}));
vi.mock("open-im-sdk-wasm",()=>({MessageStatus:{Sending:1,Succeed:2,Failed:3},MessageType:{PictureMessage:102,VideoMessage:104},SessionType:{Single:1,Notification:4}}));
vi.mock("@/core/session/browser",()=>({sessionCoordinator:mocks.session}));
vi.mock("@/layout/MainContentWrap",()=>({IMSDK:mocks.sdk}));
vi.mock("@/utils/common",()=>({feedbackToast:mocks.toast}));
vi.mock("@/store/contact",()=>({useContactStore:{getState:()=>({friendList:[]})}}));
vi.mock("@/store/user",()=>({useUserStore:{getState:()=>({imageCache:{}})}}));
vi.mock("@/store/conversation",()=>({useConversationStore:{getState:()=>mocks.conversation,setState:vi.fn()}}));
import { useMessageStore } from "@/store/message";
const message=(id:string)=>({clientMsgID:id,sendTime:1800000000000,sessionType:1,seq:2,textElem:{content:id}} as any);
const response=(id:string)=>({data:{messageList:[message(id)],lastMinSeq:1,isEnd:true}});
beforeEach(()=>{vi.clearAllMocks();mocks.session.generation++;mocks.session.current.tenant.serverID="A";mocks.conversation.currentConversation.conversationID="same-conversation";useMessageStore.setState({historyMessageList:[],previewImgList:[],lastMinSeq:0,hasMore:true});});

describe("actual history store recovery",()=>{
 it("keeps live arrivals and retryable sends across a delayed latest-page read",async()=>{
  const ids=()=>useMessageStore.getState().historyMessageList.map(item=>item.clientMsgID);
  const store=useMessageStore.getState();
  const pending={...message("pending"),status:1};
  useMessageStore.setState({historyMessageList:[pending]});
  let finish!:(value:unknown)=>void;
  mocks.sdk.getAdvancedHistoryMessageList.mockImplementationOnce(()=>new Promise(resolve=>finish=resolve));
  const latest=store.getHistoryMessageListByReq();
  store.pushNewMessage(message("arrived"));
  finish(response("older"));
  await latest;
  expect(ids()).toEqual(["arrived","pending","older"]);

  const confirmed={...pending,status:2};
  mocks.sdk.getAdvancedHistoryMessageList.mockResolvedValueOnce({
   data:{messageList:[message("older"),confirmed,message("arrived"),message("arrived")],lastMinSeq:1,isEnd:true},
  });
  await store.getHistoryMessageListByReq();
  expect(ids()).toEqual(["arrived","pending","older"]);
  expect(useMessageStore.getState().historyMessageList[1].status).toBe(2);

  mocks.sdk.getAdvancedHistoryMessageList.mockRejectedValueOnce(new Error("temporarily offline"));
  await store.getHistoryMessageListByReq();
  expect(ids()).toEqual(["arrived","pending","older"]);
  expect(mocks.toast).toHaveBeenCalledOnce();
 });
});
