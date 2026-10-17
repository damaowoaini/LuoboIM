import type { MessageReceiveOptType } from "open-im-sdk-wasm";
import { useMutation, useQuery } from "@tanstack/react-query";
import { v4 as uuidv4 } from "uuid";

import { getChatUrl, getRouteUrl } from "@/config";
import { useUserStore } from "@/store";
import { AppConfig } from "@/store/type";
import { decodeAppConfigData } from "@/core/appConfig";
import createAxiosInstance from "@/utils/request";
import { getChatToken } from "@/utils/storage";

import { sessionCoordinator } from "@/core/session/browser";
import { StaleSessionError } from "@/core/session/session";

import { errorHandle } from "./errorHandle";

const request = createAxiosInstance(getChatUrl, "chat");
const routeRequest = createAxiosInstance(getRouteUrl, "none");

const platform = window.electronAPI?.getPlatform() ?? 5;
const device = () => {
  let deviceID = localStorage.getItem("luobo.device.id");
  if (!deviceID) {deviceID = crypto.randomUUID();localStorage.setItem("luobo.device.id", deviceID);}
  return { deviceID, deviceModel: window.electronAPI?.getSystemVersion() || navigator.platform || "Web" };
};

const getAreaCode = (code?: string) =>
code ? code.includes("+") ? code : `+${code}` : code;

// Send verification code
export const useSendSms = () => {
  return useMutation({ mutationFn:
    (params: API.Login.SendSmsParams) =>
    routeRequest.post(
      "/api/verify/send",
      {
        ...params
      },
      {
        headers: {
          operationID: uuidv4()
        }
      }
    ),

    onError: errorHandle }

  );
};

// Verify mobile phone number
export const useVerifyCode = () => {
  return useMutation({ mutationFn:
    (params: API.Login.VerifyCodeParams) =>
    routeRequest.post(
      "/api/verify/check",
      {
        ...params,
        platform,
        areaCode: getAreaCode(params.areaCode)
      },
      {
        headers: {
          operationID: uuidv4()
        }
      }
    ),

    onError: errorHandle }

  );
};

// register
type AuthMutationGuard={isCurrent?:()=>boolean;quiet?:boolean};
export const useRegister = (guard:AuthMutationGuard={}) => {
  return useMutation({ retry:false,networkMode:"always",gcTime:0,mutationFn:
    async (params: API.Login.DemoRegisterType) => {
      const generation = sessionCoordinator.generation;
      const issuer=getRouteUrl();
      const current=()=>getRouteUrl()===issuer && generation===sessionCoordinator.generation && guard.isCurrent?.()!==false;
      if(!current())throw new StaleSessionError();
      const frozenRequest=createAxiosInstance(issuer,"none");
      const result = await frozenRequest.post<{chatToken: string;imToken: string;userID: string;}>(
        "/api/auth/register",
        {
          ...params,
          user: {
            ...params.user,
            areaCode: getAreaCode(params.user.areaCode)
          },
          platform,
          ...device()
        },
        {
          headers: {
            operationID: uuidv4()
          }
        }
      );
      if(!current())throw new StaleSessionError();
      await sessionCoordinator.commit({ ...result.data, issuer }, generation);
      return result;
    },

    onError: ()=>{} }

  );
};

// reset passwords
export const useReset = () => {
  return useMutation({ mutationFn:
    (params: API.Login.ResetParams) =>
    routeRequest.post(
      "/api/auth/reset-password",
      {
        ...params,
        platform,
        areaCode: getAreaCode(params.areaCode)
      },
      {
        headers: {
          operationID: uuidv4()
        }
      }
    ),

    onError: errorHandle }

  );
};

// change password
export const modifyPassword = async (params: API.Login.ModifyParams) => {
  const token = (await getChatToken()) as string;
  return request.post(
    "/account/password/change",
    {
      ...params
    },
    {
      headers: {
        token,
        operationID: uuidv4()
      }
    }
  );
};

