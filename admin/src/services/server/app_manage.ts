import { ACCOUNT_URL } from '@/config';
import { appletFromWire, type AppletDraft, type AppletRecord } from '@/core/appletManagement';
import { request } from '@umijs/max';

// new
export async function getClientConfig() {
  return request<{
    data: any;
  }>('/client_config/get', {
    method: 'POST',
    data: {},
    headers: {
      isAccount: true,
    },
    baseURL: ACCOUNT_URL,
  });
}

// new
export async function setClientConfig(data: any) {
  return request<{
    data: any;
  }>('/client_config/set', {
    method: 'POST',
    data: {
      config: {
        ...data,
      },
    },
    headers: {
      isAccount: true,
    },
    baseURL: ACCOUNT_URL,
  });
}

// new
export async function getApplet(data: {keyword:string;pagination:{pageNumber:number;showNumber:number}}) {
  const response = await request<{
    data: {applets?:AppletRecord[];total:number};
  }>('/applet/search', {
    method: 'POST',
    data,
    headers: {
      isAccount: true,
    },
    baseURL: ACCOUNT_URL,
  });
  return {...response,data:{...response.data,applets:(response.data?.applets??[]).map(appletFromWire),total:response.data?.total??0}};
}

// new
export async function crteateApplet(data: AppletDraft & {id:string}) {
  return request<{
    data: any;
  }>('/applet/add', {
    method: 'POST',
    data,
    headers: {
      isAccount: true,
    },
    baseURL: ACCOUNT_URL,
  });
}

export async function updateApplet(data:{id:string;[key:string]:unknown}) {
  return request('/applet/update',{method:'POST',data,headers:{isAccount:true},baseURL:ACCOUNT_URL});
}
export async function deleteApplet(appletIds:string[]) {
  return request('/applet/del',{method:'POST',data:{appletIds},headers:{isAccount:true},baseURL:ACCOUNT_URL});
}
