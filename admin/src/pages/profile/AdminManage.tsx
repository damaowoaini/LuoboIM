import {
  addAdminAccount,
  AdminAccountItem,
  deleteAdminAccount,
  listAdminAccounts,
  resetAdminGoogleAuth,
  resetAdminPassword,
  setAdminAccountStatus,
} from '@/services/server/admin_manage';
import { PlusOutlined } from '@ant-design/icons';
import type { ActionType, ProColumns } from '@ant-design/pro-components';
import { PageContainer, ProTable } from '@ant-design/pro-components';
import { Alert, Button, Form, Input, message, Modal, Popconfirm, Space, Tag } from 'antd';
import dayjs from 'dayjs';
import md5 from 'md5';
import { useRef, useState } from 'react';

interface AddAdminFormValues {
  account: string;
  nickname?: string;
  password: string;
  confirmPassword: string;
  faceURL?: string;
}

interface ResetPasswordFormValues {
  password: string;
  confirmPassword: string;
}

const AdminManage = () => {
  const actionRef = useRef<ActionType>();
  const [modalOpen, setModalOpen] = useState(false);
  const [passwordTarget, setPasswordTarget] = useState<AdminAccountItem>();

  const columns: ProColumns<AdminAccountItem>[] = [
    { key: 'index', valueType: 'indexBorder', width: 48 },
    { title: '账号', dataIndex: 'account', copyable: true, search: false },
    { title: '昵称', dataIndex: 'nickname', search: false, renderText: (value) => value || '-' },
    { title: '管理员ID', dataIndex: 'userID', copyable: true, search: false },
    {
      title: '权限',
      dataIndex: 'level',
      width: 110,
      search: false,
      render: (_, record) =>
        record.level === 100 ? (
          <Tag color="red">超级管理员</Tag>
        ) : record.level === 50 ? (
          <Tag color="green">坐席管理员</Tag>
        ) : (
          <Tag color="blue">普通管理员</Tag>
        ),
    },
    {
      title: '状态',
      dataIndex: 'level',
      width: 90,
      search: false,
      render: (_, record) => (!record.disabled ? <Tag color="green">启用</Tag> : <Tag>已禁用</Tag>),
    },
    {
      title: '创建时间',
      dataIndex: 'createTime',
      width: 180,
      search: false,
      renderText: (value) => (value ? dayjs(Number(value)).format('YYYY-MM-DD HH:mm:ss') : '-'),
    },
    {
      title: '操作',
      valueType: 'option',
      width: 330,
      render: (_, record) => {
        const enabled = !record.disabled;
        const actions = [
          <a key="reset-password" onClick={() => setPasswordTarget(record)}>
            重置密码
          </a>,
          <Popconfirm
            key="status"
            title={`确认${enabled ? '禁用' : '启用'}管理员“${record.account}”？`}
            description={enabled ? '禁用后该账号的现有登录会话将立即失效。' : undefined}
            onConfirm={async () => {
              try {
                await setAdminAccountStatus(record.userID, !enabled);
                message.success(enabled ? '管理员已禁用' : '管理员已启用');
                actionRef.current?.reload();
              } catch (error: any) {
                message.error(
                  error?.errMsg || error?.message || `${enabled ? '禁用' : '启用'}失败`,
                );
              }
            }}
          >
            <a style={enabled ? { color: '#fa8c16' } : undefined}>{enabled ? '禁用' : '启用'}</a>
          </Popconfirm>,
          <Popconfirm
            key="reset-google-auth"
            title="确认重置该管理员的谷歌验证码？"
            description="重置后，该管理员下次登录需要重新绑定。"
            onConfirm={async () => {
              try {
                await resetAdminGoogleAuth(record.userID);
                message.success('谷歌验证码已重置');
              } catch (error: any) {
                message.error(error?.errMsg || error?.message || '重置失败');
              }
            }}
          >
            <a>重置谷歌验证</a>
          </Popconfirm>,
        ];
        if (record.level !== 100) {
          actions.push(
            <Popconfirm
              key="delete"
              title={`确认删除管理员“${record.account}”？`}
              description="删除后该账号将无法登录后台。"
              okButtonProps={{ danger: true }}
              onConfirm={async () => {
                try {
                  await deleteAdminAccount(record.userID);
                  message.success('删除成功');
                  actionRef.current?.reload();
                } catch (error: any) {
                  message.error(error?.errMsg || error?.message || '删除失败');
                }
              }}
            >
              <a style={{ color: '#ff4d4f' }}>删除</a>
            </Popconfirm>,
          );
        }
        return actions;
      },
    },
  ];

  return (
    <PageContainer>
      <Space direction="vertical" size={16} style={{ width: '100%' }}>
        <Alert
          showIcon
          type="warning"
          message="仅超级管理员可以管理后台账号"
          description="配置文件初始化的超级管理员和后台注册的普通管理员都会显示在这里。禁用后账号的现有会话立即失效。"
        />
        <ProTable<AdminAccountItem>
          rowKey="userID"
          actionRef={actionRef}
          columns={columns}
          search={false}
          headerTitle="管理员列表"
          pagination={{ defaultPageSize: 20 }}
          toolBarRender={() => [
            <Button
              key="add"
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => setModalOpen(true)}
            >
              注册管理员
            </Button>,
          ]}
          request={async (params) => {
            try {
              const res = await listAdminAccounts({
                pagination: {
                  pageNumber: params.current || 1,
                  showNumber: params.pageSize || 20,
                },
              });
              const data = (res as any)?.data || res || {};
              return {
                data: data.adminAccounts || [],
                total: data.total || 0,
                success: true,
              };
            } catch (error: any) {
              message.error(error?.errMsg || error?.message || '管理员列表加载失败');
              return { data: [], total: 0, success: false };
            }
          }}
        />
      </Space>
      {/* eslint-disable-next-line @typescript-eslint/no-use-before-define */}
      <AddAdminModal
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onSuccess={() => {
          setModalOpen(false);
          actionRef.current?.reload();
        }}
      />
      {/* eslint-disable-next-line @typescript-eslint/no-use-before-define */}
      <ResetPasswordModal
        target={passwordTarget}
        onCancel={() => setPasswordTarget(undefined)}
        onSuccess={() => setPasswordTarget(undefined)}
      />
    </PageContainer>
  );
};

