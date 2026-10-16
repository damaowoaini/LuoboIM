import { changedUserProfile } from '@/core/userManagement';
import { Alert, Form, Input, Modal, Select, Typography } from 'antd';
import { useEffect, useRef, useState } from 'react';
import type { UserListItem } from './data';

type ProfilePatch = ReturnType<typeof changedUserProfile>;
export default function UserProfileEditor({ user, onCancel, onSave }: {
  user?: UserListItem;
  onCancel: () => void;
  onSave: (changes: ProfilePatch) => Promise<unknown>;
}) {
  const [form] = Form.useForm();
  const [pending, setPending] = useState(false);
  const inFlight = useRef(false);
  useEffect(() => {
    if (user) form.setFieldsValue({nickname:user.nickname, gender:user.gender, level:user.level});
  }, [user, form]);

  const submit = async (values: {nickname:string;gender:number;level?:number}) => {
    if (!user || inFlight.current) return;
    inFlight.current = true;
    setPending(true);
    try {
      const changes = changedUserProfile(user.userID, {...values,nickname:values.nickname.trim()}, user);
      if (Object.keys(changes).length > 1) await onSave(changes);
      onCancel();
    } catch { /* Shared request handling reports errors; retain the user's unsaved form. */ }
    finally { inFlight.current = false; setPending(false); }
  };

  return <Modal title="编辑用户资料" open={!!user} onCancel={onCancel} onOk={() => form.submit()}
    okText="保存" cancelText="取消" confirmLoading={pending} destroyOnClose
    width={520} centered style={{maxWidth:'calc(100vw - 32px)'}}
    styles={{body:{maxHeight:'calc(100dvh - 200px)',overflowY:'auto'}}}
    maskClosable={!pending} keyboard={!pending} closable={!pending} cancelButtonProps={{disabled:pending}}>
    <Typography.Paragraph type="secondary" style={{overflowWrap:'anywhere'}}>
      {user?.nickname} · 用户ID：{user?.userID}
    </Typography.Paragraph>
    {pending && <Alert type="info" showIcon message="正在保存资料，请稍候。完成前暂不能关闭或重复提交。" style={{marginBottom:16}} />}
    <Form form={form} layout="vertical" onFinish={submit} disabled={pending} preserve={false}>
      <Form.Item name="nickname" label="昵称" rules={[{required:true,whitespace:true,message:'请输入昵称'}]}>
        <Input maxLength={64} autoComplete="off" />
      </Form.Item>
      <Form.Item name="gender" label="性别" rules={[{required:true,message:'请选择性别'}]}>
        <Select options={[{label:'保密',value:0},{label:'男',value:1},{label:'女',value:2}]} />
      </Form.Item>
      <Form.Item name="level" label="用户角色">
        <Select options={[{label:'普通用户',value:1},{label:'高级用户',value:100}]} />
      </Form.Item>
    </Form>
  </Modal>;
}
