// @vitest-environment jsdom
import React from 'react';
import {ConfigProvider} from 'antd';
import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react';
import {afterEach,beforeAll,beforeEach,describe,expect,it,vi} from 'vitest';
const api=vi.hoisted(()=>({status:vi.fn(),qr:vi.fn(),bind:vi.fn(),reset:vi.fn()}));
vi.mock('@/services/server/google_auth',()=>({getUserGoogleAuthStatus:api.status,getGoogleAuthQRCode:api.qr,bindGoogleAuth:api.bind,resetGoogleAuth:api.reset}));
import GoogleAuthModal from '../components/GoogleAuthModal';
beforeAll(()=>{
  window.matchMedia=vi.fn().mockImplementation(()=>({matches:false,addListener(){},removeListener(){},addEventListener(){},removeEventListener(){},dispatchEvent(){return true;}}));
  vi.stubGlobal('ResizeObserver',class{observe(){}unobserve(){}disconnect(){}});
  const computed=window.getComputedStyle.bind(window);vi.spyOn(window,'getComputedStyle').mockImplementation(element=>computed(element));
});
beforeEach(()=>{Object.values(api).forEach(fn=>fn.mockReset());});afterEach(cleanup);
const dialog=(userID:string)=>React.createElement(ConfigProvider,{theme:{token:{motion:false}}},
  React.createElement(GoogleAuthModal,{visible:true,userID,onCancel:vi.fn(),onSuccess:vi.fn()}));
describe('user authenticator target and enrollment boundary',()=>{
  it('reads unbound protobuf-default status without silently generating or rotating an enrollment secret',async()=>{
    api.status.mockResolvedValue({data:{}});render(dialog('owner-a'));
    await screen.findByRole('button',{name:'生成绑定二维码'});
    expect(api.qr).not.toHaveBeenCalled();expect(api.bind).not.toHaveBeenCalled();expect(api.reset).not.toHaveBeenCalled();
  });
  it('does not let an old status response replace the selected user status',async()=>{
    let finish!:(value:unknown)=>void;
    api.status.mockImplementation((id:string)=>id==='owner-a'?new Promise(resolve=>{finish=resolve;}):Promise.resolve({data:{enabled:true}}));
    const view=render(dialog('owner-a'));view.rerender(dialog('owner-b'));
    await screen.findByText('该用户已绑定身份验证器');
    await act(async()=>finish({data:{}}));
    expect(screen.getByText('该用户已绑定身份验证器')).toBeDefined();expect(screen.queryByRole('button',{name:'生成绑定二维码'})).toBeNull();
  });
  it('drops a late enrollment secret after switching targets and never binds it to the replacement user',async()=>{
    let finish!:(value:unknown)=>void;
    api.status.mockImplementation((id:string)=>Promise.resolve({data:id==='owner-b'?{enabled:true}:{}}));
    api.qr.mockImplementation(()=>new Promise(resolve=>{finish=resolve;}));
    const view=render(dialog('owner-a'));fireEvent.click(await screen.findByRole('button',{name:'生成绑定二维码'}));
    await waitFor(()=>expect(api.qr).toHaveBeenCalledWith('owner-a'));
    view.rerender(dialog('owner-b'));await screen.findByText('该用户已绑定身份验证器');
    await act(async()=>finish({data:{qrCodeURL:'otpauth://totp/synthetic?secret=NOTAREALSECRET'}}));
    expect(screen.queryByText(/NOTAREALSECRET/)).toBeNull();expect(api.bind).not.toHaveBeenCalled();expect(api.reset).not.toHaveBeenCalled();
  });
  it('shows unknown status on read failure and makes destructive or binding operations unavailable',async()=>{
    api.status.mockRejectedValue(new Error('synthetic network failure'));render(dialog('owner-a'));
    await screen.findByText('验证器状态暂不可用');expect(screen.queryByRole('button',{name:'清除绑定'})).toBeNull();expect(screen.queryByRole('button',{name:'生成绑定二维码'})).toBeNull();
    expect(api.qr).not.toHaveBeenCalled();expect(api.reset).not.toHaveBeenCalled();
  });
});
