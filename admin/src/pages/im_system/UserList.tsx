import type { UserListItem } from '@/pages/business_system/user_manage/data';
import { changedUserProfile, onlineDeviceLabel } from '@/core/userManagement';
import UserActionDrawer from '@/pages/business_system/user_manage/UserActionDrawer';
import { getUserList } from '@/services/server/im_user_manage';
import { getUsersOnlineStatus, updateUserInfo } from '@/services/server/user_manage';
import { DefaultAvatar, defaultAvatarStr } from '@/utils/avatar';
import type { ActionType, ProColumns } from '@ant-design/pro-components';
import { PageContainer, ProTable } from '@ant-design/pro-components';
import { history } from '@umijs/max';
import { Space, Spin } from 'antd';
import moment from 'moment';
import { lazy, Suspense, useMemo, useRef, useState } from 'react';

  // The readonly preview remains lazy to avoid loading its media renderers on the list.
const PreviewMessageModal = lazy(() => import('./PreviewMessageModal/PreviewMessageModal'));

export type DrawerOptions = {
  visible: boolean;
  selectUser: UserListItem | undefined;
};

const UserList = () => {
  const actionRef = useRef<ActionType>();
  const [selectedUserID, setSelectUserID] = useState('');
  const [drawerOptions, setDrawerOptions] = useState<DrawerOptions>({
    visible: false,
    selectUser: undefined,
  });

  const columns: ProColumns<UserListItem>[] = useMemo(
    () => [
      {
        title: '序号',
        key: 'index',
        dataIndex: 'index',
        valueType: 'indexBorder',
        width: 48,
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
          return <DefaultAvatar faceURL={record.faceURL} nickname={record.nickname} />;
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
        editable: false,
        hideInSearch: true,
        align: 'center',
      },
      {
        title: '在线状态',
        key: 'onlineStr',
        dataIndex: 'onlineStr',
        hideInSearch: true,
        align: 'center',
      },
      {
        title: '操作',
        valueType: 'option',
        key: 'option',
        align: 'center',
        render: (_, record) => (
          <Space>
            <a
              key={'onlineStr'}
              href="#"
              onClick={() => {
                if (record.onlineStr?.length) {
                  setDrawerOptions({
                    visible: true,
                    selectUser: record,
                  });
                }
              }}
            >
              强制下线
            </a>
            <a
              key="friend"
              onClick={() =>
                history.push('/im_system/user_manage/relation_list?userID=' + record.userID)
              }
            >
              关系链
            </a>
            {/* 203服务器新版本 先不做 */}
            <a key="msg" onClick={() => setSelectUserID(record.userID)}>
              会话
            </a>
          </Space>
        ),
      },
    ],
    [],
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

  const attachInfo = async (userList: UserListItem[]) => {
    // 先给默认值，避免在线状态接口卡住/失败时整表一直 loading
    userList.forEach((user, idx) => {
      user.index = idx;
      user.avatar = defaultAvatarStr(user.faceURL);
      user.onlineStr = '状态未知';
    });
    if (!userList.length) return;

    try {
      const onlineReq = getUsersOnlineStatus(userList.map((user) => user.userID));
      // 在线状态最多等 8s，超时直接降级，避免整页卡死
      const { data } = await Promise.race([
        onlineReq,
        new Promise<never>((_, reject) => {
          setTimeout(() => reject(new Error('getUsersOnlineStatus timeout')), 8000);
        }),
      ]);
      userList.forEach(user => { user.onlineStr = '离线'; });
      (data ?? []).forEach((status) => {
        const user = userList.find((item) => item.userID === status.userID);
        if (user) {
          user.onlineStr = onlineDeviceLabel(status.singlePlatformToken);
        }
      });
    } catch { /* Keep unknown status rather than claim the user is offline. */ }
  };

  return (
    <PageContainer>
      <ProTable<UserListItem>
        columns={columns}
        actionRef={actionRef}
        cardBordered
        toolbar={{
          actions: [],
          settings: [],
        }}
        search={false}
        columnsState={{
          defaultValue: {
            option: {
              fixed: 'right',
            },
          },
        }}
        request={async (params = {}) => {
          try {
            const { data } = await getUserList({
              keyword: params.userID ?? '',
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
          } catch {
            return { data: [], success: false, total: 0 };
          }
        }}
        editable={{
          type: 'multiple',
          actionRender: (row, config, doms) => {
            return [doms.save, doms.cancel];
          },
          onSave: (key, record, original) => {
            const options = changedUserProfile(record.userID,
              { ...record, birth: record.birth ? moment(record.birth).valueOf() : 0 },
              { ...original, birth: original.birth ? moment(original.birth).valueOf() : 0 });
            return Object.keys(options).length > 1 ? updateUserInfo(options) : Promise.resolve();
          },
        }}
        rowKey="userID"
        pagination={{
          defaultPageSize: 10,
          showQuickJumper: true,
          onChange: (page) => void 0,
        }}
        dateFormatter="number"
        scroll={{ x: 'max-content' }}
      />
      {selectedUserID && (
        <Suspense
          fallback={
            <div style={{ padding: 24, textAlign: 'center' }}>
              <Spin tip="会话组件加载中..." />
            </div>
          }
        >
          <PreviewMessageModal
            userID={selectedUserID}
            closeModal={() => setSelectUserID('')}
          />
        </Suspense>
      )}
      <UserActionDrawer closeDrawer={closeDrawer} reload={reload} drawerOptions={drawerOptions} />
    </PageContainer>
  );
};

export default UserList;
