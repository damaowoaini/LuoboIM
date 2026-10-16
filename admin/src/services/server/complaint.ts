import { CHAT_URL } from '@/config';
import type {
  HandleComplaintParams,
  SearchComplaintParams,
} from '@/pages/im_system/complaint/data';
import { request } from '@umijs/max';

export async function searchComplaint(params: SearchComplaintParams) {
  return request<{
    data: any;
  }>('/complaint/search', {
    method: 'POST',
    data: params,
    headers: {
      isAccount: true,
    },
    baseURL: CHAT_URL,
  });
}

export async function handleComplaint(params: HandleComplaintParams) {
  return request<{
    data: any;
  }>('/complaint/handle', {
    method: 'POST',
    data: params,
    headers: {
      isAccount: true,
    },
    baseURL: CHAT_URL,
  });
}

export async function deleteComplaint(ids: string[]) {
  return request<{
    data: any;
  }>('/complaint/delete', {
    method: 'POST',
    data: { ids },
    headers: {
      isAccount: true,
    },
    baseURL: CHAT_URL,
  });
}

export async function getComplaintDetail(id: string) {
  return request<{
    data: any;
  }>('/complaint/detail', {
    method: 'POST',
    data: { id },
    headers: {
      isAccount: true,
    },
    baseURL: CHAT_URL,
  });
}
