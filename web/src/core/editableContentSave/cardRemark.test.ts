// @vitest-environment jsdom
// Actual UserCard modal and SendRequest; SDK/API/session and presentation/OS
// primitives are adapted. No network, real friend request, login, or navigation.
import React,{act,createRef} from "react";
import {createRoot,type Root} from "react-dom/client";
import {afterEach,beforeEach,describe,expect,it,vi} from "vitest";
import type {OverlayVisibleHandle} from "@/hooks/useOverlayVisible";
let pending:Promise<void>|undefined;
const f=vi.hoisted(()=>({sdk:vi.fn(),business:vi.fn(),add:vi.fn(),members:vi.fn(),remark:vi.fn(),toChat:vi.fn(),feedback:vi.fn(),warning:vi.fn(),listeners:new Set<()=>void>(),session:{current:{owner:"A"} as object|null,generation:1},self:{userID:"self",nickname:"Self"},allow:1}));
vi.mock("@/api/login",()=>({BusinessAllowType:{Allow:1,NotAllow:2},getBusinessUserInfo:f.business}));
vi.mock("@/core/session/browser",()=>({sessionCoordinator:{get current(){return f.session.current},get generation(){return f.session.generation},onChange:(cb:()=>void)=>{f.listeners.add(cb);return()=>f.listeners.delete(cb)}}}));
vi.mock("@/layout/MainContentWrap",()=>({IMSDK:{getUsersInfoWithCache:f.sdk,getSpecifiedGroupMembersInfo:f.members,addFriend:f.add,setFriendRemark:f.remark,on:vi.fn(),off:vi.fn()}}));
vi.mock("@/store",()=>({useUserStore:Object.assign((selector:any)=>selector({selfInfo:f.self,appConfig:{allowSendMsgNotFriend:f.allow}}),{getState:()=>({selfInfo:f.self})})}));
vi.mock("@/hooks/useConversationToggle",()=>({useConversationToggle:()=>({toSpecifiedConversation:f.toChat})}));
vi.mock("@/utils/common",()=>({feedbackToast:f.feedback}));
vi.mock("@/AntdGlobalComp",()=>({message:{warning:f.warning}}));
vi.mock("i18next",()=>({t:(key:string)=>key}));
vi.mock("open-im-sdk-wasm",()=>({CbEvents:{OnFriendAdded:"friendAdded"},GroupJoinSource:{Invitation:2,QrCode:3},SessionType:{Single:1}}));
vi.mock("react-use",()=>({useCopyToClipboard:()=>[null,vi.fn()]}));
vi.mock("ahooks",()=>({useRequest:(fn:any)=>({runAsync:fn,loading:false}),useClickAway:()=>{}}));
vi.mock("@ant-design/icons",()=>({EnterOutlined:()=>null,LeftOutlined:({onClick}:any)=>React.createElement("button",{onClick,"aria-label":"back"},"back")}));
vi.mock("antd",()=>({Button:({children,onClick,disabled,loading}:any)=>React.createElement("button",{onClick,disabled:disabled||loading},children),Spin:({children}:any)=>children,Divider:()=>null,Modal:()=>null,Input:Object.assign(React.forwardRef<HTMLInputElement,any>(({value,onChange,onPressEnter,disabled},ref)=>React.createElement("input",{ref,value:value??"",onChange,disabled,onKeyDown:(event:any)=>{if(event.key==="Enter")pending=onPressEnter(event);}})),{TextArea:({value,onChange}:any)=>React.createElement("textarea",{value,onChange})})}));
vi.mock("@/components/DraggableModalWrap",()=>({default:({open,children,onCancel}:any)=>open?React.createElement("section",{"aria-label":"user card"},React.createElement("button",{onClick:onCancel,"aria-label":"close card"},"close"),children):null}));
vi.mock("@/components/OIMAvatar",()=>({default:()=>null}));
vi.mock("@/components/UserLoginInfo",()=>({default:()=>null}));
vi.mock("@/components/UserLoginRecordList",()=>({default:()=>null}));
// These paths are resolved relative to the aliased actual modal in each stage.
vi.mock("@/pages/common/UserCardModal/CardActionRow",()=>({default:()=>null}));
vi.mock("@/pages/common/UserCardModal/EditSelfInfo",()=>({default:React.forwardRef(()=>null)}));
import UserCard from "@/pages/common/UserCardModal";
let host:HTMLDivElement,root:Root,ref:React.RefObject<OverlayVisibleHandle|null>;
beforeEach(()=>{Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true});vi.resetAllMocks();f.listeners.clear();f.session.current={userID:"self",issuer:"issuer-A",tenant:{serverID:"A"}};f.session.generation=1;f.allow=1;f.sdk.mockResolvedValue({data:[{publicInfo:{userID:"target",nickname:"Correct"}}]});f.business.mockResolvedValue({data:{users:[{userID:"target",nickname:"Correct",allowAddFriend:1}]}});f.add.mockResolvedValue({data:{}});f.toChat.mockResolvedValue(undefined);f.remark.mockResolvedValue({data:{}});pending=undefined;host=document.createElement("div");document.body.append(host);root=createRoot(host);ref=createRef();});
afterEach(async()=>{await act(async()=>root.unmount());host.remove()});
async function render(props:any={}){await act(async()=>root.render(React.createElement(UserCard,{ref,userID:"target",...props})))}
async function open(props:any={}){await render(props);await act(async()=>ref.current!.openOverlay())}
function button(key:string){return [...host.querySelectorAll("button")].find(x=>x.textContent===key)}
async function click(key:string){expect(button(key)).toBeDefined();await act(async()=>button(key)!.click())}
function deferred(){let resolve!:(v:any)=>void;const promise=new Promise<any>(r=>resolve=r);return{promise,resolve}}
function rotate(){f.session.current={userID:"selfB",issuer:"issuer-B",tenant:{serverID:"B"}};f.session.generation++;for(const cb of f.listeners)cb()}
describe("actual friend remark editor acknowledgement",()=>{
 async function editRemark(value="remark draft"){
  f.sdk.mockResolvedValue({data:[{friendInfo:{userID:"target",nickname:"Friend",remark:"old remark"}}]});
  await open();await act(async()=>host.querySelector<HTMLImageElement>('img[alt="edit name"]')!.click());
  const input=host.querySelector<HTMLInputElement>("input")!;input.value=value;return input;
 }
 async function submit(input:HTMLInputElement){await act(async()=>{input.dispatchEvent(new KeyboardEvent("keydown",{key:"Enter",bubbles:true}));await pending;});}
 it("keeps normal successful friend remark updates",async()=>{
  const input=await editRemark();await submit(input);
  expect(f.remark).toHaveBeenCalledExactlyOnceWith({toUserID:"target",remark:"remark draft"});
  expect(host.querySelector("input")).toBeNull();expect(host.textContent).toContain("remark draft");
 });
 it("retains the remark draft after a real SDK rejection and lets explicit retry succeed",async()=>{
  f.remark.mockRejectedValueOnce(new Error("remark failed"));const input=await editRemark();await submit(input);
  expect(f.remark).toHaveBeenCalledOnce();expect(f.feedback).toHaveBeenCalledOnce();
  expect(host.querySelector<HTMLInputElement>("input")?.value).toBe("remark draft");
  await submit(host.querySelector<HTMLInputElement>("input")!);expect(f.remark).toHaveBeenCalledTimes(2);
  expect(host.querySelector("input")).toBeNull();
 });
 it("does not submit a retained remark editor after the normal Card closes",async()=>{
  const input=await editRemark();await act(async()=>ref.current!.closeOverlay());await submit(input);
  expect(f.remark).not.toHaveBeenCalled();
 });
});
