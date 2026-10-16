import { DownOutlined } from '@ant-design/icons';
import { Button, Dropdown, Modal, Space } from 'antd';
import { useRef, useState } from 'react';
import type { UserListItem } from './data';

export default function UserManagementActions({user,onEdit,onResetPassword,onAuthenticator,onBlock}: {
  user: UserListItem;
  onEdit: () => void;
  onResetPassword: () => void;
  onAuthenticator: () => void;
  onBlock: () => Promise<unknown>;
}) {
  const [confirming,setConfirming] = useState(false);
  const [pending,setPending] = useState(false);
  const [modal,contextHolder] = Modal.useModal();
  const confirmationOpen = useRef(false);
  const disabled = confirming || pending;
  const reason = pending ? '该账号的操作正在提交，请稍候' : confirming ? '请先完成或取消当前确认' : undefined;
  const confirmBlock = () => {
    if (confirmationOpen.current) return;
    confirmationOpen.current = true;
    setConfirming(true);
    modal.confirm({
      title:`确认${user.forbidden ? '解封' : '禁用'}用户`,
      content:<><p>{user.nickname} · 用户ID：{user.userID}</p><p>{user.forbidden ? '解封后该用户将恢复账号使用资格。' : '禁用后该用户将无法正常使用账号，请确认处理对象。'}</p></>,
      okText:user.forbidden ? '确认解封' : '确认禁用',cancelText:'取消',okButtonProps:{danger:!user.forbidden},
      onOk:async() => {setPending(true);try {await onBlock();}finally {setPending(false);}},
      afterClose:() => {confirmationOpen.current=false;setConfirming(false);},
    });
  };
  return <>{contextHolder}<Space size={4} wrap>
    <Button type="link" size="small" disabled={disabled} title={reason || '编辑用户资料'} onClick={onEdit}>编辑</Button>
    <Dropdown trigger={['click']} disabled={disabled} menu={{items:[
      {key:'reset',label:'重置密码',onClick:onResetPassword},
      {key:'googleAuth',label:'身份验证器管理',onClick:onAuthenticator},
      {type:'divider'},
      {key:'block',danger:!user.forbidden,label:user.forbidden ? '解封用户' : '禁用用户',onClick:confirmBlock},
    ]}}>
      <Button type="link" size="small" disabled={disabled} title={reason || '重置密码、身份验证器与账号禁用管理'}>
        更多操作 <DownOutlined />
      </Button>
    </Dropdown>
  </Space></>;
}
