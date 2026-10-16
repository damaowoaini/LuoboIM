import { ACCOUNT_URL } from '@/config';
import type { PreviewTransport, ConversationPage, MessagePage } from '@/core/adminPreview';
import { request } from '@umijs/max';
const post = async <T>(path: string, data: unknown, signal: AbortSignal): Promise<T> => {
  const response = await request<{ data: T }>(`/admin/message_preview/${path}`, {
    method: 'POST',
    baseURL: ACCOUNT_URL,
    data,
    signal,
    headers: { isAccount: true },
    skipErrorHandler: true,
  });
  return response.data;
};
export const adminPreviewTransport: PreviewTransport = {
  conversations: (data, signal) => post<ConversationPage>('conversations', data, signal),
  search: (data, signal) => post<MessagePage>('search', data, signal),
};
