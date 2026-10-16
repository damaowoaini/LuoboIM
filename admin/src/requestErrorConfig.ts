import { history, type RequestConfig } from '@umijs/max';
import { message } from 'antd';
import { adminSessions, StaleAdminSessionError } from './core/adminSession';

export class AdminApiError extends Error {
  constructor(readonly errCode: number, text: string) {
    super(text);
    this.name = 'AdminApiError';
  }
}
export const errorConfig: RequestConfig = {
  errorConfig: {
    errorHandler: (error: unknown, opts: { skipErrorHandler?: boolean }) => {
      if (error instanceof StaleAdminSessionError) throw error;
      const safe =
        error instanceof AdminApiError ? error : new AdminApiError(503, '网络连接失败，请重试');
      if (!opts?.skipErrorHandler) message.error(safe.message);
      // Axios configuration can contain password/token material. The Umi request
      // wrapper rejects this sanitized error before any legacy UI catch handler.
      throw safe;
    },
  },
  responseInterceptors: [
    (response) => {
      const data = response.data as
        | { errCode?: number; errMsg?: string; errDlt?: string }
        | undefined;
      if (data?.errCode !== 0) {
        const errCode = Number(data?.errCode ?? 500);
        if (adminSessions?.rejectAuthentication(errCode)) history.replace('/login');
        throw new AdminApiError(errCode, data?.errMsg || data?.errDlt || '操作失败，请重试');
      }
      return response;
    },
  ],
};
