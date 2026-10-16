// @vitest-environment jsdom
import React from 'react';
import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react';
import {message} from 'antd';
import {afterEach,beforeAll,beforeEach,describe,expect,it,vi} from 'vitest';
const mocks=vi.hoisted(()=>({upload:vi.fn(),update:vi.fn(),create:vi.fn(),listeners:new Set<()=>void>(),owner:{generation:1,current:{account:'synthetic'}}}));
vi.mock('@/core/adminSession',()=>({adminSessions:{get generation(){return mocks.owner.generation;},get current(){return mocks.owner.current;},onChange:(listener:()=>void)=>{mocks.listeners.add(listener);return()=>mocks.listeners.delete(listener);}}}));
vi.mock('@/services/admin/upload',()=>({uploadAdminFile:mocks.upload}));
vi.mock('@/services/server/group_manage',()=>({createGroup:mocks.create,updateGroup:mocks.update}));
vi.mock('@/pages/im_system/group_manage/SelectUserModal',()=>({default:()=>null}));
import GroupActionDrawer from '../pages/im_system/group_manage/GroupActionDrawer';
import type {GroupInfo} from '../pages/im_system/group_manage/data';
const original={groupID:'owned-group',groupName:'原群',faceURL:'https://objects.synthetic.test/old.png',ex:'{"keep":"custom"}',notification:'原公告',introduction:'原简介',needVerification:1,lookMemberInfo:0,applyMemberFriend:1} as GroupInfo;
const uploaded='https://objects.synthetic.test/new.png';
beforeAll(()=>{
 window.matchMedia=vi.fn().mockImplementation(()=>({matches:false,addListener(){},removeListener(){},addEventListener(){},removeEventListener(){},dispatchEvent(){return true;}}));vi.stubGlobal('ResizeObserver',class{observe(){}unobserve(){}disconnect(){}});
 const computed=window.getComputedStyle.bind(window);vi.spyOn(window,'getComputedStyle').mockImplementation(element=>computed(element));
});
beforeEach(()=>{vi.clearAllMocks();mocks.owner.generation=1;mocks.listeners.clear();mocks.upload.mockReset().mockResolvedValue({url:uploaded});mocks.update.mockReset().mockResolvedValue({data:{}});mocks.create.mockReset().mockResolvedValue({data:{}});vi.spyOn(message,'success').mockImplementation(()=>undefined as never);});
afterEach(cleanup);
function mount(){const close=vi.fn(),reload=vi.fn();const result=render(React.createElement(GroupActionDrawer,{drawerOptions:{visible:true,opType:'edit',groupInfo:original},closeDrawer:close,reload}));return {...result,close,reload};}
async function upload(){const input=document.querySelector('input[type=file]') as HTMLInputElement;fireEvent.change(input,{target:{files:[new File(['synthetic-only'],'avatar.png',{type:'image/png'})]}});await waitFor(()=>expect(mocks.upload).toHaveBeenCalledOnce());}
describe('actual group settings page payload and ownership',()=>{
 it('places the completed new avatar into nested groupInfoForSet and preserves other raw codec fields',async()=>{
  const {close,reload}=mount();await upload();await waitFor(()=>expect(document.querySelector(`img[src="${uploaded}"]`)).not.toBeNull());fireEvent.click(screen.getByRole('button',{name:'确 定'}));
  await waitFor(()=>expect(mocks.update).toHaveBeenCalledOnce());expect(mocks.update.mock.calls[0][0]).toMatchObject({groupInfoForSet:{...original,faceURL:uploaded}});expect(mocks.update.mock.calls[0][0]).not.toHaveProperty('faceURL');
  expect(mocks.update.mock.calls[0][0].groupInfoForSet.needVerification).toBe(1);expect(mocks.update.mock.calls[0][0].groupInfoForSet.lookMemberInfo).toBe(0);expect(mocks.update.mock.calls[0][0].groupInfoForSet.ex).toBe(original.ex);
  await waitFor(()=>expect(close).toHaveBeenCalledOnce());expect(reload).toHaveBeenCalledOnce();expect(mocks.create).not.toHaveBeenCalled();
 });
 it('does not show success or close after an unknown save and retains the edited data',async()=>{
  mocks.update.mockRejectedValue(new Error('unknown acknowledgement'));const {close,reload}=mount();fireEvent.change(await screen.findByLabelText('名称'),{target:{value:'保留群名'}});fireEvent.click(screen.getByRole('button',{name:'确 定'}));
  await screen.findByText('保存结果未确认，请刷新群列表核实后再操作；已保留填写内容和上传头像。');expect(message.success).not.toHaveBeenCalled();expect(close).not.toHaveBeenCalled();expect(reload).not.toHaveBeenCalled();expect((screen.getByLabelText('名称') as HTMLInputElement).value).toBe('保留群名');
 });
 it('keeps the original avatar after upload failure instead of claiming an upload or save succeeded',async()=>{
  mocks.upload.mockRejectedValue(new Error('synthetic upload failure'));mount();await upload();await screen.findByText('头像上传未完成，未替换原头像；请重新选择图片。');expect(mocks.update).not.toHaveBeenCalled();expect(message.success).not.toHaveBeenCalled();expect(document.querySelector(`img[src="${original.faceURL}"]`)).not.toBeNull();
 });
 it('aborts the page-owned upload on unmount and ignores its late completion',async()=>{
  let finish!:(value:unknown)=>void;mocks.upload.mockImplementation(()=>new Promise(resolve=>{finish=resolve;}));const view=mount();await upload();const signal=mocks.upload.mock.calls[0][2] as AbortSignal;view.unmount();expect(signal.aborted).toBe(true);
  await act(async()=>{finish({url:uploaded});});expect(mocks.update).not.toHaveBeenCalled();expect(message.success).not.toHaveBeenCalled();
 });
 it('does not apply an upload or save response to the next administrator session',async()=>{
  let finish!:(value:unknown)=>void;mocks.upload.mockImplementation(()=>new Promise(resolve=>{finish=resolve;}));const {close,reload}=mount();await upload();
  await act(async()=>{mocks.owner.generation++;for(const listener of mocks.listeners)listener();finish({url:uploaded});});expect(document.querySelector(`img[src="${uploaded}"]`)).toBeNull();expect(mocks.update).not.toHaveBeenCalled();expect(close).not.toHaveBeenCalled();expect(reload).not.toHaveBeenCalled();expect(message.success).not.toHaveBeenCalled();
 });
 it('ignores a late save acknowledgement after the administrator rotated',async()=>{
  let finish!:(value:unknown)=>void;mocks.update.mockImplementation(()=>new Promise(resolve=>{finish=resolve;}));const {close,reload}=mount();fireEvent.click(screen.getByRole('button',{name:'确 定'}));await waitFor(()=>expect(mocks.update).toHaveBeenCalledOnce());
  await act(async()=>{mocks.owner.generation++;for(const listener of mocks.listeners)listener();finish({data:{}});});expect(close).not.toHaveBeenCalled();expect(reload).not.toHaveBeenCalled();expect(message.success).not.toHaveBeenCalled();
 });
});
