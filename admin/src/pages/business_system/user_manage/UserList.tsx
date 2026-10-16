import GoogleAuthModal from '@/components/GoogleAuthModal';
import { onlineDeviceLabel } from '@/core/userManagement';
import { adminSessions } from '@/core/adminSession';
import { updateCapturedUserAvatar } from '@/core/adminUserAvatar';
import { handleFileUpload } from '@/services/admin/upload';
import { batchGetGoogleAuthStatus } from '@/services/server/google_auth';
import {
  getUserList,
  getUsersOnlineStatus,
  resetUserPassword,
  updateBlock,
  updateUserInfo,
} from '@/services/server/user_manage';
import { DefaultAvatar, defaultAvatarStr } from '@/utils/avatar';
import type { ActionType, ProColumns } from '@ant-design/pro-components';
import { PageContainer, ProTable } from '@ant-design/pro-components';
import { Button, Input, InputRef, message, Modal, Upload } from 'antd';
import md5 from 'md5';
import { UploadRequestOption } from 'rc-upload/lib/interface';
import { useMemo, useRef, useState } from 'react';
import type { UserListItem } from './data';
import UserActionDrawer from './UserActionDrawer';
import UserProfileEditor from './UserProfileEditor';
import UserManagementActions from './UserManagementActions';

export type DrawerOptions = {
  visible: boolean;
  selectUser: UserListItem | undefined;
};

