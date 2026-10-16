// @vitest-environment jsdom
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
const mocks = vi.hoisted(() => ({
  session:{generation:0,current:{issuer:"https://synthetic-route.test",tenant:{serverID:"A"},userID:"same-user"}},
  sdk:{on:vi.fn(),off:vi.fn(),deleteMessage:vi.fn(),revokeMessage:vi.fn(),sendMessage:vi.fn(),findMessageList:vi.fn()},
  update:vi.fn(),remove:vi.fn(),quote:vi.fn(),revoked:vi.fn(),emit:vi.fn(),download:vi.fn(),toast:vi.fn(),saveEmoji:vi.fn(),getEmojis:vi.fn(),
  pause:vi.fn(),removeAudioSource:vi.fn(),play:vi.fn(),clipboard:vi.fn(),fetch:vi.fn(),saveFile:vi.fn(),
}));
// SDK enum values are pinned to the local wrapper declarations; do not initialize its Worker in component mocks.
vi.mock("open-im-sdk-wasm",()=>({MessageStatus:{Sending:1,Succeed:2,Failed:3},SessionType:{Single:1,WorkingGroup:3,Notification:4},CbEvents:{OnProgress:"OnProgress"},MessageType:{TextMessage:101,PictureMessage:102,VoiceMessage:103,VideoMessage:104,FileMessage:105,AtTextMessage:106,MergeMessage:107,CardMessage:108,LocationMessage:109,CustomMessage:110,QuoteMessage:114,FaceMessage:115,OANotification:1400,GroupAnnouncementUpdated:1519,RevokeMessage:2101}}));
vi.mock("react-i18next",()=>({useTranslation:()=>({t:(key:string)=>key})}));
vi.mock("i18next",()=>({default:{on:vi.fn()},t:(key:string)=>key}));
vi.mock("@/AntdGlobalComp",()=>({message:{error:vi.fn()}}));
vi.mock("@/core/session/browser",()=>({sessionCoordinator:mocks.session}));
vi.mock("@/layout/MainContentWrap",()=>({IMSDK:mocks.sdk}));
vi.mock("@/utils/events",()=>({default:{emit:mocks.emit}}));
vi.mock("@/utils/common",()=>({feedbackToast:mocks.toast,downloadFile:mocks.download,getDownloadTask:()=>undefined,escapeHtml:(s:string)=>s,sanitizeHtml:(s:string)=>s}));
vi.mock("@/utils/imCommon",()=>({isGroupSession:()=>false,formatLink:(s:string)=>s,formatMessageByType:()=>"normal"}));
vi.mock("@/utils/storage",()=>({getUserCustomEmojis:mocks.getEmojis,setUserCustomEmojis:mocks.saveEmoji}));
vi.mock("@/hooks/useCurrentMemberRole",()=>({useCurrentMemberRole:()=>({isNomal:true,isAdmin:false})}));
vi.mock("@/svg/VoiceIcon",()=>({default:()=>React.createElement("span",null,"voice")}));
vi.mock("@/store",()=>{
  const state={imageCache:{},downloadMap:{},pushNewMessage:vi.fn(),tryAddPreviewImg:vi.fn(),updateOneMessage:mocks.update,deleteAndPushOneMessage:vi.fn(),deleteOneMessage:mocks.remove,updateCheckMode:vi.fn(),selfInfo:{userID:"same-user"},currentGroupInfo:{ownerUserID:"same-user"},updateQuoteMessage:mocks.quote,addRevokedMessage:mocks.revoked};
  const hook=Object.assign((select:(value:any)=>unknown)=>select(state),{getState:()=>state});
  return {useUserStore:hook,useMessageStore:hook,useConversationStore:Object.assign(hook,{getState:()=>({currentConversation:{conversationID:"conversation",userID:"peer"}})}),getImageMessageSourceUrl:()=>"https://objects.synthetic.test/image.png"};
});
import MessageSuffix from "@/pages/chat/queryChat/MessageItem/MessageSuffix";
import MessageMenuContent from "@/pages/chat/queryChat/MessageItem/MessageMenuContent";
import VoiceMessageRender from "@/pages/chat/queryChat/MessageItem/VoiceMessageRender";
import MediaMessageRender from "@/pages/chat/queryChat/MessageItem/MediaMessageRender";
import QuoteMessageRenderer from "@/pages/chat/queryChat/MessageItem/QuoteMessageRenderer";
import { messagePrivacyState, messageHistoryFence } from "./messagePrivacyRuntime";
import { MessageStatus, MessageType } from "open-im-sdk-wasm";
import type { ExMessageItem } from "@/store";
let root:Root, host:HTMLDivElement;
const now=1800000000000;
function wire(fields:Partial<ExMessageItem>={}):ExMessageItem {return {clientMsgID:"synthetic",sendID:"same-user",sessionType:1,status:MessageStatus.Succeed,sendTime:now,contentType:MessageType.TextMessage,textElem:{content:"normal text"},...fields} as ExMessageItem;}
const privateInfo=(hasReadTime?:number)=>({isPrivateChat:true,burnDuration:30,hasReadTime});
async function render(element:React.ReactNode){await act(async()=>root.render(element));}
async function click(text:string){const button=[...host.querySelectorAll("button")].find(button=>button.textContent===text);expect(button).toBeDefined();await act(async()=>button!.click());}
beforeEach(()=>{
  Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true});vi.clearAllMocks();vi.useFakeTimers();vi.setSystemTime(now);
  mocks.session.generation++;mocks.session.current.tenant.serverID="A";mocks.session.current.issuer="https://synthetic-route.test";
  mocks.sdk.deleteMessage.mockResolvedValue({});mocks.sdk.revokeMessage.mockResolvedValue({});mocks.sdk.findMessageList.mockResolvedValue({data:{findResultItems:[]}});
  mocks.getEmojis.mockResolvedValue([]);mocks.saveEmoji.mockResolvedValue(undefined);mocks.play.mockResolvedValue(undefined);
  Object.defineProperty(navigator,"clipboard",{configurable:true,value:{writeText:mocks.clipboard,write:mocks.clipboard}});
  vi.stubGlobal("fetch",mocks.fetch);vi.stubGlobal("Audio",class {src="";onended:any;onpause:any;onplay:any;pause=mocks.pause;removeAttribute=mocks.removeAudioSource;play=mocks.play;});
  Object.defineProperty(window,"electronAPI",{configurable:true,value:{fileExists:()=>false,saveFileToDisk:mocks.saveFile}});
  host=document.createElement("div");document.body.append(host);root=createRoot(host);
});
afterEach(async()=>{await act(async()=>root.unmount());host.remove();vi.useRealTimers();vi.unstubAllGlobals();});

