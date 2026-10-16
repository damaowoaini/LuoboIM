import { adminSessions } from '@/core/adminSession';
import { changeOwnAdminPassword } from '@/services/server/user_manage';
import { PageContainer } from '@ant-design/pro-components';
import { history, useModel } from '@umijs/max';
import { Button, Card, Form, Input, message } from 'antd';
import md5 from 'md5';
import { useState } from 'react';

type FormFields = { currentPassword: string; password: string; password2: string };
const Profile = () => {
  const [form] = Form.useForm<FormFields>();
  const { setInitialState } = useModel('@@initialState');
  const [saving, setSaving] = useState(false);
  const onFinish = async (fields: FormFields) => {
    setSaving(true);
    try {
      await changeOwnAdminPassword(md5(fields.currentPassword), md5(fields.password));
      form.resetFields();
      adminSessions!.clear();
      await setInitialState(state => ({ ...state, currentUser: undefined }));
      message.success('密码已修改，请重新登录');
      history.replace('/login');
    } finally { setSaving(false); }
  };
  return <PageContainer><Card><Form form={form} layout="vertical" onFinish={onFinish} className="mt-4" style={{width:300,maxWidth:'100%'}}>
    <Form.Item label="当前密码" name="currentPassword" rules={[{required:true,message:'请输入当前密码'}]}><Input.Password autoComplete="current-password" placeholder="请输入当前密码" /></Form.Item>
    <Form.Item label="新密码" name="password" rules={[{required:true,min:6,max:64,message:'请输入6至64位新密码'}]}><Input.Password autoComplete="new-password" placeholder="请输入新密码" /></Form.Item>
    <Form.Item label="确认密码" name="password2" dependencies={['password']} rules={[{required:true,message:'请再次确认新密码'},({getFieldValue})=>({validator:(_,value)=>!value||getFieldValue('password')===value?Promise.resolve():Promise.reject(new Error('两次输入的密码不一致'))})]} className="mb-8"><Input.Password autoComplete="new-password" placeholder="请再次确认新密码" /></Form.Item>
    <Form.Item><Button type="primary" htmlType="submit" block loading={saving}>保存</Button></Form.Item>
  </Form></Card></PageContainer>;
};
export default Profile;
