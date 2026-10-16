// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import UserManagementActions from '../pages/business_system/user_manage/UserManagementActions';
import type { UserListItem } from '../pages/business_system/user_manage/data';

beforeAll(()=>{
  window.matchMedia=vi.fn().mockImplementation(()=>({matches:false,addListener(){},removeListener(){},addEventListener(){},removeEventListener(){},dispatchEvent(){return true;}}));
  vi.stubGlobal('ResizeObserver',class{observe(){}unobserve(){}disconnect(){}});
  const computed=window.getComputedStyle.bind(window);vi.spyOn(window,'getComputedStyle').mockImplementation(element=>computed(element));
});
afterEach(cleanup);
const mount=(onBlock=vi.fn().mockResolvedValue(undefined))=>{
  const handlers={onEdit:vi.fn(),onResetPassword:vi.fn(),onAuthenticator:vi.fn(),onBlock};
  render(React.createElement(ConfigProvider,{theme:{token:{motion:false}}},
    React.createElement(UserManagementActions,{user:{userID:'task-owned',nickname:'独立用户',forbidden:0} as UserListItem,...handlers})));
  return handlers;
};
describe('administrator row action safety',()=>{
  it('keeps edit primary and exposes password/security operations only in a labelled menu',async()=>{
    const handlers=mount();
    expect(screen.queryByText('重置密码')).toBeNull();
    fireEvent.click(screen.getByRole('button',{name:'编辑'}));expect(handlers.onEdit).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole('button',{name:/更多操作/}));
    fireEvent.click(await screen.findByRole('menuitem',{name:'重置密码'}));
    expect(handlers.onResetPassword).toHaveBeenCalledOnce();expect(handlers.onBlock).not.toHaveBeenCalled();
  });
  it('requires a target-specific confirmation and cancellation has no write',async()=>{
    const handlers=mount();
    fireEvent.click(screen.getByRole('button',{name:/更多操作/}));fireEvent.click(await screen.findByRole('menuitem',{name:'禁用用户'}));
    await screen.findByText('独立用户 · 用户ID：task-owned');
    expect(screen.getByText('禁用后该用户将无法正常使用账号，请确认处理对象。')).toBeDefined();
    expect(handlers.onBlock).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button',{name:'取 消'}));
    await waitFor(()=>expect(screen.queryAllByText('确认禁用用户')).toHaveLength(0));
    expect(handlers.onBlock).not.toHaveBeenCalled();
  });
  it('disables row actions with an explicit reason while an acknowledged operation is pending',async()=>{
    let finish!:()=>void;const handlers=mount(vi.fn().mockImplementation(()=>new Promise<void>(resolve=>{finish=resolve;})));
    fireEvent.click(screen.getByRole('button',{name:/更多操作/}));fireEvent.click(await screen.findByRole('menuitem',{name:'禁用用户'}));
    fireEvent.click(await screen.findByRole('button',{name:'确认禁用'}));
    await waitFor(()=>expect(handlers.onBlock).toHaveBeenCalledOnce());
    const edit=screen.getAllByTitle('该账号的操作正在提交，请稍候').find(node=>node.textContent==='编辑') as HTMLButtonElement;
    expect(edit.disabled).toBe(true);expect(edit.title).toBe('该账号的操作正在提交，请稍候');
    finish();await waitFor(()=>expect(screen.queryAllByText('确认禁用用户')).toHaveLength(0));
  });
});
