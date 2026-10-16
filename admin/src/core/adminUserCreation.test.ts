// @vitest-environment jsdom
import React from 'react';
import {beforeAll,beforeEach,afterEach,describe,it,expect,vi}from 'vitest';
import {render,screen,fireEvent,waitFor,cleanup}from '@testing-library/react';
const mocks=vi.hoisted(()=>({register:vi.fn(),query:vi.fn(),resolve:vi.fn(),sessions:{generation:1,current:{issuer:'https://a.synthetic.test',account:'synthetic-admin'}}}));
vi.mock('@/core/adminSession',()=>({adminSessions:mocks.sessions}));
vi.mock('@/services/server/user_manage',()=>({registerUser:mocks.register,getAdminProvisioningStatus:mocks.query,resolveAdminProvisioning:mocks.resolve}));
vi.mock('@/services/admin/upload',()=>({handleFileUpload:vi.fn()}));
import AdminUserCreation from '../pages/business_system/user_manage/AdminUserCreation';
import {ProvisioningJournal}from './adminProvisioning';
const id='11111111-1111-4111-8111-111111111111';
beforeAll(()=>{
 window.matchMedia=vi.fn().mockImplementation(()=>({matches:false,addListener(){},removeListener(){},addEventListener(){},removeEventListener(){},dispatchEvent(){return true;}}));
 vi.stubGlobal('ResizeObserver',class{observe(){}unobserve(){}disconnect(){}});
 const computed=window.getComputedStyle.bind(window);vi.spyOn(window,'getComputedStyle').mockImplementation(element=>computed(element));
 Object.defineProperty(navigator,'locks',{value:{request:(_key:string,callback:()=>unknown)=>Promise.resolve().then(callback)},configurable:true});
});
beforeEach(()=>{localStorage.clear();mocks.register.mockReset();mocks.query.mockReset();mocks.resolve.mockReset();mocks.sessions.generation=1;vi.spyOn(crypto,'randomUUID').mockReturnValue(id);});
afterEach(()=>{cleanup();vi.restoreAllMocks();});
async function submit(){
 fireEvent.change(await screen.findByPlaceholderText('请输入用户昵称'),{target:{value:'合成开户'}});
 fireEvent.change(screen.getByPlaceholderText('请输入用户手机号'),{target:{value:'00000000001'}});
 fireEvent.change(screen.getByPlaceholderText('请输入用户密码'),{target:{value:'synthetic-test-password'}});
 fireEvent.click(screen.getByRole('button',{name:'添 加'}));
}
describe('actual creation UI uncertainty and recovery boundaries',()=>{
 it('freezes a durable ID before one dispatch and reports created pending without claiming bound',async()=>{
  let finish!:(value:any)=>void;mocks.register.mockImplementation(()=>new Promise(resolve=>{finish=resolve;}));
  const reload=vi.fn();render(React.createElement(AdminUserCreation,{reload}));await submit();
  await waitFor(()=>expect(mocks.register).toHaveBeenCalledOnce());
  expect(mocks.register.mock.calls[0][0].adminRequestID).toBe(id);
  const stored=localStorage.getItem(new ProvisioningJournal(localStorage,mocks.sessions.current.issuer,mocks.sessions.current.account,()=>id).key)!;
  expect(stored).toContain(id);expect(stored).not.toMatch(/password|00000000001|合成开户|token/i);
  expect(screen.getByRole('button',{name:/查询开户结果/}).getAttribute('disabled')).not.toBeNull();
  finish({data:{userID:'synthetic-user',adminRequestID:id,provisioningState:'pending',provisioningPending:true}});
  await screen.findByText('账号已创建，登录路由待确认');await waitFor(()=>expect(reload).toHaveBeenCalledOnce());
  expect(localStorage.getItem(new ProvisioningJournal(localStorage,mocks.sessions.current.issuer,mocks.sessions.current.account,()=>id).key)).toContain(id);
  fireEvent.click(screen.getByRole('button',{name:'继续添加用户'}));expect((await screen.findByPlaceholderText('请输入用户密码') as HTMLInputElement).value).toBe('');
 });
 it('preserves an unknown-ACK ID across close/reopen and queries without persisting credentials',async()=>{
  mocks.register.mockRejectedValue(new Error('synthetic network failure'));
  const mount=render(React.createElement(AdminUserCreation,{reload:vi.fn()}));await submit();
  await screen.findByText('请求结果未确认。请查询同一请求编号，不要重新开户。');mount.unmount();
  mocks.query.mockResolvedValue({data:{found:false,state:'unknown',definitiveNotCreated:false}});
  render(React.createElement(AdminUserCreation,{reload:vi.fn()}));expect(screen.queryByPlaceholderText('请输入用户密码')).toBeNull();
  fireEvent.click(screen.getByRole('button',{name:/查询开户结果/}));await waitFor(()=>expect(mocks.query).toHaveBeenCalledWith(id));
  await screen.findByText('尚未取得权威开户结果，请保留请求编号并继续查询，勿重新开户');
  expect(mocks.register).toHaveBeenCalledOnce();expect(screen.queryByRole('button',{name:'继续添加用户'})).toBeNull();
 });
 it('does not dispatch when request ID persistence fails',async()=>{
  const set=vi.spyOn(Storage.prototype,'setItem').mockImplementation(()=>{throw new Error('synthetic quota');});
  render(React.createElement(AdminUserCreation,{reload:vi.fn()}));await submit();
  await screen.findByText('无法保存开户请求编号，尚未发送开户请求。');expect(mocks.register).not.toHaveBeenCalled();set.mockRestore();
 });
 it('drops a late created result after administrator generation changes',async()=>{
  let finish!:(value:any)=>void;mocks.register.mockImplementation(()=>new Promise(resolve=>{finish=resolve;}));
  const reload=vi.fn();render(React.createElement(AdminUserCreation,{reload}));await submit();
  await waitFor(()=>expect(mocks.register).toHaveBeenCalledOnce());mocks.sessions.generation++;
  finish({data:{userID:'synthetic-user',adminRequestID:id,provisioningState:'bound'}});
  await waitFor(()=>expect(screen.getByRole('button',{name:/查询开户结果/}).getAttribute('disabled')).toBeNull());
  expect(reload).not.toHaveBeenCalled();expect(screen.queryByText('账号已创建，登录路由已确认')).toBeNull();
 });
 it('requires explicit stop confirmation and server tombstone before offering a new intent',async()=>{
  const journal=new ProvisioningJournal(localStorage,mocks.sessions.current.issuer,mocks.sessions.current.account,()=>id);await journal.reserve();
  mocks.resolve.mockResolvedValue({data:{found:true,state:'rejected',definitiveNotCreated:true,created:false,adminRequestID:id}});
  render(React.createElement(AdminUserCreation,{reload:vi.fn()}));
  fireEvent.click(screen.getByRole('button',{name:'停止此意图并重新填写'}));
  expect(mocks.resolve).not.toHaveBeenCalled();
  fireEvent.click(await screen.findByRole('button',{name:'确认停止'}));
  await screen.findByText('服务端已确认未创建账号，可以开始新的开户');expect(mocks.resolve).toHaveBeenCalledExactlyOnceWith(id);
  expect(journal.read().requestIDs).toEqual([]);expect(mocks.register).not.toHaveBeenCalled();
  await waitFor(()=>expect((screen.getByRole('button',{name:'继续添加用户'})as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole('button',{name:'继续添加用户'}));expect(await screen.findByPlaceholderText('请输入用户密码')).not.toBeNull();
 });
});
