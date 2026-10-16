import { forceLogout } from '@/services/server/user_manage';
import AdminUserCreation from './AdminUserCreation';
import {
  Avatar,
  Button,
  Checkbox,
  Drawer,
  message,
} from 'antd';
type CheckboxValueType = string | number | boolean;
import type { FC } from 'react';
import { memo, useEffect, useState } from 'react';
import type { DrawerOptions } from './UserList';

import { avatarList } from '@/utils/avatar';

type UserActionProps = {
  drawerOptions: DrawerOptions;
  closeDrawer: () => void;
  reload: () => void;
};

const KickPage = memo(({ drawerOptions, closeDrawer, reload }: UserActionProps) => {
  const [checkOptions, setCheckOptions] = useState([] as any);

  let checkedValue = [] as number[];

  const getOptions = () => {
    const tmpArr = [];
    if (drawerOptions.selectUser?.onlineStr?.includes('Web')) {
      tmpArr.push({
        label: 'Web端',
        value: 5,
      });
    }
    if (drawerOptions.selectUser?.onlineStr?.includes('Android')) {
      tmpArr.push({
        label: 'Android',
        value: 2,
      });
    }
    if (drawerOptions.selectUser?.onlineStr?.includes('IOS')) {
      tmpArr.push({
        label: 'IOS',
        value: 1,
      });
    }
    if (drawerOptions.selectUser?.onlineStr?.includes('Windows')) {
      tmpArr.push({
        label: 'Windows',
        value: 3,
      });
    }
    if (drawerOptions.selectUser?.onlineStr?.includes('Linux')) {
      tmpArr.push({
        label: 'Linux',
        value: 7,
      });
    }
    if (drawerOptions.selectUser?.onlineStr?.includes('OSX')) {
      tmpArr.push({
        label: 'OSX',
        value: 4,
      });
    }
    setCheckOptions(tmpArr);
  };

  useEffect(() => {
    if (drawerOptions.selectUser) {
      getOptions();
    }
  }, [drawerOptions.selectUser]);

  const onChange = (checkedValues: CheckboxValueType[]) => {
    checkedValue = checkedValues as any;
  };

  const kickOut = () => {
    if (checkedValue.length === 0) {
      message.warning('请先选择平台');
      return;
    }
    const promissArr = [] as any;
    checkedValue.forEach((value) => {
      promissArr.push(forceLogout({ userID: drawerOptions.selectUser!.userID, platformID: value }));
    });
    Promise.all(promissArr).then(() => {
      message.success('操作成功！');
      reload();
      closeDrawer();
    });
    // .catch(() => message.error('操作失败！'));
  };

  return (
    <div className="d px-3">
      <div className="d flex border-b items-center pb-5">
        {/* <Avatar
          shape="square"
          size={55}
          className="min-w-[55px]"
          src={drawerOptions.selectUser?.avatar}
        /> */}
        {(drawerOptions.selectUser?.avatar || '').length ? (
          <Avatar
            shape="square"
            size={55}
            className="min-w-[55px]"
            src={
              avatarList.find((avator) => avator.name === drawerOptions.selectUser?.avatar)?.src ||
              drawerOptions.selectUser?.avatar
            }
          ></Avatar>
        ) : (
          <Avatar
            shape="square"
            style={{ backgroundColor: '#4096FF' }}
            size={55}
            className="min-w-[55px]"
          >
            {drawerOptions.selectUser?.nickname.split('')[0]}
          </Avatar>
        )}
        <div className="flex flex-col justify-evenly ml-3 h-[55px]">
          <div className="truncate max-w-[200px]">{drawerOptions.selectUser?.nickname}</div>
          <div>{drawerOptions.selectUser?.userID}</div>
        </div>
      </div>
      <div className="pt-5">
        <Checkbox.Group
          className="flex flex-col kick_check"
          options={checkOptions}
          onChange={onChange}
        />
      </div>
      <div className="w-full text-right">
        <Button type="primary" onClick={kickOut}>
          强制下线
        </Button>
      </div>
    </div>
  );
});

const UserActionDrawer: FC<UserActionProps> = (props) => {
  const { drawerOptions, closeDrawer } = props;
  const [creating,setCreating]=useState(false);

  return (
    <Drawer
      title={drawerOptions.selectUser ? '强制用户下线' : '添加新用户'}
      placement="right"
      closable={!creating}
      maskClosable={!creating}
      keyboard={!creating}
      destroyOnClose
      onClose={()=>{if(!creating)closeDrawer();}}
      open={drawerOptions.visible}
    >
      {drawerOptions.selectUser ? (
        <KickPage {...props} />
      ) : (
        drawerOptions.visible ? <AdminUserCreation reload={props.reload} onBusyChange={setCreating}/> : null
      )}
    </Drawer>
  );
};

export default UserActionDrawer;
