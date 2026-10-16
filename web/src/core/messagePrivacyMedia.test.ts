// @vitest-environment jsdom
import React,{act,createRef} from "react";
import {createRoot,type Root} from "react-dom/client";
import {afterEach,beforeEach,describe,expect,it,vi} from "vitest";
const mocks=vi.hoisted(()=>({session:{generation:0,current:{issuer:"https://route.synthetic.test",tenant:{serverID:"A"},userID:"same-user"}},items:[] as any[],previewProps:undefined as any,download:vi.fn(),load:vi.fn(),videoUnmount:vi.fn()}));
vi.mock("@/core/session/browser",()=>({sessionCoordinator:mocks.session}));
vi.mock("antd",()=>({Image:{PreviewGroup:(props:any)=>{mocks.previewProps=props;return React.createElement("div",null,props.preview.visible?"preview-visible":"preview-closed",...props.items,props.preview.visible&&props.preview.imageRender?props.preview.imageRender(React.createElement("img"),{current:props.preview.current}):null);}},Spin:()=>null}));
vi.mock("@/components/VideoPlayer",()=>({default:({url}:{url:string})=>{React.useEffect(()=>()=>mocks.videoUnmount(),[url]);return React.createElement("span",null,url);}}));
vi.mock("@/components/PreviewToolsBar",()=>({default:()=>React.createElement("span",null,"export-tools")}));
vi.mock("@/utils/common",()=>({downloadFile:mocks.download,getDownloadTask:()=>undefined}));
vi.mock("@/store",()=>({
 useMessageStore:Object.assign((selector:(state:any)=>unknown)=>selector({previewImgList:mocks.items,updateDownloadTask:vi.fn(),getConversationPreviewImgList:mocks.load,downloadMap:{}}),{getState:()=>({previewImgList:mocks.items,downloadMap:{}})}),
 useUserStore:(selector:(state:any)=>unknown)=>selector({imageCache:{}}),useConversationStore:{getState:()=>({currentConversation:{conversationID:"c"}})},
}));
import MediaPreview from "@/pages/chat/queryChat/MediaPreview";
import {messageHistoryFence} from "./messagePrivacyRuntime";
let root:Root,host:HTMLDivElement,handle:ReturnType<typeof createRef<{showAlbum:(id:string)=>Promise<void>}>>;
const now=1800000000000;
const item=(isPrivate:boolean,readAt?:number)=>({clientMsgID:"media",url:"https://objects.synthetic.test/picture",sourceMessage:{clientMsgID:"media",attachedInfoElem:{isPrivateChat:isPrivate,hasReadTime:readAt,burnDuration:30}}});
async function render(){await act(async()=>root.render(React.createElement(MediaPreview,{conversationID:"c",ref:handle})));}
beforeEach(()=>{Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true});vi.clearAllMocks();vi.useFakeTimers();vi.setSystemTime(now);mocks.session.generation++;mocks.session.current.tenant.serverID="A";mocks.items=[];mocks.load.mockResolvedValue(undefined);Object.defineProperty(window,"electronAPI",{configurable:true,value:{fileExists:()=>false}});host=document.createElement("div");document.body.append(host);root=createRoot(host);handle=createRef();});
afterEach(async()=>{await act(async()=>root.unmount());host.remove();vi.useRealTimers();});
describe("actual album view versus retention",()=>{
 it("allows unexpired private viewing without desktop download, then removes the expired URL and closes the preview",async()=>{
  mocks.items=[item(true,now-29000)];await render();await act(async()=>handle.current!.showAlbum("media"));
  expect(mocks.previewProps.preview.visible).toBe(true);expect(mocks.download).not.toHaveBeenCalled();
  const original=React.createElement("span",null,"view-tools");expect(mocks.previewProps.preview.toolbarRender(original,{current:0})).toBe(original);
  await act(async()=>vi.advanceTimersByTimeAsync(2000));expect(mocks.previewProps.preview.visible).toBe(false);expect(mocks.previewProps.items).toEqual([]);expect(host.textContent).not.toContain("https://objects.synthetic.test/picture");
 });
 it("plays an unexpired private remote video without persistence and unmounts its player at expiry",async()=>{
  mocks.items=[{...item(true,now-29000),videoUrl:"https://objects.synthetic.test/private-video"}];await render();await act(async()=>handle.current!.showAlbum("media"));
  expect(host.textContent).toContain("https://objects.synthetic.test/private-video");expect(mocks.download).not.toHaveBeenCalled();
  await act(async()=>vi.advanceTimersByTimeAsync(2000));expect(mocks.previewProps.preview.visible).toBe(false);expect(host.textContent).not.toContain("private-video");expect(mocks.videoUnmount).toHaveBeenCalled();
 });
 it("does not fabricate expiry for private media without known read time, and keeps ordinary media download available",async()=>{
  mocks.items=[item(true)];await render();await act(async()=>handle.current!.showAlbum("media"));await act(async()=>vi.advanceTimersByTimeAsync(35000));
  expect(mocks.previewProps.preview.visible).toBe(true);expect(mocks.download).not.toHaveBeenCalled();
 });
 it("preserves an ordinary media preview and its existing download behavior",async()=>{
  mocks.items=[item(false)];await render();await act(async()=>handle.current!.showAlbum("media"));expect(mocks.previewProps.preview.visible).toBe(true);expect(mocks.download).toHaveBeenCalledOnce();
 });
 it("does not open a late album search after clear or export through an old same-user tenant closure",async()=>{
  let finish!:()=>void;mocks.load.mockImplementation(()=>new Promise<void>(resolve=>finish=resolve));await render();
  let pending!:Promise<void>;await act(async()=>{pending=handle.current!.showAlbum("media");});
  await act(async()=>{messageHistoryFence.invalidate();mocks.items=[item(false)];finish();await pending;});expect(mocks.previewProps.preview.visible).toBe(false);expect(mocks.download).not.toHaveBeenCalled();
  const old=handle.current!.showAlbum;mocks.session.generation++;mocks.session.current.tenant.serverID="B";await act(async()=>old("media"));expect(mocks.download).not.toHaveBeenCalled();
 });
});
