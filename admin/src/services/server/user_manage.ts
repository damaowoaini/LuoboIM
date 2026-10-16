import { ACCOUNT_URL, API_URL, CHAT_URL } from '@/config';
import type { AdminOnlineDetail } from '@/core/userManagement';
import type { AdminCreationResult, ProvisioningStatus } from '@/core/adminProvisioning';
import { request } from '@umijs/max';

// new
export async function getUserList(params: API.UserManage.GetUserParams) {
  return request('/user/search/full', {
    method: 'POST',
    data: {
      ...params,
      // 0全部 1未封 2已封
      normal: 1,
      genders: 0,
    },
    headers: {
      isAccount: true,
    },
    baseURL: CHAT_URL,
  });
}

// new
export async function updateUserInfo(params: API.UserManage.UserInfoItem) {
  return request<{
    data: any;
  }>('user/update', {
    method: 'POST',
    data: {
      ...params,
    },
    headers: {
      isAccount: true,
    },
    baseURL: CHAT_URL,
  });
}

// new
export async function registerUser(params: API.UserManage.RegisterUserParams) {
  return request<{
    data: AdminCreationResult;
  }>('/account/register', {
    method: 'POST',
    data: {
      ...params,
      platform: 5,
    },
    headers: {
      isAccount: true,
    },
    baseURL: CHAT_URL,
  });
}

export async function getAdminProvisioningStatus(adminRequestID: string) {
  return request<{data:ProvisioningStatus}>('/account/provisioning/status', {
    method:'POST', data:{adminRequestID}, headers:{isAccount:true}, baseURL:ACCOUNT_URL,
  });
}

export async function resolveAdminProvisioning(adminRequestID: string) {
  return request<{data:ProvisioningStatus}>('/account/provisioning/resolve', {
    method:'POST',data:{adminRequestID,closeIfNotCreated:true},headers:{isAccount:true},baseURL:ACCOUNT_URL,
  });
}

// new
export async function resetUserPassword(params: API.UserManage.ResetUserPasswordParams) {
  return request<{
    data: any;
  }>('/user/password/reset', {
    method: 'POST',
    data: {
      ...params,
    },
    headers: {
      isAccount: true,
    },
    baseURL: ACCOUNT_URL,
  });
}

//new
export async function getBlockList(params: API.UserManage.GetUserParams) {
  return request<{
    data: any;
  }>('/block/search', {
    method: 'POST',
    data: {
      ...params,
    },
    headers: {
      isAccount: true,
    },
    baseURL: ACCOUNT_URL,
  });
}

// new
// op  1 block 2 unblock user
export async function updateBlock(userID: string, reason = '') {
  return request<{
    data: any;
  }>('/block/add', {
    method: 'POST',
    data: {
      userID,
      reason,
    },
    headers: {
      isAccount: true,
    },
    baseURL: ACCOUNT_URL,
  });
}

// 解封
export async function deleBlock(userID: string) {
  return request<{
    data: any;
  }>('/block/del', {
    method: 'POST',
    data: {
      userIDs: [userID],
    },
    headers: {
      isAccount: true,
    },
    baseURL: ACCOUNT_URL,
  });
}

// new
export async function getFriendList(params: API.UserManage.GetFriendsParams) {
  return request<{
    data: any;
  }>('/friend/get_friend_list', {
    method: 'POST',
    data: {
      ...params,
    },
    baseURL: API_URL,
  });
}

// new
export async function deleteFriend(params: API.UserManage.DeleteFriendParams) {
  return request<{
    data: any;
  }>('/friend/delete_friend', {
    method: 'POST',
    data: {
      ...params,
    },
    baseURL: API_URL,
  });
}

// new 下线
export async function forceLogout(params: API.UserManage.KickUserParams) {
  return request<{
    data: any;
  }>('/auth/force_logout', {
    method: 'POST',
    data: {
      ...params,
    },
    baseURL: API_URL,
  });
}

// new
export async function getUsersOnlineStatus(userIDs: string[]) {
  return request<{
    data: AdminOnlineDetail[];
  }>('/user/get_users_online_token_detail', {
    method: 'POST',
    data: {
      userIDs,
    },
    baseURL: API_URL,
  });
}

// new
export async function update_info(params: API.UserManage.UpdateAdminInfoParams) {
  return request<{
    data: any;
  }>('/account/update', {
    method: 'POST',
    data: {
      ...params,
    },
    headers: {
      isAccount: true,
    },
    baseURL: ACCOUNT_URL,
  });
}

// new

// The server derives the administrator ID from the authenticated token. The
// profile endpoint deliberately cannot bypass the current-password check.
export async function changeOwnAdminPassword(currentPassword: string, newPassword: string) {
  return request('/account/change_password', {
    method: 'POST',
    data: { currentPassword, newPassword },
    headers: { isAccount: true },
    baseURL: ACCOUNT_URL,
  });
}