describe("actual message presentation and actions",()=>{
  it("shows the remaining known lifetime on remount and never deletes on timer or unmount",async()=>{
    const message=wire({isRead:true,attachedInfoElem:privateInfo(now-25000) as any});
    await render(React.createElement(MessageSuffix,{message,conversationID:"conversation",isSender:false}));expect(host.textContent).toContain("5s");
    await act(async()=>vi.advanceTimersByTimeAsync(6000));expect(host.textContent).not.toContain("s");
    await render(null);await render(React.createElement(MessageSuffix,{message,conversationID:"conversation",isSender:false}));
    expect(host.textContent).not.toContain("s");expect(mocks.sdk.deleteMessage).not.toHaveBeenCalled();expect(mocks.remove).not.toHaveBeenCalled();
  });
  it("does not manufacture a countdown from isRead without a known timestamp",async()=>{
    await render(React.createElement(MessageSuffix,{message:wire({isRead:true,attachedInfoElem:privateInfo() as any}),conversationID:"conversation",isSender:false}));
    await act(async()=>vi.advanceTimersByTimeAsync(35000));expect(host.textContent).toBe("");expect(mocks.sdk.deleteMessage).not.toHaveBeenCalled();
  });
  it("keeps delete available but hides retention actions for both private roots and actual private quoted descendants",async()=>{
    const nested=wire({quoteElem:{text:"reply",quoteMessage:wire({attachedInfoElem:privateInfo() as any})} as any});
    for(const message of [wire({attachedInfoElem:privateInfo() as any}),nested]){
      await render(React.createElement(MessageMenuContent,{message,conversationID:"conversation",closeMenu:vi.fn()}));
      const labels=[...host.querySelectorAll("button")].map(button=>button.textContent);
      expect(labels).toContain("placeholder.delete");expect(labels).not.toContain("placeholder.copy");expect(labels).not.toContain("placeholder.forward");expect(labels).not.toContain("placeholder.reply");
    }
  });
  it("does not dispatch old menu copy/forward/delete/revoke after a same-user issuer/tenant switch",async()=>{
    await render(React.createElement(MessageMenuContent,{message:wire(),conversationID:"conversation",closeMenu:vi.fn()}));
    mocks.session.generation++;mocks.session.current.tenant.serverID="B";
    for(const label of ["placeholder.copy","placeholder.forward","placeholder.delete","placeholder.revoke"]) await click(label);
    expect(mocks.clipboard).not.toHaveBeenCalled();expect(mocks.emit).not.toHaveBeenCalled();expect(mocks.sdk.deleteMessage).not.toHaveBeenCalled();expect(mocks.sdk.revokeMessage).not.toHaveBeenCalled();expect(mocks.toast).not.toHaveBeenCalled();
  });
  it("does not dispatch an old still-mounted menu after clear invalidated its history revision",async()=>{
    await render(React.createElement(MessageMenuContent,{message:wire(),conversationID:"conversation",closeMenu:vi.fn()}));
    const oldButtons=[...host.querySelectorAll("button")].filter(button=>["placeholder.delete","placeholder.revoke","placeholder.forward"].includes(button.textContent??""));
    expect(oldButtons).toHaveLength(3);
    await act(async()=>{messageHistoryFence.invalidate();oldButtons.forEach(button=>button.click());});
    expect(mocks.sdk.deleteMessage).not.toHaveBeenCalled();expect(mocks.sdk.revokeMessage).not.toHaveBeenCalled();expect(mocks.emit).not.toHaveBeenCalled();
  });
  it("waits for clipboard acknowledgement and reports a rejected write as failure",async()=>{
    let reject!:(error:Error)=>void;mocks.clipboard.mockImplementation(()=>new Promise((_resolve,fail)=>reject=fail));
    await render(React.createElement(MessageMenuContent,{message:wire(),conversationID:"conversation",closeMenu:vi.fn()}));
    await click("placeholder.copy");expect(mocks.toast).not.toHaveBeenCalled();
    await act(async()=>reject(new Error("permission denied")));expect(mocks.toast).toHaveBeenCalledWith(expect.objectContaining({msg:"toast.copyFailed",error:expect.any(Error)}));
  });
  it("does not commit a late emoji download to the next account's cache or emoji storage",async()=>{
    let finish!:(blob:Blob)=>void;mocks.fetch.mockResolvedValue({blob:()=>new Promise(resolve=>finish=resolve)});
    const message=wire({contentType:MessageType.PictureMessage,pictureElem:{sourcePath:"",sourcePicture:{url:"https://objects.synthetic.test/image.png",width:1,height:1}} as any});
    await render(React.createElement(MessageMenuContent,{message,conversationID:"conversation",closeMenu:vi.fn()}));await click("placeholder.add");
    mocks.session.generation++;await act(async()=>finish(new Blob(["synthetic"],{type:"image/png"})));
    expect(mocks.saveFile).not.toHaveBeenCalled();expect(mocks.saveEmoji).not.toHaveBeenCalled();expect(mocks.toast).not.toHaveBeenCalled();
  });
  it("does not write a late image blob after authoritative privacy metadata was learned",async()=>{
    let finish!:(blob:Blob)=>void;mocks.fetch.mockResolvedValue({blob:()=>new Promise(resolve=>finish=resolve)});
    const message=wire({contentType:MessageType.PictureMessage,pictureElem:{sourcePath:"",sourcePicture:{url:"https://objects.synthetic.test/image.png",width:1,height:1}} as any});
    await render(React.createElement(MessageMenuContent,{message,conversationID:"conversation",closeMenu:vi.fn()}));await click("placeholder.copy");
    messagePrivacyState({...message,attachedInfoElem:privateInfo() as any},"conversation");
    await act(async()=>finish(new Blob(["synthetic"],{type:"image/png"})));
    expect(mocks.clipboard).not.toHaveBeenCalled();expect(mocks.toast).not.toHaveBeenCalled();
  });
  it("plays unexpired private voice, then pauses and releases its source at the known deadline",async()=>{
    const message=wire({contentType:MessageType.VoiceMessage,soundElem:{sourceUrl:"https://objects.synthetic.test/voice",duration:1} as any,attachedInfoElem:privateInfo(now-29000) as any});
    await render(React.createElement(VoiceMessageRender,{message,isSender:false,conversationID:"conversation"}));
    await act(async()=>host.querySelector("div")!.click());expect(mocks.play).toHaveBeenCalledOnce();
    await act(async()=>vi.advanceTimersByTimeAsync(2000));expect(host.textContent).toContain("消息已过期");expect(mocks.pause).toHaveBeenCalled();expect(mocks.removeAudioSource).toHaveBeenCalledWith("src");
  });
  it("shows an unexpired private picture and permits viewing without auto-saving a thumbnail cache",async()=>{
    const showAlbum=vi.fn();const message=wire({contentType:MessageType.PictureMessage,pictureElem:{sourcePath:"",sourcePicture:{url:"https://objects.synthetic.test/full",width:100,height:100},snapshotPicture:{url:"https://objects.synthetic.test/thumb",height:100}} as any,attachedInfoElem:privateInfo(now) as any});
    await render(React.createElement(MediaMessageRender,{message,isSender:false,conversationID:"conversation",showAlbum}));
    const image=host.querySelector("img");expect(image).not.toBeNull();await act(async()=>image!.click());expect(showAlbum).toHaveBeenCalledWith("synthetic");expect(mocks.download).not.toHaveBeenCalled();
  });
  it("redacts an actual nested private quote without exposing its original text or fetching its attachment",async()=>{
    const quoted=wire({textElem:{content:"synthetic-private-quote"},attachedInfoElem:privateInfo() as any});
    await render(React.createElement(QuoteMessageRenderer,{message:wire({quoteElem:{text:"normal reply",quoteMessage:quoted} as any}),isSender:false,conversationID:"conversation"}));
    expect(host.textContent).toContain("不可保留");expect(host.textContent).not.toContain("synthetic-private-quote");expect(mocks.fetch).not.toHaveBeenCalled();
  });
});
