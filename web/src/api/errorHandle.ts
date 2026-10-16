import { StaleSessionError } from "@/core/session/session";
import { message } from "@/AntdGlobalComp";
import { ErrCodeMap } from "@/constants";
import { registrationFlags } from "@/core/registrationRecovery";

interface ErrorData {
  errCode: number;
  errMsg?: string;
  errDlt?: string;
  registration?:unknown;
}

export const errorHandle = (err: unknown) => {
  if (err instanceof StaleSessionError) return;
  const errData = err as ErrorData;
  const registration=registrationFlags(errData?.registration);
  if(registration.registrationPending){message.warning("账号开通仍在确认，请稍后使用原账号密码登录，勿重复注册");return;}
  if(registration.registrationRecovered){message.info("账号和登录路由已恢复，请使用原账号密码登录");return;}
  if (err instanceof Error && !errData.errDlt && !errData.errMsg) { message.error(err.message); return; }
  if (errData.errMsg || errData.errDlt) {
    // 拼接 errDlt 和 errMsg，不切割
    const parts: string[] = [];
    if (errData.errDlt) {
      parts.push(errData.errDlt);
    }
    if (errData.errMsg) {
      parts.push(errData.errMsg);
    }
    const errorMsg = parts.length > 0 ? parts.join(' ') : '操作失败';
    message.error(ErrCodeMap[errData.errCode] || errorMsg);
  }
};
