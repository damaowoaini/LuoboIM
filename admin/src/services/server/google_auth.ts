import { CHAT_URL } from '@/config';
import { request } from '@umijs/max';

// 获取二维码（调用 chart 服务）
export async function getGoogleAuthQRCode(userID: string) {
  return request<{
    data: {
      secret: string;
      qrCodeURL: string;
    };
  }>('/google_auth/qrcode', {
    method: 'POST',
    data: { userID },
    headers: { isAccount: true },
    baseURL: CHAT_URL,
  });
}

// 绑定谷歌验证码（调用 chart 服务）
export async function bindGoogleAuth(params: { userID: string; secret: string; code: string }) {
  return request<{
    data: any;
  }>('/google_auth/bind', {
    method: 'POST',
    data: params,
    headers: { isAccount: true },
    baseURL: CHAT_URL,
  });
}

// 重置/清空谷歌验证码（调用 chart 服务）
export async function resetGoogleAuth(userID: string) {
  return request<{
    data: any;
  }>('/google_auth/reset', {
    method: 'POST',
    data: { userID },
    headers: { isAccount: true },
    baseURL: CHAT_URL,
  });
}

// 查看用户谷歌验证码状态（调用 chart 服务）
export async function getUserGoogleAuthStatus(userID: string) {
  return request<{
    data: {
      enabled: boolean;
    };
  }>('/google_auth/status', {
    method: 'POST',
    data: { userID },
    headers: { isAccount: true },
    baseURL: CHAT_URL,
  });
}

// 批量查看用户谷歌验证码状态（暂时不支持，需要逐个查询）
export async function batchGetGoogleAuthStatus(userIDs: string[]) {
  const promises = userIDs.map((userID) => getUserGoogleAuthStatus(userID));
  const results = await Promise.all(promises);

  const statusMap: Record<string, boolean> = {};
  userIDs.forEach((userID, index) => {
    statusMap[userID] = results[index]?.data?.enabled || false;
  });

  return { data: statusMap };
}
