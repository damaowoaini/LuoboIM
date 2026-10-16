// @vitest-environment jsdom
import {afterEach,beforeEach,describe,expect,it,vi} from "vitest";
const mocks=vi.hoisted(()=>({session:{generation:0,current:{issuer:"https://route.synthetic.test",tenant:{serverID:"A"},userID:"self"}},notifications:vi.fn()}));
vi.mock("@/core/session/browser",()=>({sessionCoordinator:mocks.session}));
vi.mock("open-im-sdk-wasm",()=>({MessageType:{TextMessage:101,RevokeMessage:2101},SessionType:{Single:1,Group:3,WorkingGroup:3}}));
vi.mock("@/store",()=>({useUserStore:{getState:()=>({selfInfo:{userID:"self"}})},useConversationStore:{getState:()=>({})}}));
vi.mock("@/store/contact",()=>({useContactStore:{getState:()=>({friendList:[]})}}));
vi.mock("@/utils/common",()=>({escapeHtml:(value:string)=>value,generateAvatar:()=>"synthetic-icon",secondsToTime:()=>"0"}));
vi.mock("@/utils/storage",()=>({getAccessedFriendApplication:vi.fn(),getAccessedGroupApplication:vi.fn()}));
import {createNotification,formatMessageByType,getConversationContent} from "@/utils/imCommon";
import {forgetMessage} from "./messagePrivacyRuntime";
const wire=(fields={})=>({clientMsgID:"same-client",contentType:101,sendID:"self",groupID:"",textElem:{content:"synthetic-private-summary"},...fields} as any);
beforeEach(()=>{vi.clearAllMocks();mocks.session.generation++;mocks.session.current.issuer="https://route.synthetic.test";mocks.session.current.tenant.serverID="A";});
afterEach(()=>vi.unstubAllGlobals());
describe("actual conversation/notification formatter privacy memory",()=>{
 it("does not revive a previously observed private message when a metadata-only latestMsg omits the privacy fields",()=>{
  expect(formatMessageByType(wire({attachedInfoElem:{isPrivateChat:true}}))).not.toContain("synthetic-private-summary");
  expect(formatMessageByType(wire())).not.toContain("synthetic-private-summary");
  expect(getConversationContent(wire())).not.toContain("synthetic-private-summary");
 });
 it("does not expose an explicitly deleted message through a stale summary",()=>{
  expect(formatMessageByType(wire())).toBe("synthetic-private-summary");forgetMessage("c","same-client");
  expect(formatMessageByType(wire())).not.toContain("synthetic-private-summary");
 });
 it("does not issue an OS notification body containing a private summary after its metadata was omitted",()=>{
  formatMessageByType(wire({attachedInfoElem:{isPrivateChat:true}}));
  Object.defineProperty(document,"hidden",{configurable:true,value:true});
  vi.stubGlobal("Notification",class {static permission="granted";onclick:any;constructor(title:string,options:any){mocks.notifications(title,options);}close(){}});
  createNotification({message:wire(),conversation:{conversationID:"c",showName:"Synthetic",faceURL:"https://objects.synthetic.test/icon",conversationType:1} as any,callback:vi.fn()});
  expect(mocks.notifications).toHaveBeenCalledOnce();expect(mocks.notifications.mock.calls[0][1].body).not.toContain("synthetic-private-summary");
 });
 it("preserves ordinary summaries and isolates identical IDs at another issuer/session",()=>{
  formatMessageByType(wire({attachedInfoElem:{isPrivateChat:true}}));mocks.session.generation++;mocks.session.current.issuer="https://other-route.synthetic.test";
  expect(formatMessageByType(wire())).toBe("synthetic-private-summary");
 });
});