const UserList = () => {
  const actionRef = useRef<ActionType>();
  const [drawerOptions, setDrawerOptions] = useState<DrawerOptions>({
    visible: false,
    selectUser: undefined,
  });
  const [resetPwdData, setResetPwdData] = useState({
    userID: '',
    modalVisible: false,
    confirmLoading: false,
  });
  const passwordRef = useRef<InputRef>(null);
  const [googleAuthModal, setGoogleAuthModal] = useState({
    visible: false,
    userID: '',
  });
  const [googleAuthStatus, setGoogleAuthStatus] = useState<Record<string, boolean>>({});
  const [editingUser, setEditingUser] = useState<UserListItem>();

  const customUpload = async (userID: string, data: UploadRequestOption) => {
    try {
      await updateCapturedUserAvatar(adminSessions!,userID,()=>handleFileUpload(data),updateUserInfo,
        ()=>{actionRef.current?.reload();});
    } catch { /* The shared request handler displays a safe error. */ }
  };

  const columns: ProColumns<UserListItem>[] = useMemo(
    () => [
      {
        key: 'index',
        dataIndex: 'index',
        valueType: 'indexBorder',
        width: 48,
      },
      {
        title: '用户ID/手机号',
        key: 'keyword',
        dataIndex: 'keyword',
        editable: false,
        hideInTable: true,
        align: 'center',
      },
      {
        title: '用户头像',
        dataIndex: 'faceURL',
        valueType: 'avatar',
        key: 'faceURL',
        hideInSearch: true,
        editable: false,
        align: 'center',
        render(_, record) {
          return (
            <Upload
              accept="image/*"
              customRequest={(option) => customUpload(record.userID, option)}
              showUploadList={false}
              className="cursor-pointer"
            >
              <DefaultAvatar faceURL={record.faceURL} nickname={record.nickname} />
            </Upload>
          );
        },
      },
      {
        title: '昵称',
        key: 'nickname',
        dataIndex: 'nickname',
        hideInSearch: true,
        align: 'center',
      },
      {
        title: '用户ID',
        key: 'userID',
        dataIndex: 'userID',
        hideInSearch: true,
        editable: false,
        align: 'center',
      },
      {
        title: '性别',
        key: 'gender',
        dataIndex: 'gender',
        hideInSearch: true,
        valueType: 'select',
        align: 'center',
        request: async () => {
          return [
            {
              label: '保密',
              value: 0,
            },
            {
              label: '男',
              value: 1,
            },
            {
              label: '女',
              value: 2,
            },
          ];
        },
      },
      {
        title: '手机号',
        key: 'phoneNumber',
        dataIndex: 'phoneNumber',
        hideInSearch: true,
        editable: false,
        align: 'center',
      },
      {
        title: '用户角色',
        key: 'level',
        dataIndex: 'level',
        hideInSearch: true,
        valueType: 'select',
        align: 'center',
        request: async () => {
          return [
            {
              label: '普通用户',
              value: 1,
            },
            {
              label: '高级用户',
              value: 100,
            },
          ];
        },
      },
      {
        title: '在线状态',
        key: 'onlineStr',
        dataIndex: 'onlineStr',
        hideInSearch: true,
        editable: false,
        align: 'center',
      },
      {
        title: '谷歌验证码',
        key: 'googleAuth',
        hideInSearch: true,
        editable: false,
        align: 'center',
        width: 100,
        render: (_, record) => {
          const status = googleAuthStatus[record.userID];
          if (status === undefined) {
            return <span style={{ color: '#999' }}>-</span>;
          }
          return status ? (
            <span style={{ color: '#52c41a' }}>已绑定</span>
          ) : (
            <span style={{ color: '#999' }}>未绑定</span>
          );
        },
      },
      {
        title: '操作',
        valueType: 'option',
        key: 'option',
        align: 'center',
        width: 190,
        render: (text, record, _, action) => {
          return (
            <UserManagementActions user={record} onEdit={() => setEditingUser({...record})}
              onResetPassword={() => setResetPwdData({userID:record.userID,modalVisible:true,confirmLoading:false})}
              onAuthenticator={() => setGoogleAuthModal({visible:true,userID:record.userID})}
              onBlock={async() => {await updateBlock(record.userID);action?.reload();message.success('操作成功！');}} />
          );
        },
      },
    ],
    [googleAuthStatus],
  );

  const closeDrawer = () => {
    setDrawerOptions({
      visible: false,
      selectUser: undefined,
    });
  };

  const reload = () => {
    actionRef.current?.reload();
  };

  const handleOk = () => {
    if (!passwordRef.current?.input?.value) {
      message.warning('请输入新密码');
      return;
    }
    setResetPwdData({
      ...resetPwdData,
      confirmLoading: true,
    });
    resetUserPassword({
      userID: resetPwdData.userID,
      newPassword: md5(passwordRef.current?.input?.value),
    })
      .then(() => {
        message.success('设置成功！');
        setResetPwdData({
          userID: '',
          modalVisible: false,
          confirmLoading: false,
        });
      })
      .catch(() => {
        setResetPwdData({
          userID: '',
          modalVisible: false,
          confirmLoading: false,
        });
      });
  };

  const handleCancel = () => {
    setResetPwdData({
      userID: '',
      modalVisible: false,
      confirmLoading: false,
    });
  };

  const attachInfo = async (userList: UserListItem[]) => {
    const userIDs = userList.map((user) => user.userID);
    userList.forEach((user, idx) => {
      user.index = idx;
      user.avatar = defaultAvatarStr(user.faceURL);
      user.onlineStr = '状态未知';
    });
    if (!userIDs.length) return;

    // 批量查询在线状态
    try {
      const { data } = await getUsersOnlineStatus(userIDs);
      userList.forEach(user => { user.onlineStr = '离线'; });
      (data ?? []).forEach((status) => {
        const user = userList.find(user => user.userID === status.userID);
        if (user) user.onlineStr = onlineDeviceLabel(status.singlePlatformToken);
      });
    } catch { /* Availability failure is unknown, rather than proof of offline. */ }

    // 批量查询谷歌验证码状态
    try {
      const googleAuthRes = await batchGetGoogleAuthStatus(userIDs);
      if (googleAuthRes.data) {
        setGoogleAuthStatus(googleAuthRes.data);
      }
    } catch { /* Existing rows stay visible when optional status is unavailable. */ }
  };

  return (
    <PageContainer>
      <ProTable<UserListItem>
        columns={columns}
        actionRef={actionRef}
        cardBordered
        columnsState={{
          defaultValue: {
            option: {
              fixed: 'right',
            },
          },
        }}
        request={async (params = {}, sort, filter) => {
          void 0;
          const { data } = await getUserList({
            keyword: params.keyword as string,
            pagination: {
              pageNumber: params.current as number,
              showNumber: params.pageSize as number,
            },
          });
          const tmpData = data.users ?? [];
          await attachInfo(tmpData);
          return {
            data: tmpData,
            success: true,
            total: data.total,
          };
        }}
        rowKey="userID"
        search={{
          labelWidth: 'auto',
        }}
        pagination={{
          defaultPageSize: 10,
          showQuickJumper: true,
          onChange: (page) => void 0,
        }}
        dateFormatter="number"
        scroll={{ x: 'max-content' }}
        toolbar={{
          actions: [
            <Button
              key="key"
              type="primary"
              onClick={() => {
                setDrawerOptions({
                  visible: true,
                  selectUser: undefined,
                });
              }}
            >
              添加新用户
            </Button>,
          ],
          settings: [],
        }}
      />
      <UserProfileEditor user={editingUser} onCancel={() => setEditingUser(undefined)}
        onSave={async changes => { await updateUserInfo(changes); reload(); }} />
      <UserActionDrawer closeDrawer={closeDrawer} reload={reload} drawerOptions={drawerOptions} />
      {resetPwdData.modalVisible && (
        <Modal
          title={`重置用户密码 · ${resetPwdData.userID}`}
          open={resetPwdData.modalVisible}
          confirmLoading={resetPwdData.confirmLoading}
          onOk={handleOk}
          onCancel={handleCancel}
        >
          <p>将为此用户设置新密码。请先核实账号归属，再通过可信渠道告知账号本人。</p>
          <div className="w-full flex justify-center">
            <Input.Password ref={passwordRef} placeholder="请输入新密码" autoComplete="new-password" />
          </div>
        </Modal>
      )}
      <GoogleAuthModal
        visible={googleAuthModal.visible}
        userID={googleAuthModal.userID}
        onCancel={() => {
          setGoogleAuthModal({
            visible: false,
            userID: '',
          });
        }}
        onSuccess={() => {
          setGoogleAuthModal({
            visible: false,
            userID: '',
          });
          message.success('操作成功');
        }}
      />
    </PageContainer>
  );
};

export default UserList;