// log in
export const useLogin = (guard:AuthMutationGuard={}) => {
  return useMutation({ retry:false,networkMode:"always",gcTime:0,mutationFn:
    async (params: API.Login.LoginParams) => {
      const generation = sessionCoordinator.generation;
      const issuer=getRouteUrl();
      const current=()=>getRouteUrl()===issuer && generation===sessionCoordinator.generation && guard.isCurrent?.()!==false;
      if(!current())throw new StaleSessionError();
      const frozenRequest=createAxiosInstance(issuer,"none");
      const result = await frozenRequest.post<{chatToken: string;imToken: string;userID: string;}>(
        "/api/auth/login",
        {
          ...params,
          platform,
          ...device(),
          areaCode: getAreaCode(params.areaCode)
        },
        {
          headers: {
            operationID: uuidv4()
          }
        }
      );
      if(!current())throw new StaleSessionError();
      await sessionCoordinator.commit({ ...result.data, issuer }, generation);
      return result;
    },

    onError: guard.quiet?()=>{}:errorHandle }

  );
};

// Get user information
export interface BusinessUserInfo {
  userID: string;
  password: string;
  account: string;
  phoneNumber: string;
  areaCode: string;
  email: string;
  nickname: string;
  faceURL: string;
  gender: number;
  level: number;
  birth: number;
  allowAddFriend: BusinessAllowType;
  allowBeep: BusinessAllowType;
  allowVibration: BusinessAllowType;
  globalRecvMsgOpt: MessageReceiveOptType;
  userRole?: string; // 用户角色：普通用户、高级用户等
}

export enum BusinessAllowType {
  Allow = 1,
  NotAllow = 2,
}

export const getBusinessUserInfo = async (userIDs: string[]) => {
  const token = (await getChatToken()) as string;
  return request.post<{users: BusinessUserInfo[];}>(
    "/user/find/full",
    {
      userIDs
    },
    {
      headers: {
        operationID: uuidv4(),
        token
      }
    }
  );
};

export const searchBusinessUserInfo = async (keyword: string) => {
  const token = (await getChatToken()) as string;
  return request.post<{total: number;users: BusinessUserInfo[];}>(
    "/user/search/full",
    {
      keyword,
      pagination: {
        pageNumber: 1,
        showNumber: 1
      }
    },
    {
      headers: {
        operationID: uuidv4(),
        token
      }
    }
  );
};

interface UpdateBusinessUserInfoParams {
  email: string;
  nickname: string;
  faceURL: string;
  gender: number;
  birth: number;
  allowAddFriend: number;
  allowBeep: number;
  allowVibration: number;
  globalRecvMsgOpt: number;
}

export const updateBusinessUserInfo = async (
params: Partial<UpdateBusinessUserInfoParams>) =>
{
  const token = (await getChatToken()) as string;
  return request.post<unknown>(
    "/user/update",
    {
      ...params,
      userID: useUserStore.getState().selfInfo?.userID
    },
    {
      headers: {
        operationID: uuidv4(),
        token
      }
    }
  );
};

export const getAppConfig = async (invitationCode?: string) => {
  await sessionCoordinator.restore().catch(() => undefined);
  const response = await (sessionCoordinator.current ? request : routeRequest).post<unknown>(
    sessionCoordinator.current ? "/client_config/get" : "/api/client_config/get",
    { invitationCode },
    {
      headers: {
        operationID: uuidv4()
      }
    }
  );
  // The protobuf string map is omitted for an empty successful config. Keep
  // transport/business failures rejected and validate the successful data here.
  const data = decodeAppConfigData(response.data);
  // Preserve the existing UI-facing type; the wire values themselves are all
  // strings, and existing policy consumers perform their own Number/String conversion.
  return { ...response, data: data as unknown as { config: AppConfig } };
};

// 获取用户最新登录记录
export const getUserLatestLoginRecord = async (
params: API.User.GetLatestLoginRecordParams) =>
{
  const token = (await getChatToken()) as string;
  return request.post<API.User.LatestLoginRecordResponse>(
    "/user/login_record/latest",
    params,
    {
      headers: {
        operationID: uuidv4(),
        token
      }
    }
  );
};

// 获取用户登录记录列表
export const getUserLoginRecords = async (
params: API.User.GetLoginRecordsParams) =>
{
  const token = (await getChatToken()) as string;
  return request.post<API.User.LoginRecordsResponse>(
    "/user/login_record/list",
    params,
    {
      headers: {
        operationID: uuidv4(),
        token
      }
    }
  );
};
