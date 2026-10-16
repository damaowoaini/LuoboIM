import { ACCOUNT_URL } from '@/config';
import { request } from '@umijs/max';

// 获取管理员谷歌验证码二维码（通过账号密码）
export async function getAdminGoogleAuthQRCode(account: string, password: string) {
  return request<{
    data: {
      qrCodeUrl: string;
      secret: string;
    };
  }>('google_auth_manager/qrcode', {
    method: 'POST',
    data: {
      account,
      password,
    },
    baseURL: ACCOUNT_URL,
  });
}

// 绑定管理员谷歌验证码（通过账号密码）
export async function bindAdminGoogleAuth(
  account: string,
  password: string,
  secret: string,
  code: string,
) {
  return request<{
    data: Record<string, never>;
  }>('google_auth_manager/bind', {
    method: 'POST',
    data: {
      account,
      password,
      secret,
      code,
    },
    baseURL: ACCOUNT_URL,
  });
}

// 重置管理员谷歌验证码（超级管理员）
export async function resetAdminGoogleAuth(userID: string) {
  return request<{
    data: Record<string, never>;
  }>('google_auth_manager/reset', {
    method: 'POST',
    data: {
      userID,
    },
    headers: { isAccount: true },
    baseURL: ACCOUNT_URL,
  });
}

// 获取管理员谷歌验证码状态
export async function getAdminGoogleAuthStatus(account: string, password: string) {
  return request<{
    data: {
      enabled: boolean;
    };
  }>('google_auth_manager/status', {
    method: 'POST',
    data: {
      account,
      password,
    },
    baseURL: ACCOUNT_URL,
  });
}
