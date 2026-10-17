import { AppConfigResponseError } from "./appConfig";
import { ApiError } from "@/utils/request";

export interface AppConfigFailure {
  reason: "timeout" | "connection" | "response" | "rejected" | "unknown";
  code?: number;
}

// Keep a finite public diagnosis only. Error messages/configuration can carry
// upstream URLs, credentials or response bodies and never enter this state.
export function appConfigFailure(error: unknown): AppConfigFailure {
  if (error instanceof AppConfigResponseError) return { reason: "response" };
  if (error instanceof ApiError) {
    const code = Number.isSafeInteger(error.errCode) && Math.abs(error.errCode) <= 99999999
      ? error.errCode : undefined;
    if (code === 503) {
      return { reason: error.message === "请求超时，请重试" ? "timeout" : "connection", code };
    }
    return { reason: "rejected", ...(code === undefined ? {} : { code }) };
  }
  return { reason: "unknown" };
}

export function appConfigFailureDescription(value?: AppConfigFailure | null): string {
  const descriptions = {
    timeout: "配置请求超时，请稍后重试或检查服务地址。",
    connection: "无法连接配置服务，请检查网络或服务地址。",
    response: "配置服务返回的数据格式异常，请联系管理员检查服务版本。",
    rejected: "配置服务返回错误，请重试或联系管理员。",
    unknown: "配置获取失败，请检查服务地址或稍后重试。",
  };
  const message = descriptions[value?.reason ?? "unknown"];
  return value?.code === undefined ? message : `${message}（错误码 ${value.code}）`;
}
