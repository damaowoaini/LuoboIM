import axios, { AxiosError, AxiosRequestConfig, InternalAxiosRequestConfig } from "axios";
import { v4 as uuidv4 } from "uuid";
import { sessionCoordinator } from "@/core/session/browser";
import { RequestLease, SessionCoordinator, StaleSessionError } from "@/core/session/session";
import { registrationFlags, type RegistrationFlags } from "@/core/registrationRecovery";

type TokenMode = "im" | "chat" | "none";
export type GuardedRequestConfig = AxiosRequestConfig & { luoboCurrent?: () => boolean };
type ScopedConfig = InternalAxiosRequestConfig & { luoboLease?: RequestLease; luoboCurrent?: () => boolean };
export class ApiError extends Error {
  readonly registration:RegistrationFlags;
  constructor(readonly errCode: number, readonly errDlt: string, data?:unknown) { super(errDlt); this.name = "ApiError";this.registration=registrationFlags(data); }
}
// This brand is created only at the Axios transport boundary. A business
// envelope with errCode503 remains an ordinary ApiError.
export class TransportApiError extends ApiError {
  constructor(readonly transportKind: "timeout" | "connection") {
    super(503, transportKind === "timeout" ? "请求超时，请重试" : "网络连接失败，请重试");
  }
}
const createAxiosInstance = (base: string | (() => string), tokenMode: TokenMode | boolean = "im", coordinator: SessionCoordinator = sessionCoordinator) => {
  const mode = typeof tokenMode === "boolean" ? tokenMode ? "im" : "chat" : tokenMode;
  const request = axios.create({ timeout: 25000 });
  request.interceptors.request.use(async (config: ScopedConfig) => {
    if (config.luoboCurrent?.() === false) throw new StaleSessionError();
    if (mode !== "none") await coordinator.restore();
    if (config.luoboCurrent?.() === false) throw new StaleSessionError();
    const baseURL = typeof base === "function" ? base() : base;
    if (!baseURL) throw new ApiError(401, "请先登录或配置路由服务");
    if (config.url?.startsWith("//")) throw new ApiError(400, "请求地址无效");
    if (/^[a-z][a-z\d+.-]*:/i.test(config.url || "")) {
      if (new URL(config.url!).origin !== new URL(baseURL).origin) throw new ApiError(400, "不能向其他服务器发送当前凭证");
    }
    const session = coordinator.current;
    if (mode !== "none" && !session) throw new ApiError(401, "请先登录");
    config.baseURL = baseURL;
    // The caller cannot accidentally attach an IM token to chart or a previous
    // account's token to a request that started after the session changed.
    if (mode === "none") delete config.headers.token;
    else config.headers.token = mode === "im" ? session!.imToken : session!.chatToken;
    config.headers.operationID = uuidv4();
    const lease = coordinator.lease();
    config.signal = lease.signal;
    config.luoboLease = lease;
    return config;
  });
  request.interceptors.response.use(res => {
    const lease = (res.config as ScopedConfig).luoboLease;
    lease?.release(); lease?.assertCurrent();
    if ((res.config as ScopedConfig).luoboCurrent?.() === false) throw new StaleSessionError();
    if (res.data?.errCode !== 0) throw new ApiError(Number(res.data?.errCode ?? 500), res.data?.errDlt || res.data?.errMsg || "操作未完成，请重试",res.data?.data);
    return res.data;
  }, (error: AxiosError | ApiError | StaleSessionError) => {
    if (error instanceof ApiError || error instanceof StaleSessionError) throw error;
    const lease = (error.config as ScopedConfig | undefined)?.luoboLease;
    lease?.release(); lease?.assertCurrent();
    if ((error.config as ScopedConfig | undefined)?.luoboCurrent?.() === false) throw new StaleSessionError();
    // Do not propagate Axios request configuration, which contains tokens and
    // password material, to old UI console/error handlers.
    if (!error.response && (error.code === "ECONNABORTED" || error.code === "ETIMEDOUT"))
      throw new TransportApiError("timeout");
    if (!error.response && error.code === "ERR_NETWORK")
      throw new TransportApiError("connection");
    throw new ApiError(error.response?.status ?? 503, error.code === "ECONNABORTED" ? "请求超时，请重试" : "网络连接失败，请重试",(error.response?.data as {data?:unknown}|undefined)?.data);
  });
  return request;
};
export default createAxiosInstance;
