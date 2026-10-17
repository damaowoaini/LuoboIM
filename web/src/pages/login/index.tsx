import { readSdkLoginFailure } from "@/core/session/loginFailure";
import { useUserStore } from "@/store";
import { SettingOutlined } from "@ant-design/icons";
import { Alert, Button } from "antd";
import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import { RegistrationRecovery } from "@/core/registrationRecovery";
import WindowControlBar from "@/components/WindowControlBar";
import { APP_VERSION } from "@/config";
import { getLoginMethod, setLoginMethod as saveLoginMethod } from "@/utils/storage";
import ConfigModal from "./ConfigModal";
import LoginForm from "./LoginForm";
import ModifyForm from "./ModifyForm";
import RegisterForm from "./RegisterForm";
import styles from "./index.module.scss";
import { useSearchParams } from "react-router-dom";
export type FormType = 0 | 1 | 2;
export const Login = () => {
  const [search] = useSearchParams();
  const imError = search.get("imError") ?? readSdkLoginFailure();
  const logout = search.get("logout");
  const safeImCode = imError && /^-?\d{1,8}$/.test(imError) ? imError : "unknown";
  const appConfigStatus = useUserStore(state=>state.appConfigStatus);
  const retryConfig = useUserStore(state=>state.getAppConfigByReq);
  const [formType, setFormType] = useState<FormType>(0);
  const [registrationRecovery]=useState(()=>new RegistrationRecovery());
  const registrationView=useSyncExternalStore(registrationRecovery.subscribe,registrationRecovery.getSnapshot,registrationRecovery.getSnapshot);
  useEffect(()=>()=>registrationRecovery.dispose(),[registrationRecovery]);
  const [loginMethod, setLoginMethod] = useState<"phone" | "email">(getLoginMethod());
  const [configVisible, setConfigVisible] = useState(false);
  const updateLoginMethod = useCallback((method: "phone" | "email") => { setLoginMethod(method); saveLoginMethod(method); }, []);
  return <main className={styles.page}>
    <header className={styles.header}>
      <div className={styles.brand}><span className={styles.mark} aria-hidden="true">萝</span><span>萝卜对话</span></div>
      <div className="flex items-center gap-3"><Button type="text" icon={<SettingOutlined />} onClick={() => setConfigVisible(true)}>服务设置</Button>{window.electronAPI && <WindowControlBar />}</div>
    </header>
    <section className={styles.content}>
      <div className={styles.intro}>
        <span className={styles.eyebrow}>让每一次对话，都更近一点</span>
        <h1>日常交流，<br /><em>从容连接。</em></h1>
        <p>与好友聊一聊，和团队聚一聚。<br />消息、语音与视频，在这里自然发生。</p>
        <div className={styles.features}><span>即时消息</span><span>群组协作</span><span>音视频通话</span></div>
      </div>
      <div className={`${styles.login} ${styles.card}`}>
        {logout === "pending" && <Alert className="mb-5" type="info" showIcon message="已退出当前设备" description="当前凭证已撤销，服务端正在完成连接关闭。" />}
        {logout === "failed" && <Alert className="mb-5" type="warning" showIcon message="已清除本机登录状态" description="暂时无法确认服务端注销结果。请检查网络，必要时联系管理员关闭仍在连接的会话。" />}
        {imError && <Alert className="mb-5" type="error" showIcon message="消息服务登录失败" description={safeImCode === "unknown" ? "暂时无法连接消息服务，请稍后重试。" : `错误码 ${safeImCode}。请稍后重试或联系管理员。`} />}
        {appConfigStatus === "error" && <Alert className="mb-5" type="warning" showIcon message="暂时无法获取服务配置" description="请检查服务地址或稍后重试。" action={<div className="flex shrink-0 flex-col items-end gap-2"><Button size="small" aria-label="检查登录服务地址" onClick={() => setConfigVisible(true)}>服务设置</Button><Button size="small" onClick={()=>retryConfig()}>重试</Button></div>} />}
        {formType!==2 && registrationRecovery.hasAttempt() && <Alert className="mb-5" type="warning" showIcon message="此前注册结果仍需确认"
          description={`请使用注册时的原账号密码登录，勿重新获取注册验证码或再次注册。${registrationView.identity?` 原账号：${registrationView.identity}`:""}`}
          action={<Button size="small" onClick={()=>setFormType(2)}>返回确认</Button>}/>}
        {formType === 0 && <LoginForm setFormType={setFormType} loginMethod={loginMethod} updateLoginMethod={updateLoginMethod} />}
        {formType === 1 && <ModifyForm setFormType={setFormType} loginMethod={loginMethod} />}
        {formType === 2 && <RegisterForm setFormType={setFormType} loginMethod={loginMethod} recovery={registrationRecovery} />}
      </div>
    </section>
    <footer className={styles.footer}>{APP_VERSION}</footer>
    <ConfigModal visible={configVisible} close={() => setConfigVisible(false)} />
  </main>;
};
