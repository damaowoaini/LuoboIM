// @vitest-environment jsdom
import React from 'react';
import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react';
import {Modal,message} from 'antd';
import {afterEach,beforeAll,beforeEach,describe,expect,it,vi} from 'vitest';
const mocks=vi.hoisted(()=>({batch:vi.fn(),owner:{generation:1,current:{imUserID:'system'},onChange:vi.fn(()=>()=>{})}}));
vi.mock('@/core/adminSession',()=>({adminSessions:mocks.owner}));
vi.mock('@/services/server/message_manage',()=>({batchSend:mocks.batch}));
vi.mock('@/services/server/user_manage',()=>({getUserList:vi.fn()}));
vi.mock('@/utils/avatar',()=>({DefaultAvatar:()=>null,defaultAvatarStr:()=>''}));
vi.mock('@ant-design/pro-components',()=>({PageContainer:({children}:{children:React.ReactNode})=>React.createElement('div',null,children),ProTable:()=>null}));
import SendMessage from '../pages/im_system/message_manage/SendMessage';
let confirm:undefined|(()=>unknown);
beforeAll(()=>{
 window.matchMedia=vi.fn().mockImplementation(()=>({matches:false,addListener(){},removeListener(){},addEventListener(){},removeEventListener(){},dispatchEvent(){return true;}}));
 vi.stubGlobal('ResizeObserver',class{observe(){}unobserve(){}disconnect(){}});
 const computed=window.getComputedStyle.bind(window);vi.spyOn(window,'getComputedStyle').mockImplementation(element=>computed(element));
});
beforeEach(()=>{vi.clearAllMocks();mocks.batch.mockReset();mocks.owner.generation=1;confirm=undefined;vi.spyOn(Modal,'confirm').mockImplementation(options=>{confirm=options.onOk as ()=>unknown;return {destroy(){},update(){}};});vi.spyOn(message,'success').mockImplementation(()=>undefined as never);vi.spyOn(message,'warning').mockImplementation(()=>undefined as never);});
afterEach(cleanup);
async function draft(){render(React.createElement(SendMessage));fireEvent.change(screen.getByPlaceholderText('请输入群发内容'),{target:{value:'retained synthetic content'}});fireEvent.click(screen.getByRole('checkbox',{name:'发送给所有已注册用户'}));fireEvent.click(screen.getByRole('button',{name:/^发\s*送$/}));expect(confirm).toBeDefined();}
describe('real batch-send page result handling',()=>{
 it('does not show success for HTTP0/all failedUserIDs and locks the original draft without automatic replay',async()=>{
  mocks.batch.mockResolvedValue({data:{results:null,failedUserIDs:['one','two']}});await draft();await act(async()=>{await confirm!();});
  await waitFor(()=>expect(screen.getByText(/已确认发送0位/)).toBeDefined());expect(message.success).not.toHaveBeenCalled();expect(mocks.batch).toHaveBeenCalledOnce();
  expect((screen.getByPlaceholderText('请输入群发内容') as HTMLTextAreaElement).value).toBe('retained synthetic content');expect((screen.getByRole('button',{name:/^发\s*送$/}) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole('button',{name:/^发\s*送$/}));expect(mocks.batch).toHaveBeenCalledOnce();expect(screen.getByText(/未确认用户：one、two/)).toBeDefined();
 });
 it('does not erase the frozen draft or retry when the network acknowledgement is unknown',async()=>{
  mocks.batch.mockRejectedValue(new Error('synthetic lost acknowledgement'));await draft();await act(async()=>{await confirm!();});
  await waitFor(()=>expect(screen.getByText(/发送结果未确认/)).toBeDefined());expect(message.success).not.toHaveBeenCalled();expect(mocks.batch).toHaveBeenCalledOnce();expect(screen.getByText('原消息：retained synthetic content')).toBeDefined();
 });
 it('does not publish a late success after the administrator session changed',async()=>{
  let finish!:(value:unknown)=>void;mocks.batch.mockImplementation(()=>new Promise(resolve=>{finish=resolve;}));await draft();let pending:unknown;await act(async()=>{pending=confirm!();await Promise.resolve();});mocks.owner.generation++;
  await act(async()=>{finish({data:{results:[{recvID:'one',clientMsgID:'client',serverMsgID:'server',sendTime:123}],failedUserIDs:null}});await pending;});
  expect(message.success).not.toHaveBeenCalled();expect(message.warning).not.toHaveBeenCalled();expect(mocks.batch).toHaveBeenCalledOnce();
 });
});
