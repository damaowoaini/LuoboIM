import { Button, Form, Input, QRCode, Select, Space, Tabs, message } from "antd";
import { t } from "i18next";
import md5 from "md5";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";

import { useLogin, useSendSms } from "@/api/login";
import login_pc from "@/assets/images/login/login_pc.png";
import login_qr from "@/assets/images/login/login_qr.png";
import {
  getEmail,
  getPhoneNumber,
  setAreaCode,
  setEmail,
  setIMProfile,
  setPhoneNumber,
} from "@/utils/storage";

import { areaCode } from "./areaCode";
import type { FormType } from "./index";
import styles from "./index.module.scss";

// 0login 1resetPassword 2register
enum LoginType {
  Password,
  VerifyCode,
}

type LoginFormProps = {
  setFormType: (type: FormType) => void;
  loginMethod: "phone" | "email";
  updateLoginMethod: (method: "phone" | "email") => void;
};

const LoginForm = ({ loginMethod, setFormType, updateLoginMethod }: LoginFormProps) => {
  const navigate = useNavigate();
  const [form] = Form.useForm();
  const [loginType, setLoginType] = useState<LoginType>(LoginType.Password);
  const { mutate: login, isPending: loginLoading } = useLogin();
  const { mutate: semdSms, isPending: sending } = useSendSms();

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

  const onFinish = (params: API.Login.LoginParams) => {
    if (loginType === 0) {
      params.password = md5(params.password ?? "");
      delete params.verifyCode;
    } else {
      delete params.password;
    }
    if (loginMethod === "phone") delete params.email; else delete params.phoneNumber;

    setAreaCode(params.areaCode);
    if (params.phoneNumber) {
      setPhoneNumber(params.phoneNumber);
    }
    if (params.email) {
      setEmail(params.email);
    }

    login(params, {
      onSuccess: (data) => {
        const { chatToken, imToken, userID } = data.data;

        navigate("/chat");
      },
      onError: () => {},
    });
  };

  const sendSmsHandle = async () => {
    try { await form.validateFields([loginMethod === "phone" ? "phoneNumber" : "email"]); } catch { return; }
    semdSms(
      {
        phoneNumber: loginMethod === "phone" ? form.getFieldValue("phoneNumber") as string : undefined,
        email: loginMethod === "email" ? form.getFieldValue("email") as string : undefined,
        areaCode: form.getFieldValue("areaCode") as string,
        usedFor: 3,
      },
      {
        onSuccess() {
          setCountdown(60);
        },
      },
    );
  };

  // const Point = () => (
  //   <div
  //     className="relative h-16 w-16 cursor-pointer rounded-md"
  //     style={{
  //       background: "linear-gradient(to bottom left, #DBEAFE 50%, white 50%)",
  //     }}
  //     onClick={() => setLoginType(loginType === 2 ? 0 : 2)}
  //   >
  //     <img
  //       src={loginType === 2 ? login_pc : login_qr}
  //       alt="login"
  //       className=" absolute left-[25px] top-[15px]"
  //     />
  //   </div>
  // );

  // if (loginType === 2) {
  //   return (
  //     <>
  //       <div className="flex flex-row items-end justify-end">
  //         <Point />
  //       </div>

  //       <div className=" flex flex-col items-center">
  //         <div className="text-xl font-medium">{t("placeholder.qrCodeLogin")}</div>
  //         <span className=" mt-3 text-sm  text-gray-400">
  //           {t("placeholder.qrCodeLoginTitle")}
  //         </span>
  //         <QRCode className="mt-8" value="https://www.openim.online/zh" size={190} />
  //       </div>
  //     </>
  //   );
  // }

  const onLoginMethodChange = (key: string) => {
    updateLoginMethod(key as "phone" | "email");
  };

  return (
    <>
      <div className="flex flex-row items-center justify-between">
        <div className="text-xl font-medium">{t("placeholder.welcome")}</div>
        {/* <Point /> */}
      </div>
      <Tabs
        className={styles["login-method-tab"]}
        activeKey={loginMethod}
        items={[
          { key: "phone", label: "手机号" },
          { key: "email", label: "邮箱" },
        ]}
        onChange={onLoginMethodChange}
      />
      <Form
        form={form}
        layout="vertical"
        onFinish={onFinish}
        autoComplete="off"
        labelCol={{ prefixCls: "custom-form-item" }}
        initialValues={{
          areaCode: "+86",
          phoneNumber: getPhoneNumber() ?? "",
          email: getEmail() ?? "",
        }}
      >
        {loginMethod === "phone" ? (
          <Form.Item label={t("placeholder.phoneNumber")}>
            <Space.Compact className="w-full">
              <Form.Item name="areaCode" noStyle>
                <Select aria-label="国家或地区代码" options={areaCode} className="!w-28" />
              </Form.Item>
              <Form.Item name="phoneNumber" noStyle rules={[{ required: true, pattern: /^\d{6,24}$/, message: "请输入正确手机号" }]}>
                <Input aria-label="手机号" autoComplete="tel-national" inputMode="tel" allowClear placeholder={t("toast.inputPhoneNumber")} />
              </Form.Item>
            </Space.Compact>
          </Form.Item>
        ) : (
          <Form.Item
            label={t("placeholder.email")}
            name="email"
            rules={[{ required: true, type: "email", message: t("toast.inputCorrectEmail") }]}
          >
            <Input allowClear placeholder={t("toast.inputEmail")} />
          </Form.Item>
        )}

        {loginType === LoginType.VerifyCode ? (
          <Form.Item label={t("placeholder.verifyCode")}>
            <Space.Compact className="w-full">
              <Form.Item name="verifyCode" noStyle rules={[{ required: true, pattern: /^\d{6}$/, message: "请输入6位验证码" }]}>
                <Input aria-label="验证码" allowClear inputMode="numeric" autoComplete="one-time-code" maxLength={6} placeholder={t("toast.inputVerifyCode")} className="w-full" />
              </Form.Item>
              <Button type="primary" onClick={sendSmsHandle} loading={sending} disabled={countdown > 0}>
                {countdown > 0
                  ? t("date.second", { num: countdown })
                  : t("placeholder.sendVerifyCode")}
              </Button>
            </Space.Compact>
          </Form.Item>
        ) : (
          <Form.Item label={t("placeholder.password")} name="password" rules={[{ required: true, message: "请输入密码" }]}>
            <Input.Password allowClear placeholder={t("toast.inputPassword")} />
          </Form.Item>
        )}

        <Form.Item label="谷歌验证码（可选）" name="googleAuthCode">
          <Input
            allowClear
            placeholder="请输入6位谷歌验证码"
            maxLength={6}
          />
        </Form.Item>

        <div className="mb-10 flex flex-row justify-between">
          <button type="button"
            className="text-sm text-slate-600"
            onClick={() => {
              setFormType(1);
            }}
          >
            {t("placeholder.forgetPassword")}
          </button>
          <button type="button"
            className="text-sm text-[var(--primary)]"
            onClick={() => {
              form.setFieldsValue({ password: undefined, verifyCode: undefined });
              setLoginType(loginType === LoginType.Password ? LoginType.VerifyCode : LoginType.Password);
            }}
          >
            {`${
              loginType === LoginType.Password
                ? t("placeholder.verifyCode")
                : t("placeholder.password")
            }${t("placeholder.login")}`}
          </button>
        </div>

        <Form.Item className="mb-4">
          <Button type="primary" htmlType="submit" block loading={loginLoading}>
            {t("placeholder.login")}
          </Button>
        </Form.Item>

        <div className="flex flex-row items-center justify-center">
          <span className="text-sm text-gray-400">
            {t("placeholder.registerToast")}
          </span>
          <button type="button"
            className="ml-1 text-sm text-[var(--primary)]"
            onClick={() => setFormType(2)}
          >
            {t("placeholder.toRegister")}
          </button>
        </div>
      </Form>
    </>
  );
};

export default LoginForm;
