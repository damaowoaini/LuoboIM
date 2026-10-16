import { LeftOutlined } from "@ant-design/icons";
import { App, Button, Form, Input, Select, Space } from "antd";
import { t } from "i18next";
import md5 from "md5";
import { useEffect, useState } from "react";

import { useReset, useSendSms, useVerifyCode } from "@/api/login";

import { areaCode } from "./areaCode";
import type { FormType } from "./index";

type ModifyFormProps = {
  loginMethod: "phone" | "email";
  setFormType: (type: FormType) => void;
};

type FormFields = {
  phoneNumber?: string;
  email?: string;
  areaCode: string;
  verifyCode: string;
  password: string;
  password2: string;
};

const ModifyForm = ({ loginMethod, setFormType }: ModifyFormProps) => {
  const { message } = App.useApp();
  const [form] = Form.useForm<FormFields>();
  const [countdown, setCountdown] = useState(0);
  const [isConfirm, setIsConfirm] = useState(false);
  const { mutate: sendSms, isPending: sending } = useSendSms();
  const { mutate: reset, isPending: resetting } = useReset();
  const { mutate: verifyCode, isPending: verifying } = useVerifyCode();

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

  const onFinish = (fields: FormFields) => {
    if (!fields.verifyCode) return;
    if (!isConfirm) {
      verifyCode(
        {
          ...fields,
          usedFor: 2,
        },
        {
          onSuccess() {
            setIsConfirm(true);
          },
        },
      );
    } else {
      reset(
        { ...fields, password: md5(fields.password) },
        {
          onSuccess() {
            message.success(t("toast.updatePasswordSuccess"));
            setFormType(0);
          },
        },
      );
    }
  };

  const sendSmsHandle = async () => {
    try { await form.validateFields([loginMethod === "phone" ? "phoneNumber" : "email"]); } catch { return; }
    sendSms(
      {
        phoneNumber: loginMethod === "phone" ? form.getFieldValue("phoneNumber") as string : undefined,
        email: loginMethod === "email" ? form.getFieldValue("email") as string : undefined,
        areaCode: form.getFieldValue("areaCode") as string,
        usedFor: 2,
      },
      {
        onSuccess() {
          setCountdown(60);
        },
      },
    );
  };

  const back = () => {
    setFormType(0);
    form.resetFields();
  };

  return (
    <div className="flex flex-col justify-between">
      <button type="button" className="self-start text-sm text-slate-600" onClick={back}>
        <LeftOutlined rev={undefined} />
        <span className="ml-1">{t("placeholder.getBack")}</span>
      </button>
      <div className="mt-6 text-2xl font-medium">{t("placeholder.forgetPassword")}</div>
      <Form
        form={form}
        layout="vertical"
        labelCol={{ prefixCls: "custom-form-item" }}
        onFinish={onFinish}
        autoComplete="off"
        className="mt-6"
        initialValues={{ areaCode: "+86" }}
      >
        {isConfirm && (
          <>
            <Form.Item
              label={t("placeholder.password")}
              name="password"
              help={
                <span className=" text-xs text-gray-400">
                  {t("toast.passwordRules")}
                </span>
              }
              rules={[
                {
                  required: true,
                  pattern: /^(?=.*[0-9])(?=.*[a-zA-Z]).{6,20}$/,
                  message: t("toast.passwordRules"),
                },
              ]}
              hidden={!isConfirm}
            >
              <Input.Password allowClear placeholder={t("toast.inputPassword")} />
            </Form.Item>
            <Form.Item
              label={t("placeholder.confirmPassword")}
              name="password2"
              rules={[
                {
                  required: true,
                  message: "Please confirm your password",
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
            >
              <Input.Password allowClear placeholder={t("toast.reconfirmPassword")} />
            </Form.Item>
          </>
        )}
        {loginMethod === "phone" ? (
          <Form.Item label={t("placeholder.phoneNumber")} required hidden={isConfirm}>
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
            required
            hidden={isConfirm}
            label={t("placeholder.email")}
            name="email"
            rules={[{ required: true, type: "email", message: t("toast.inputCorrectEmail") }]}
          >
            <Input allowClear placeholder={t("toast.inputEmail")} />
          </Form.Item>
        )}

        <Form.Item
          label={t("placeholder.verifyCode")}
          hidden={isConfirm}
          required
        >
          <Space.Compact className="w-full">
            <Form.Item name="verifyCode" noStyle rules={[{ required: true, pattern: /^\d{6}$/, message: "请输入6位验证码" }]}><Input aria-label="验证码" allowClear inputMode="numeric" autoComplete="one-time-code" maxLength={6} placeholder={t("toast.inputVerifyCode")} className="w-full" /></Form.Item>
            <Button type="primary" onClick={sendSmsHandle} loading={sending} disabled={countdown > 0}>
              {countdown > 0
                ? t("date.second", { num: countdown })
                : t("placeholder.sendVerifyCode")}
            </Button>
          </Space.Compact>
        </Form.Item>
        <Form.Item className="mt-20">
          <Button type="primary" htmlType="submit" block loading={resetting || verifying}>
            {isConfirm ? t("confirm") : t("placeholder.nextStep")}
          </Button>
        </Form.Item>
      </Form>
    </div>
  );
};

export default ModifyForm;
