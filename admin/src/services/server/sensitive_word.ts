import { API_URL } from '@/config';
import type {
  AddSensitiveWordParams,
  SearchSensitiveWordParams,
  UpdateSensitiveWordParams,
} from '@/pages/im_system/sensitive_word/data';
import { request } from '@umijs/max';

export async function searchSensitiveWord(params: SearchSensitiveWordParams) {
  return request<{
    data: any;
  }>('/sensitive_word/search', {
    method: 'POST',
    data: params,
    baseURL: API_URL,
  });
}

export async function addSensitiveWord(params: AddSensitiveWordParams) {
  return request<{
    data: any;
  }>('/sensitive_word/add', {
    method: 'POST',
    data: params,
    baseURL: API_URL,
  });
}

export async function updateSensitiveWord(params: UpdateSensitiveWordParams) {
  return request<{
    data: any;
  }>('/sensitive_word/update', {
    method: 'POST',
    data: params,
    baseURL: API_URL,
  });
}

export async function deleteSensitiveWord(ids: string[]) {
  return request<{
    data: any;
  }>('/sensitive_word/delete', {
    method: 'POST',
    data: { ids },
    baseURL: API_URL,
  });
}

export async function reloadSensitiveWord() {
  return request<{
    data: any;
  }>('/sensitive_word/reload', {
    method: 'POST',
    data: {},
    baseURL: API_URL,
  });
}
