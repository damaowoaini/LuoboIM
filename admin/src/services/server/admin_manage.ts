import { ACCOUNT_URL } from '@/config';
import { request } from '@umijs/max';

export interface AdminAccountItem {
  account: string;
  faceURL?: string;
  nickname?: string;
  userID: string;
  level: number;
  disabled: boolean;
  createTime: number;
}

const requestOptions = {
  headers: { isAccount: true },
  baseURL: ACCOUNT_URL,
};

export async function listAdminAccounts(params: {
  pagination: { pageNumber: number; showNumber: number };
}) {
  return request<{ data: { total: number; adminAccounts: AdminAccountItem[] } }>('/account/list', {
    ...requestOptions,
    method: 'POST',
    data: params,
  });
}

export async function addAdminAccount(data: {
  account: string;
  password: string;
  nickname?: string;
  faceURL?: string;
}) {
  return request('/account/add_admin', { ...requestOptions, method: 'POST', data });
}

export async function deleteAdminAccount(userID: string) {
  return request('/account/del_admin', {
    ...requestOptions,
    method: 'POST',
    data: { userIDs: [userID] },
  });
}

export async function setAdminAccountStatus(userID: string, enabled: boolean) {
  return request('/account/set_status', {
    ...requestOptions,
    method: 'POST',
    data: { userID, enabled },
  });
}

export async function resetAdminPassword(userID: string, newPassword: string) {
  return request('/account/reset_password', {
    ...requestOptions,
    method: 'POST',
    data: { userID, newPassword },
  });
}

export async function resetAdminGoogleAuth(userID: string) {
  return request('/google_auth_manager/reset', {
    ...requestOptions,
    method: 'POST',
    data: { userID },
  });
}
