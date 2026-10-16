import { ACCOUNT_URL } from '@/config';
import type { AdminSession } from '@/core/adminSession';
import { request } from '@umijs/max';

export async function adminLogin(account: string, password: string, googleAuthCode?: string) {
  return request<{
    data: any;
  }>('account/login', {
    method: 'POST',
    data: {
      account,
      password,
      googleAuthCode,
    },
    baseURL: ACCOUNT_URL,
  });
}

export async function adminInfo() {
  return request('account/info', {
    method: 'POST',
    data: {},
    headers: {
      isAccount: true,
    },
    baseURL: ACCOUNT_URL,
  });
}

export async function revokeAdminSession(session: AdminSession, signal: AbortSignal) {
  // This request must carry the captured session, even if another login replaces the global session.
  const response = await fetch(`${ACCOUNT_URL}/account/logout`, {
    method:'POST', headers:{'Content-Type':'application/json',token:session.adminToken,
      operationID:crypto.randomUUID()}, body:'{}', signal, credentials:'omit', redirect:'error',
  });
  if (!response.ok) throw new Error('管理员会话撤销未确认');
  const result = await response.json();
  if (result.errCode !== 0 || result.data?.revoked !== true) throw new Error('管理员会话撤销未确认');
  return {revoked:true};
}
