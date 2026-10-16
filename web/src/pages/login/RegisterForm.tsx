import { LeftOutlined } from "@ant-design/icons";
import { Alert, App, Button, Form, Input, Select, Space } from "antd";
import { t } from "i18next";
import md5 from "md5";
import React, { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { BusinessAllowType, getAppConfig, useLogin, useRegister, useSendSms, useVerifyCode } from "@/api/login";
import { getRouteUrl } from "@/config";
import { RegistrationRecovery } from "@/core/registrationRecovery";
import { sessionCoordinator } from "@/core/session/browser";
import { useUserStore } from "@/store";
import { setAreaCode, setEmail, setPhoneNumber } from "@/utils/storage";

import { areaCode } from "./areaCode";
import type { FormType } from "./index";

type RegisterFormProps = {
  loginMethod: "phone" | "email";
  setFormType: (type: FormType) => void;
  recovery:RegistrationRecovery;
};

type FormFields = {
  email?: string;
  phoneNumber?: string;
  areaCode: string;
  verifyCode: string;
  invitationCode: string;
  nickname: string;
  password: string;
  password2: string;
};

const RegisterForm = ({ loginMethod, setFormType, recovery }: RegisterFormProps) => {
  const { message } = App.useApp();
  const [form] = Form.useForm<FormFields>();
  const navigate = useNavigate();
  const location=useLocation(),path=useRef(location.pathname);path.current=location.pathname;
  const pageEpoch=useRef(0),pageActive=useRef(true),transitionBusy=useRef(false);
  useEffect(()=>{pageActive.current=true;return()=>{pageActive.current=false;pageEpoch.current++;};},[]);
  const pageCurrent=()=>pageActive.current && path.current==="/login";
  const { mutate: sendSms, isPending: sending } = useSendSms();
  const { mutate: verifySmsCode, isPending: verifying } = useVerifyCode();
  const activeTicket=useRef<number|undefined>(recovery.currentTicket());
  const view=useSyncExternalStore(recovery.subscribe,recovery.getSnapshot,recovery.getSnapshot);
  const guard={isCurrent:()=>pageCurrent()&&recovery.isCurrent(activeTicket.current),quiet:true};
  const { mutate: register, isPending: registering } = useRegister(guard);
  const { mutate: confirmLogin } = useLogin(guard);
  const [googleAuthCode,setGoogleAuthCode]=useState("");
  const verifiedIssuer=useRef<string|undefined>(undefined);

  const needInvitationCode = useUserStore(
    (state) =>
      Number(state.appConfig.needInvitationCodeRegister) === BusinessAllowType.Allow,
  );

  // 0login 1resetPassword 2register
  const [registerForm, setRegisterForm] = useState(0);

  const [code, setCode] = useState("");
  const [loadingConfig, setLoadingConfig] = useState(false);
  const [countdown, setCountdown] = useState(0);
  useEffect(() => {
    if (countdown > 0) {
      const timer = setTimeout(() => {
        setCountdown((prevCountdown) => prevCountdown - 1);
        if (countdown === 1) {
          clearTimeout(timer);
          setCountdown(0);
        }
      }, 1000);

      return () => clearTimeout(timer);
    }
  }, [countdown]);

  const onFinish = async (fields: FormFields) => {
    if(recovery.hasAttempt()||transitionBusy.current||!pageCurrent())return;
    const epoch=pageEpoch.current,issuer=getRouteUrl();
    const current=()=>pageCurrent()&&pageEpoch.current===epoch&&getRouteUrl()===issuer&&!recovery.hasAttempt();
    if (registerForm === 0) {
      const pattern = /^\d{6,24}$/;
      if (fields.phoneNumber && !pattern.test(fields.phoneNumber)) {
        return message.error(t("toast.inputCorrectPhoneNumber"));
      }
      transitionBusy.current=true;setLoadingConfig(true);
      try {
        const { data } = await getAppConfig(fields.invitationCode);
        if(!current())return;
        verifiedIssuer.current=issuer;
        const setting = String(data.config.needVerificationCodeRegister ?? "1").trim().toLowerCase();
        if (["0", "false", "no"].includes(setting)) { setRegisterForm(2); return; }
      } catch (error) { if(current())message.error(error instanceof Error ? error.message : "获取注册配置失败"); return; }
      finally { transitionBusy.current=false;if(pageCurrent()&&pageEpoch.current===epoch)setLoadingConfig(false); }
      if(!current())return;
      transitionBusy.current=true;
      sendSms(
        {
          usedFor: 1,
          ...fields,
        },
        {
          onSuccess() {
            if(!current())return;
            setCountdown(60);
            setRegisterForm(1);

          },
          onSettled(){transitionBusy.current=false;},
        },
      );
    }
    const verifyCode = code;

    if (registerForm === 1) {
      if (!/^\d{6}$/.test(verifyCode)) { message.error("请输入6位验证码"); return; }
      transitionBusy.current=true;verifySmsCode(
        {
          ...fields,
          verifyCode,
          usedFor: 1,
        },
        {
          onSuccess() {
            if(!current())return;
            setRegisterForm(2);
          },
          onSettled(){transitionBusy.current=false;},
        },
      );
    }
    if (registerForm === 2) {
      if(verifiedIssuer.current!==issuer){setRegisterForm(0);setCode("");message.error("服务地址已变化，请重新确认注册配置与验证码");return;}
      setAreaCode(fields.areaCode);
      if (fields.phoneNumber) {
        setPhoneNumber(fields.phoneNumber);
      }
      if (fields.email) {
        setEmail(fields.email);
      }

      const ticket=recovery.begin({...fields,verifyCode,loginMethod},{issuer:getRouteUrl(),generation:sessionCoordinator.generation});
      activeTicket.current=ticket;
      const payload=recovery.registration(md5);
      // Keep the original only in the private recovery draft. The editable
      // form (including confirmation password) no longer needs a copy.
      form.resetFields();setCode("");
      register(
        payload,
        {
          onSuccess(res) {
            if(!recovery.isCurrent(ticket))return;
            recovery.complete(ticket);
            message.success(t("toast.registerSuccess"));
            navigate("/chat");
          },
          onError(error){recovery.failed(ticket,error);},
        },
      );
    }
  };

  const sendSmsHandle = () => {
    if(recovery.hasAttempt()||transitionBusy.current||!pageCurrent())return;
    const epoch=pageEpoch.current,issuer=getRouteUrl();transitionBusy.current=true;
    sendSms(
      {
        email: form.getFieldValue("email") as string,
        phoneNumber: form.getFieldValue("phoneNumber") as string,
        areaCode: form.getFieldValue("areaCode") as string,
        invitationCode: form.getFieldValue("invitationCode") as string,
        usedFor: 1,
      },
      {
        onSuccess() {
          if(!pageCurrent()||pageEpoch.current!==epoch||getRouteUrl()!==issuer||recovery.hasAttempt())return;
          setCountdown(60);
        },
        onSettled(){transitionBusy.current=false;},
      },
    );
  };

  const back = () => {
    if(["submitting","checking-login"].includes(view.phase))return;
    pageActive.current=false;pageEpoch.current++;
    setFormType(0);
    if(view.phase==="idle")form.resetFields();
  };

  const isEmail = loginMethod === "email";

  const verifyTitile = isEmail
    ? "placeholder.verifyEmail"
    : "placeholder.verifyPhoneNumber";

  const receiver = view.phase!=="idle" ? view.identity : isEmail
    ? (form.getFieldValue("email") as string)
    : `${form.getFieldValue("areaCode")} ${form.getFieldValue("phoneNumber")}`;

  if(view.phase!=="idle")return <div className="flex flex-col gap-4" aria-label="注册结果确认">
    <Alert type={view.phase==="recovered"?"info":"warning"} showIcon message={view.phase==="submitting"?"正在提交注册":"确认注册结果"}
      description={<div><p>{view.notice}</p>{view.identity&&<p className="mt-2 break-all">原注册账号：{view.identity}</p>} </div>}/>
    {view.phase!=="submitting" && view.phase!=="complete" && <>
      <Input aria-label="谷歌验证码（如已绑定）" placeholder="谷歌验证码（如已绑定，可选）" maxLength={6} value={googleAuthCode}
        disabled={view.phase==="checking-login"} onChange={event=>setGoogleAuthCode(event.target.value.replace(/\D/g,"").slice(0,6))}/>
      <Button type="primary" loading={view.phase==="checking-login"} disabled={view.phase==="checking-login"} onClick={()=>{
        const ticket=recovery.currentTicket();if(ticket===undefined)return;
        try {
          const params=recovery.beginLogin(getRouteUrl(),sessionCoordinator.generation,googleAuthCode,md5);
          activeTicket.current=ticket;
          confirmLogin(params,{onSuccess(){if(!recovery.isCurrent(ticket))return;recovery.complete(ticket);setGoogleAuthCode("");navigate("/chat");},onError(error){recovery.loginFailed(ticket,error);}});
        }catch(error){message.error(error instanceof Error?error.message:"未提交登录，请检查原服务设置");}
      }}>使用原账号密码登录</Button>
      <Button disabled={view.phase==="checking-login"} onClick={back}>返回登录页（保留确认状态）</Button>
    </>}
    <p className="text-sm text-slate-600">此页面只在内存保留原注册草稿，不会保存密码。刷新或关闭后，请直接用原账号密码登录确认结果，勿重复注册。</p>
  </div>;

  return (
    <div className="flex flex-col justify-between">
      <button type="button" className="self-start text-sm text-slate-600" onClick={back}>
        <LeftOutlined rev={undefined} />
        <span className="ml-1">{t("placeholder.getBack")}</span>
      </button>
      <div className="mt-4 text-2xl font-medium">
        {registerForm === 0 && <span>{t("placeholder.register")}</span>}
        {registerForm === 1 && <span>{t(verifyTitile)}</span>}
        {registerForm === 2 && <span>{t("placeholder.setInfo")}</span>}
      </div>
      <div className="mt-4 tracking-wider text-gray-400" hidden={registerForm !== 1}>
        <span>{t("placeholder.pleaseEnterSendTo")}</span>
        <span className=" text-blue-600">{receiver}</span>
        <span>{t("placeholder.verifyValidity")}</span>
      </div>
      <Form
        form={form}
        layout="vertical"
        labelCol={{ prefixCls: "custom-form-item" }}
        onFinish={onFinish}
        autoComplete="off"
        className="mt-4"
        initialValues={{ areaCode: "+86" }}
      >
        {loginMethod === "phone" ? (
          <Form.Item label={t("placeholder.phoneNumber")} hidden={registerForm !== 0}>
            <Space.Compact className="w-full">
              <Form.Item name="areaCode" noStyle>
                <Select aria-label="国家或地区代码" options={areaCode} className="!w-28" />
              </Form.Item>
              <Form.Item name="phoneNumber" noStyle rules={[{ required: true, pattern: /^\d{6,24}$/, message: "请输入正确手机号" }]}>
                <Input aria-label="手机号" inputMode="tel" autoComplete="tel-national" allowClear placeholder={t("toast.inputPhoneNumber")} />
              </Form.Item>
            </Space.Compact>
          </Form.Item>
        ) : (
          <Form.Item
            label={t("placeholder.email")}
            name="email"
            rules={[{ required: true, type: "email", message: t("toast.inputCorrectEmail") }]}
            hidden={registerForm !== 0}
          >
            <Input allowClear placeholder={t("toast.inputEmail")} />
          </Form.Item>
        )}

        <Form.Item
          className="mb-8"
          label={t("placeholder.invitationCode")}
          name="invitationCode"
          hidden={registerForm !== 0}
        >
          <Input
            allowClear
            spellCheck={false}
            placeholder={`${t("toast.inputInvitationCode")}${
              !needInvitationCode ? t("placeholder.optional") : ""
            }`}
            className="w-full"
          />
        </Form.Item>

        <Form.Item label="验证码" hidden={registerForm !== 1} className="mb-8 mt-6">
          <Input aria-label="6位验证码" inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code}
            onChange={event => setCode(event.target.value.replace(/\D/g, "").slice(0, 6))}
            className="text-center text-2xl tracking-[.4em]" />
          <div className="mt-4 text-sm text-slate-600" aria-live="polite">
            {countdown > 0 ? `${countdown}s 后可以重新获取验证码` : <Button type="link" onClick={sendSmsHandle} loading={sending}>重新获取验证码</Button>}
          </div>
        </Form.Item>

        {registerForm === 2 && (
          <>
            <Form.Item
              label={t("placeholder.nickName")}
              name="nickname"
              hidden={registerForm !== 2}
              rules={[
                {
                  required: true,
                },
              ]}
            >
              <Input
                allowClear
                spellCheck={false}
                placeholder={t("toast.inputNickName")}
              />
            </Form.Item>

            <Form.Item
              label={t("placeholder.password")}
              name="password"
              rules={[
                {
                  required: true,
                  pattern: /^(?=.*[0-9])(?=.*[a-zA-Z]).{6,20}$/,
                  message: t("toast.passwordRules"),
                },
              ]}
            >
              <Input.Password allowClear placeholder={t("toast.inputPassword")} />
            </Form.Item>

            <Form.Item
              label={t("placeholder.confirmPassword")}
              name="password2"
              dependencies={["password"]}
              rules={[
                {
                  required: true,
                  message: t("toast.reconfirmPassword"),
                },
                ({ getFieldValue }) => ({
                  validator(_, value) {
                    if (!value || getFieldValue("password") === value) {
                      return Promise.resolve();
                    }
                    return Promise.reject(new Error(t("toast.passwordsDifferent")));
                  },
                }),
              ]}
              className="mb-8"
            >
              <Input.Password allowClear placeholder={t("toast.reconfirmPassword")} />
            </Form.Item>
          </>
        )}

        <Form.Item>
          <Button type="primary" htmlType="submit" block loading={loadingConfig || sending || verifying || registering}>
            {registerForm === 2 ? t("confirm") : t("placeholder.nextStep")}
          </Button>
        </Form.Item>
      </Form>
    </div>
  );
};

export default RegisterForm;
