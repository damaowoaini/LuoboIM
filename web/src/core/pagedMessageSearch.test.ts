// @vitest-environment jsdom
import React,{act} from "react";
import {createRoot,type Root} from "react-dom/client";
import {afterEach,beforeEach,describe,expect,it,vi} from "vitest";
const mocks=vi.hoisted(()=>({session:{onChange:vi.fn(),generation:1,current:{issuer:"https://route.synthetic.test",userID:"same-user",tenant:{serverID:"tenant-a"}}},load:vi.fn(),error:vi.fn()}));
vi.mock("./session/browser",()=>({sessionCoordinator:mocks.session}));
import {messageHistoryFence} from "./messagePrivacyRuntime";
import {SearchPageCursor,usePagedMessageSearch} from "./usePagedMessageSearch";

describe("request-relative search pagination",()=>{
 it("starts a new keyword at1 then uses that request's page2 after an older query reached page3",()=>{
  const cursor=new SearchPageCursor();let page=cursor.begin("old",false)!;cursor.accept(page,20,20);page=cursor.begin("old",true)!;cursor.accept(page,20,20);
  const fresh=cursor.begin("new",false)!;expect(fresh.page).toBe(1);cursor.accept(fresh,20,20);expect(cursor.begin("new",true)?.page).toBe(2);
 });
 it("replaces an in-flight query and rejects its late page without consuming the replacement cursor",()=>{
  const cursor=new SearchPageCursor(),old=cursor.begin("old",false)!,fresh=cursor.begin("new",false)!;
  expect(cursor.accept(old,20,20)).toBe(false);expect(cursor.accept(fresh,20,20)).toBe(true);expect(cursor.begin("new",true)?.page).toBe(2);
 });
 it("blocks duplicate next-page calls and does not advance on read failure or exhausted pages",()=>{
  const cursor=new SearchPageCursor(),first=cursor.begin("a",false)!;expect(cursor.begin("a",true)).toBeUndefined();cursor.fail(first);
  const retry=cursor.begin("a",true)!;expect(retry.page).toBe(1);cursor.accept(retry,0,20);expect(cursor.begin("a",true)).toBeUndefined();
 });
});

let root:Root,host:HTMLDivElement,view:ReturnType<typeof usePagedMessageSearch<string>>;
function Probe({conversationID,enabled=true}:{conversationID:string;enabled?:boolean}){
 view=usePagedMessageSearch<string>({conversationID,enabled,loadPage:(keyword,page)=>mocks.load(conversationID,keyword,page),onError:mocks.error});
 return React.createElement("div",null,view.messageList.join(","));
}
async function render(conversationID:string,enabled=true){await act(async()=>root.render(React.createElement(Probe,{conversationID,enabled})));}
beforeEach(()=>{Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true});mocks.session.generation=1;mocks.session.current.issuer="https://route.synthetic.test";mocks.session.current.tenant.serverID="tenant-a";mocks.load.mockReset();mocks.error.mockReset();host=document.createElement("div");document.body.append(host);root=createRoot(host);});
afterEach(async()=>{await act(async()=>root.unmount());host.remove();});
describe("real search hook lifecycle",()=>{
 it("requests1,2 then1,2 on keyword replacement rather than skipping new results",async()=>{
  mocks.load.mockImplementation(async(_conversation,keyword)=>Array(20).fill(keyword));await render("conversation");
  for(const [keyword,append] of [["old",false],["old",true],["new",false],["new",true]] as const)await act(async()=>{view.search(keyword,append);});
  expect(mocks.load.mock.calls.map(call=>call[2])).toEqual([1,2,1,2]);expect(view.messageList).toHaveLength(40);expect(view.messageList.every(item=>item==="new")).toBe(true);
 });
 it("keeps new conversation results when an old request resolves after the new page mounted",async()=>{
  let finish!:(messages:string[])=>void;mocks.load.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve;})).mockResolvedValueOnce(["B-result"]);
  await render("A");await act(async()=>{view.search("first");});await render("B");await act(async()=>{view.search("second");});
  await act(async()=>{finish(["A-secret"]);});expect(host.textContent).toBe("B-result");expect(mocks.error).not.toHaveBeenCalled();
 });
 it("drops an issuer/generation change even if the React owner has not rerendered yet",async()=>{
  let finish!:(messages:string[])=>void;mocks.load.mockImplementation(()=>new Promise(resolve=>{finish=resolve;}));
  await render("same-id");await act(async()=>{view.search("keyword");});mocks.session.current.issuer="https://other.synthetic.test";mocks.session.generation++;
  await act(async()=>{finish(["old-tenant-secret"]);});expect(host.textContent).toBe("");expect(mocks.error).not.toHaveBeenCalled();
 });
 it("does not start another SDK read or report a late error after the drawer is closed",async()=>{
  let reject!:(error:Error)=>void;mocks.load.mockImplementation(()=>new Promise((_resolve,fail)=>{reject=fail;}));
  await render("A");await act(async()=>{view.search("word");});await render("A",false);
  await act(async()=>{view.search("another");reject(new Error("old read failed"));});
  expect(mocks.load).toHaveBeenCalledOnce();expect(mocks.error).not.toHaveBeenCalled();expect(host.textContent).toBe("");
 });
});

describe("actual search clear barrier",()=>{
 it("retires old download callbacks at clear but lets a newly subscribed callback update explicitly refreshed results",async()=>{
  mocks.load.mockResolvedValue(["normal"]);await render("A");await act(async()=>{view.search("first");});
  const oldUpdate=view.updateMessages;await act(async()=>messageHistoryFence.invalidate());
  await act(async()=>{view.search("refresh");});
  expect(view.updateMessages).not.toBe(oldUpdate);
  await act(async()=>{oldUpdate(()=>["old-secret"]);});expect(host.textContent).toBe("normal");
  await act(async()=>{view.updateMessages(messages=>messages.map(message=>message+"-downloaded"));});expect(host.textContent).toBe("normal-downloaded");
 });

 it("keeps an old page out after clear, requires an explicit refresh, and preserves ordinary new queries",async()=>{
  let finish!:(messages:string[])=>void;mocks.load.mockImplementationOnce(()=>new Promise(resolve=>finish=resolve)).mockResolvedValue(["fresh"]);
  await render("A");await act(async()=>{view.search("secret");});
  await act(async()=>{messageHistoryFence.invalidate();});await act(async()=>{finish(["old-secret"]);});expect(host.textContent).toBe("");
  await act(async()=>{view.search("secret",false,true);});expect(mocks.load).toHaveBeenCalledOnce();
  await act(async()=>{view.search("explicit");});expect(host.textContent).toBe("fresh");expect(mocks.load).toHaveBeenCalledTimes(2);
 });
});
