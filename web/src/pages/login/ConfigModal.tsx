import { Button, Form, Input, Modal, message } from "antd";
import React, { memo } from "react";
import { getRouteUrl, saveRouteUrl, validateRouteUrl } from "@/core/session/route";
import { clearIMProfile } from "@/utils/storage";
interface ConfigModalProps { visible: boolean; close: () => void; }
const ConfigModal: React.FC<ConfigModalProps> = ({ visible, close }) => {
  const save = async ({ routeUrl }: { routeUrl: string }) => {
    try { const url = validateRouteUrl(routeUrl); await clearIMProfile(); saveRouteUrl(url); window.location.reload(); }
    catch (error) { message.error(error instanceof Error ? error.message : "保存失败，请重试"); }
  };
  return <Modal title="登录服务设置" open={visible} onCancel={close} footer={null} centered>
    <Form layout="vertical" initialValues={{ routeUrl: getRouteUrl() }} onFinish={save}>
      <Form.Item label="服务地址" name="routeUrl" rules={[{ required: true, message: "请输入登录服务地址" }]}><Input placeholder="https://登录服务域名" autoComplete="url" /></Form.Item>
      <p className="mb-4 text-sm text-gray-500">保存后重新登录。登录成功后将自动连接你的账号所属服务。</p>
      <Button htmlType="submit" type="primary">保存并重新登录</Button>
    </Form>
  </Modal>;
};
export default memo(ConfigModal);
