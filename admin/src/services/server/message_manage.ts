import { ACCOUNT_URL, API_URL } from '@/config';
import { request } from '@umijs/max';

// new
export async function getChatLogs(data: API.ChatLog.GetChatLogParams) {
  return request<{
    data: any;
  }>('/admin/message_search', {
    method: 'POST',
    data,
    headers: { isAccount: true },
    baseURL: ACCOUNT_URL,
  });
}

export async function getsignalrecords(data: API.ChatLog.GetSignalrecords) {
  return request<{
    data: any;
  }>('/cms/signal/get_signal_records', {
    method: 'POST',
    data,
  });
}

// new
export function revokeMessage(data: API.ChatLog.RevokeMessageParams) {
  return request<{
    data: any;
  }>('/msg/revoke_msg', {
    method: 'POST',
    data,
    baseURL: API_URL,
  });
}

// new 系统通知
export async function batchSend({
  recvIDList,
  isSendAll,
  sendID,
  text,
}: API.ChatLog.BatchSendParams) {
  return request<{data:unknown}>('/msg/batch_send_msg', {
    method: 'POST',
    data: {
      isSendAll,
      sendID,
      recvIDs: recvIDList,
      groupID: '',
      senderNickname: '系统通知',
      senderFaceURL: '',
      senderPlatformID: 5,
      content: {
        content: text,
      },
      contentType: 101,
      // 系统通知4还没做，先按单聊1发
      sessionType: 1,
      isOnlineOnly: false,
      notOfflinePush: false,
      offlinePushInfo: {
        title: '系统通知',
        desc: '系统通知',
        ex: '',
        iOSPushSound: 'default',
        iOSBadgeCount: true,
      },
      //--------
      // businessOperationID: uuid(),
      // contentType: 1400,
      // groupID: '',
      // isOnlineOnly: false,
      // notOfflinePush: false,
      // offlinePushInfo: {
      //   desc: '系统通知',
      //   ex: '',
      //   iOSBadgeCount: true,
      //   iOSPushSound: 'default',
      //   title: '系统通知',
      // },
      // operationID: uuid(),
      // recvIDList,
      // isSendAll,
      // sendID,
      // senderFaceURL: '',
      // senderNickname: '系统通知',
      // senderPlatformID: 5,
      // sessionType: 4,
      // content: {
      //   ex: '',
      //   mixType: pictureElem ? 1 : 0,
      //   notificationFaceURL: '',
      //   notificationName: '系统通知',
      //   notificationType: 1,
      //   pictureElem,
      //   text,
      //   url: '',
      // },
    },
    baseURL: API_URL,
  });
}
