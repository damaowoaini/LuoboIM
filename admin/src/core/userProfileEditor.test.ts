// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import UserProfileEditor from '../pages/business_system/user_manage/UserProfileEditor';
import type { UserListItem } from '../pages/business_system/user_manage/data';

beforeAll(() => {
  window.matchMedia = vi.fn().mockImplementation(() => ({matches:false,addListener(){},removeListener(){},addEventListener(){},removeEventListener(){},dispatchEvent(){return true;}}));
  vi.stubGlobal('ResizeObserver', class {observe(){} unobserve(){} disconnect(){}});
  const computed = window.getComputedStyle.bind(window);
  vi.spyOn(window,'getComputedStyle').mockImplementation(element => computed(element));
});
afterEach(cleanup);
const user = {userID:'synthetic-owner',nickname:'原昵称',gender:2,level:1,phoneNumber:'not-forwarded',birth:123} as UserListItem;
const mount = (onSave = vi.fn().mockResolvedValue(undefined), onCancel = vi.fn()) => {
  render(React.createElement(UserProfileEditor,{user,onSave,onCancel}));
  return {onSave,onCancel};
};

describe('responsive administrator profile editor',()=>{
  it('opens a separate labelled editor and cancel never changes server data',async()=>{
    const {onSave,onCancel}=mount();
    expect((await screen.findByLabelText('昵称') as HTMLInputElement).value).toBe('原昵称');
    expect(screen.getByRole('dialog').style.maxWidth).toBe('calc(100vw - 32px)');
    fireEvent.click(screen.getByRole('button',{name:'取 消'}));
    expect(onCancel).toHaveBeenCalledOnce();expect(onSave).not.toHaveBeenCalled();
  });
  it('validates blank nickname and retains the form without a request',async()=>{
    const {onSave,onCancel}=mount();
    fireEvent.change(await screen.findByLabelText('昵称'),{target:{value:'   '}});
    fireEvent.click(screen.getByRole('button',{name:'保 存'}));
    await screen.findByText('请输入昵称');
    expect(onSave).not.toHaveBeenCalled();expect(onCancel).not.toHaveBeenCalled();
  });
  it('sends only actual changes and prevents duplicate or cancelled pending saves',async()=>{
    let finish!:()=>void;
    const onSave=vi.fn().mockImplementation(()=>new Promise<void>(resolve=>{finish=resolve;}));
    const {onCancel}=mount(onSave);
    fireEvent.change(await screen.findByLabelText('昵称'),{target:{value:'新昵称'}});
    fireEvent.click(screen.getByRole('button',{name:'保 存'}));
    await waitFor(()=>expect(onSave).toHaveBeenCalledOnce());
    expect(onSave).toHaveBeenCalledWith({userID:user.userID,nickname:'新昵称'});
    expect((screen.getByRole('button',{name:'取 消'}) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByLabelText('昵称') as HTMLInputElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole('button',{name:/保 存/}));expect(onSave).toHaveBeenCalledOnce();
    finish();await waitFor(()=>expect(onCancel).toHaveBeenCalledOnce());
  });
  it('keeps unsaved input after a failed update so it can be corrected or retried',async()=>{
    const {onCancel}=mount(vi.fn().mockRejectedValue(new Error('synthetic failure')));
    fireEvent.change(await screen.findByLabelText('昵称'),{target:{value:'失败时保留'}});
    fireEvent.click(screen.getByRole('button',{name:'保 存'}));
    await waitFor(()=>expect((screen.getByLabelText('昵称') as HTMLInputElement).disabled).toBe(false));
    expect((screen.getByLabelText('昵称') as HTMLInputElement).value).toBe('失败时保留');
    expect(onCancel).not.toHaveBeenCalled();
  });
});