interface ResetPasswordModalProps {
  target?: AdminAccountItem;
  onCancel: () => void;
  onSuccess: () => void;
}

const ResetPasswordModal = ({ target, onCancel, onSuccess }: ResetPasswordModalProps) => {
  const [form] = Form.useForm<ResetPasswordFormValues>();
  const [saving, setSaving] = useState(false);

  const handleSave = async () => {
    if (!target) return;
    try {
      const values = await form.validateFields();
      setSaving(true);
      await resetAdminPassword(target.userID, md5(values.password));
      message.success('登录密码已重置，该管理员需要重新登录');
      form.resetFields();
      onSuccess();
    } catch (error: any) {
      if (error?.errorFields) return;
      message.error(error?.errMsg || error?.message || '密码重置失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title={`重置“${target?.account || ''}”的登录密码`}
      open={Boolean(target)}
      confirmLoading={saving}
      destroyOnClose
      onCancel={() => {
        form.resetFields();
        onCancel();
      }}
      onOk={handleSave}
    >
      <Alert
        showIcon
        type="warning"
        message="重置成功后，该管理员的现有登录会话会立即失效。"
        style={{ marginBottom: 16 }}
      />
      <Form<ResetPasswordFormValues> form={form} layout="vertical" autoComplete="off">
        <Form.Item
          label="新密码"
          name="password"
          rules={[
            { required: true, message: '请输入新密码' },
            { min: 6, max: 64, message: '密码长度为6到64个字符' },
          ]}
        >
          <Input.Password placeholder="请输入新密码" maxLength={64} />
        </Form.Item>
        <Form.Item
          label="确认新密码"
          name="confirmPassword"
          dependencies={['password']}
          rules={[
            { required: true, message: '请再次输入新密码' },
            ({ getFieldValue }) => ({
              validator(_, value) {
                if (!value || getFieldValue('password') === value) return Promise.resolve();
                return Promise.reject(new Error('两次输入的密码不一致'));
              },
            }),
          ]}
        >
          <Input.Password placeholder="请再次输入新密码" maxLength={64} />
        </Form.Item>
      </Form>
    </Modal>
  );
};

interface AddAdminModalProps {
  open: boolean;
  onCancel: () => void;
  onSuccess: () => void;
}

const AddAdminModal = ({ open, onCancel, onSuccess }: AddAdminModalProps) => {
  const [form] = Form.useForm<AddAdminFormValues>();
  const [saving, setSaving] = useState(false);

  const handleSave = async () => {
    try {
      const values = await form.validateFields();
      setSaving(true);
      await addAdminAccount({
        account: values.account.trim(),
        password: md5(values.password),
        nickname: values.nickname?.trim(),
        faceURL: values.faceURL?.trim(),
      });
      message.success('管理员创建成功');
      form.resetFields();
      onSuccess();
    } catch (error: any) {
      if (error?.errorFields) return;
      message.error(error?.errMsg || error?.message || '创建失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      title="注册管理员"
      open={open}
      confirmLoading={saving}
      destroyOnClose
      onCancel={() => {
        form.resetFields();
        onCancel();
      }}
      onOk={handleSave}
    >
      <Form<AddAdminFormValues> form={form} layout="vertical" autoComplete="off">
        <Form.Item
          label="登录账号"
          name="account"
          rules={[
            { required: true, message: '请输入登录账号' },
            { min: 3, max: 64, message: '账号长度为3到64个字符' },
            { pattern: /^[A-Za-z0-9_.-]+$/, message: '账号只能包含字母、数字、下划线、点和短横线' },
          ]}
        >
          <Input placeholder="例如 operator01" maxLength={64} />
        </Form.Item>
        <Form.Item label="昵称" name="nickname" rules={[{ max: 64, message: '最多64个字符' }]}>
          <Input placeholder="例如 运营人员" maxLength={64} />
        </Form.Item>
        <Form.Item
          label="初始密码"
          name="password"
          rules={[
            { required: true, message: '请输入初始密码' },
            { min: 6, max: 64, message: '密码长度为6到64个字符' },
          ]}
        >
          <Input.Password placeholder="请输入初始密码" maxLength={64} />
        </Form.Item>
        <Form.Item
          label="确认密码"
          name="confirmPassword"
          dependencies={['password']}
          rules={[
            { required: true, message: '请再次输入密码' },
            ({ getFieldValue }) => ({
              validator(_, value) {
                if (!value || getFieldValue('password') === value) return Promise.resolve();
                return Promise.reject(new Error('两次输入的密码不一致'));
              },
            }),
          ]}
        >
          <Input.Password placeholder="请再次输入初始密码" maxLength={64} />
        </Form.Item>
        <Form.Item
          label="头像地址"
          name="faceURL"
          rules={[{ type: 'url', warningOnly: true, message: '请输入完整的头像URL' }]}
        >
          <Input placeholder="可选" maxLength={255} />
        </Form.Item>
      </Form>
    </Modal>
  );
};

export default AdminManage;
