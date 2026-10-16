// @vitest-environment jsdom
import React from 'react';
import {ConfigProvider} from 'antd';
import {cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react';
import {afterEach,beforeAll,beforeEach,describe,expect,it,vi} from 'vitest';
import type {AppletRecord} from './appletManagement';
const api=vi.hoisted(()=>({create:vi.fn(),update:vi.fn()}));
vi.mock('@/services/server/app_manage',()=>({crteateApplet:api.create,updateApplet:api.update}));
vi.mock('@/services/admin/upload',()=>({uploadAdminFile:vi.fn()}));
import AppModal from '../pages/business_system/app_manage/AppModal';
beforeAll(()=>{
  window.matchMedia=vi.fn().mockImplementation(()=>({matches:false,addListener(){},removeListener(){},addEventListener(){},removeEventListener(){},dispatchEvent(){return true;}}));
  vi.stubGlobal('ResizeObserver',class{observe(){}unobserve(){}disconnect(){}});
  const computed=window.getComputedStyle.bind(window);vi.spyOn(window,'getComputedStyle').mockImplementation(element=>computed(element));
});
beforeEach(()=>{api.create.mockReset().mockResolvedValue({});api.update.mockReset().mockResolvedValue({});});
afterEach(cleanup);
const record:AppletRecord={id:'stable-owned',createTime:1,name:'原应用',appID:'__UNI__SYNTHETIC',icon:'',url:'https://objects.fixture.example/task.wgt',md5:'a'.repeat(32),size:86,version:'1.0.0',priority:0,status:2};
const mount=(existing?:AppletRecord)=>{
  const saved=vi.fn(),close=vi.fn();
  render(React.createElement(ConfigProvider,{theme:{token:{motion:false}}},React.createElement(AppModal,{isOpen:true,isCreate:!existing,record:existing,setIsOpen:close,onSaved:saved})));
  return {saved,close};
};
describe('applet metadata editor',()=>{
  it('does not submit an empty new form and cancel makes no management write',async()=>{
    const {close}=mount();fireEvent.click(screen.getByRole('button',{name:'保 存'}));
    await screen.findByText('请输入AppID');expect(api.create).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button',{name:'取 消'}));expect(close).toHaveBeenCalledWith(false);expect(api.update).not.toHaveBeenCalled();
  });
  it('updates only changed fields using actual wrapper JSON and refreshes after acknowledgement',async()=>{
    const {saved,close}=mount(record);
    fireEvent.change(await screen.findByLabelText('应用名称'),{target:{value:'更新名称'}});
    fireEvent.click(screen.getByRole('button',{name:'保 存'}));
    await waitFor(()=>expect(api.update).toHaveBeenCalledWith({id:'stable-owned',name:{value:'更新名称'}}));
    expect(api.create).not.toHaveBeenCalled();await waitFor(()=>expect(saved).toHaveBeenCalledOnce());expect(close).toHaveBeenCalledWith(false);
  });
  it('retains changed input after unknown update response and does not report success',async()=>{
    api.update.mockRejectedValue(new Error('synthetic connection loss'));
    const {saved,close}=mount(record);fireEvent.change(await screen.findByLabelText('应用名称'),{target:{value:'保留输入'}});
    fireEvent.click(screen.getByRole('button',{name:'保 存'}));
    await waitFor(()=>expect(api.update).toHaveBeenCalledOnce());
    await screen.findByText('提交结果未确认，请刷新列表核实后再操作；已保留填写内容');
    expect((screen.getByLabelText('应用名称') as HTMLInputElement).value).toBe('保留输入');expect(saved).not.toHaveBeenCalled();expect(close).not.toHaveBeenCalled();
  });
});
