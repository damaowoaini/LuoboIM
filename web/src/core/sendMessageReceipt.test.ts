// @vitest-environment jsdom
import React,{act} from 'react';
import {createRoot,type Root} from 'react-dom/client';
import {afterEach,beforeEach,describe,expect,it,vi} from 'vitest';
const mocks=vi.hoisted(()=>({session:{generation:1,current:{issuer:"https://route.synthetic.test",tenant:{serverID:"A"},userID:"owner"}},send:vi.fn(),push:vi.fn(),update:vi.fn(),preview:vi.fn(),replace:vi.fn()}));
vi.mock('@/core/session/browser',()=>({sessionCoordinator:mocks.session}));
vi.mock('open-im-sdk-wasm',()=>({MessageStatus:{Failed:3}}));
vi.mock('@/layout/MainContentWrap',()=>({IMSDK:{sendMessage:mocks.send}}));
vi.mock('@/utils/events',()=>({default:{emit:vi.fn()}}));
vi.mock('@/store',()=>({useMessageStore:(selector:(state:any)=>unknown)=>selector({pushNewMessage:mocks.push,updateOneMessage:mocks.update,tryAddPreviewImg:mocks.preview,deleteAndPushOneMessage:mocks.replace}),useConversationStore:Object.assign((selector:(state:any)=>unknown)=>selector({currentConversation:{conversationID:'conversation',conversationType:1,userID:'target',groupID:''}}),{getState:()=>({currentConversation:{conversationID:'conversation',conversationType:1,userID:'target',groupID:''},captureConversationSendScope:()=>({target:{kind:'user' as const,id:'target',conversationID:'conversation'},current:()=>true,canSend:()=>true})})})}));
import {captureMessageRetention,messagePrivacyState,messageHistoryFence} from './messagePrivacyRuntime';
import {useSendMessage} from '@/pages/chat/queryChat/ChatFooter/useSendMessage';
import type {ExMessageItem} from '@/store';
let generationCounter=1;
let root:Root,host:HTMLDivElement,send:(params:Omit<Parameters<ReturnType<typeof useSendMessage>['sendMessage']>[0],'intent'>)=>ReturnType<ReturnType<typeof useSendMessage>['sendMessage']>;
beforeEach(async()=>{
 Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true});vi.clearAllMocks();mocks.send.mockReset();mocks.session.generation=++generationCounter;
 host=document.createElement('div');document.body.append(host);root=createRoot(host);
 function Probe(){const hook=useSendMessage(),intent=hook.beginCurrentSend()!;send=params=>hook.sendMessage({...params,intent});return null;}
 await act(async()=>root.render(React.createElement(Probe)));
});
afterEach(async()=>{await act(async()=>root.unmount());host.remove();});
const message={clientMsgID:'synthetic-client',contentType:101,status:1} as ExMessageItem;
describe('actual message hook delivery receipts',()=>{
 it('returns an explicit unconfirmed outcome for a failed SDK send while keeping its failed bubble',async()=>{
  mocks.send.mockRejectedValue(new Error('unknown acknowledgement'));const receipt=await send({message});
  expect(receipt.state).toBe('unconfirmed');expect(mocks.update).toHaveBeenCalledWith({...message,status:3});expect(mocks.preview).not.toHaveBeenCalled();expect(mocks.send).toHaveBeenCalledOnce();
 });
 it('preserves an observed successful ACK without applying it to the next session store',async()=>{
  let finish!:(value:unknown)=>void;mocks.send.mockImplementation(()=>new Promise(resolve=>{finish=resolve;}));const pending=send({message});for(let i=0;i<8;i++)await Promise.resolve();expect(mocks.send).toHaveBeenCalledOnce();mocks.session.generation++;
  finish({data:{...message,status:2}});expect((await pending).state).toBe('sent');expect(mocks.update).not.toHaveBeenCalled();expect(mocks.preview).not.toHaveBeenCalled();
 });
 it('does not dispatch at all from a retired hook generation',async()=>{
  mocks.session.generation++;expect((await send({message})).state).toBe('stale');expect(mocks.send).not.toHaveBeenCalled();expect(mocks.push).not.toHaveBeenCalled();
 });
});

describe('source retention at the actual send commit',()=>{
 it('does not send a prepared forward after its original source becomes known private',async()=>{
  const source={clientMsgID:'source-original'};const retention=captureMessageRetention(source,'conversation');
  messagePrivacyState({...source,attachedInfoElem:{isPrivateChat:true}},'conversation');
  expect((await send({message,sourceRetention:retention})).state).toBe('stale');
  expect(mocks.send).not.toHaveBeenCalled();expect(mocks.push).not.toHaveBeenCalled();
 });
 it('does not dispatch a prepared message across global history clear',async()=>{
  const retention=captureMessageRetention({clientMsgID:'normal-source'},'conversation');
  const finish=messageHistoryFence.beginClear(JSON.stringify([mocks.session.generation,mocks.session.current.issuer,mocks.session.current.tenant.serverID,mocks.session.current.userID]));finish();
  expect((await send({message,sourceRetention:retention})).state).toBe('stale');expect(mocks.send).not.toHaveBeenCalled();
 });
});
