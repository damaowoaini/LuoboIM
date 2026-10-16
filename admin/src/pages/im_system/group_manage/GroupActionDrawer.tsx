import { Alert, Avatar, Button, Drawer, Form, Input, message, Select, Tag, Upload } from 'antd';
import type { FC } from 'react';
import { memo, useEffect, useRef, useState } from 'react';

import avatar_upload from '@/assets/images/avatar_upload.png';
import { AllowType, GroupVerificationType } from '@/constants/enum';
import { uploadAdminFile } from '@/services/admin/upload';
import {adminSessions} from '@/core/adminSession';
import {groupCreatePayload,groupEditPayload,repeatedGroupSelection} from '@/core/groupMutation';
import { createGroup, updateGroup } from '@/services/server/group_manage';
import { UploadRequestOption } from 'rc-upload/lib/interface';
import type { GroupInfo } from './data';
import type { SelectedListItem, SelectModalOptions } from './SelectUserModal';
import SelectUserModal from './SelectUserModal';

export type DrawerOptions = {
  visible: boolean;
  opType: 'create' | 'edit';
  groupInfo?: GroupInfo;
};

type GroupActionProps = {
  drawerOptions: DrawerOptions;
  closeDrawer: () => void;
  reload: () => void;
};

enum GroupType {
  Nomal = 0,
  Working = 2,
}

type AddGroupFormFields = {
  groupName: string;
  groupType: GroupType;
  memberList: any[];
  groupInfoForSet: any;
  ownerUserID: string;
  groupInfo: any;
  needVerification: number;
  groupID: string;
  notification: string;
  introduction: string;
  faceURL: string;
  ex: string;
  lookMemberInfo: number;
  applyMemberFriend: number;
  adminUserIDs?:string[];
  memberUserIDs?:string[];
};

type OpGroupPageProps = {
  opType: 'create' | 'edit';
  groupInfo?: GroupInfo;
  closeDrawer: () => void;
  reload: () => void;
};

const OpGroupPage: FC<OpGroupPageProps> = memo(({ closeDrawer, reload, opType, groupInfo }) => {
  const [uploadUrl, setUploadUrl] = useState('');
  const [uploading,setUploading]=useState(false),[saving,setSaving]=useState(false),[notice,setNotice]=useState('');
  const [sessionValid,setSessionValid]=useState(!!adminSessions?.current);
  const mounted=useRef(true),generation=useRef(adminSessions?.generation),savingRef=useRef(false),uploadingRef=useRef(false),uploadTurn=useRef(0),uploadController=useRef<AbortController>();
  const target=groupInfo?.groupID||'create',targetRef=useRef(target);targetRef.current=target;
  const current=()=>mounted.current&&generation.current===adminSessions?.generation&&!!adminSessions?.current&&targetRef.current===target;
  useEffect(()=>{
    mounted.current=true;
    const unsubscribe=adminSessions?.onChange(()=>{uploadController.current?.abort();setSessionValid(false);setNotice('管理员会话已变化，请重新打开群设置。');});
    return()=>{mounted.current=false;uploadController.current?.abort();unsubscribe?.();};
  },[]);
  const [selectModalOptions, setSelectModalOptions] = useState<SelectModalOptions>({
    visible: false,
    selectType: 'member',
  });
  const [selectItems, setSelectItems] = useState({
    owner: {
      data: [],
    } as SelectedListItem,
    admin: {
      data: [],
    } as SelectedListItem,
    member: {
      data: [],
    } as SelectedListItem,
  });
  const [form] = Form.useForm();

  const closeSelectModal = () => {
    setSelectModalOptions({
      visible: false,
      selectType: 'member',
    });
  };

  const selectedCallBack = (data: SelectedListItem, type: 'owner' | 'admin' | 'member') => {
    if(!current()||savingRef.current)return;
    if(type==='owner'&&!data.data[0]){setNotice('请选择群主');return;}
    const tmpData = { ...selectItems };
    tmpData[type] = data;
    setSelectItems(tmpData);
    if (type === 'owner') {
      form.setFieldValue('ownerUserID', data.data[0].userID);
    }
    if (type === 'admin') {
      const tmpArr = data.data.map((user) => ({
        roleLevel: 1,
        userID: user.userID,
      }));
      form.setFieldValue(
        'adminUserIDs',
        tmpArr.map((e) => e.userID),
      );
    }
    if (type === 'member') {
      const tmpArr = data.data.map((user) => ({
        roleLevel: 1,
        userID: user.userID,
      }));
      form.setFieldValue(
        'memberUserIDs',
        tmpArr.map((e) => e.userID),
      );
    }
    void 0;
  };

  const onFinish = async (values: AddGroupFormFields) => {
    if(!current()||savingRef.current||uploadingRef.current)return;
    if(opType==='create'&&repeatedGroupSelection(values.ownerUserID,values.adminUserIDs,values.memberUserIDs)){
      setNotice('群主、管理员和群成员不能重复，请分别选择。');return;
    }
    savingRef.current=true;setSaving(true);setNotice('');
    try{
      if(opType==='create')await createGroup(groupCreatePayload(values,uploadUrl) as any);
      else await updateGroup(groupEditPayload(values,groupInfo!,uploadUrl||undefined) as API.GroupManage.UpdateGroupParams);
      if(!current())return;
      message.success('操作成功！');reload();form.resetFields();closeDrawer();
    }catch{if(current())setNotice('保存结果未确认，请刷新群列表核实后再操作；已保留填写内容和上传头像。');}
    finally{savingRef.current=false;if(current())setSaving(false);}
  };

  const customUpload = async (data: UploadRequestOption) => {
    if(!current()||savingRef.current)return;
    uploadController.current?.abort();const controller=new AbortController();uploadController.current=controller;
    const turn=++uploadTurn.current;uploadingRef.current=true;setUploading(true);setNotice('');
    try {
      const asset=await uploadAdminFile(data.file as File,1024*1024,controller.signal);
      if(current()&&turn===uploadTurn.current&&!controller.signal.aborted){setUploadUrl(asset.url);data.onSuccess?.({url:asset.url});}
    } catch {
      if(current()&&turn===uploadTurn.current&&!controller.signal.aborted){setNotice('头像上传未完成，未替换原头像；请重新选择图片。');data.onError?.(new Error('头像上传未完成'));}
    }finally{if(turn===uploadTurn.current){uploadingRef.current=false;if(current())setUploading(false);}}
  };

  if(!sessionValid)return <Alert type="warning" showIcon message="管理员会话已变化，请关闭并重新打开群设置。" action={<Button onClick={closeDrawer}>关闭</Button>}/>;
  return (
    <div>
      <div>
        <div className="w-full text-center mb-12">
          <div className="flex flex-col">
            <Upload accept="image/*" disabled={saving||uploading||!sessionValid} customRequest={customUpload} showUploadList={false}>
              <Avatar shape="square" size={55} src={uploadUrl || groupInfo?.faceURL || avatar_upload} />
              <div className="text-[#1890FFFF] mt-3">点击上传</div>
            </Upload>
          </div>
        </div>
        {notice&&<Alert type="warning" showIcon message={notice} style={{marginBottom:16}}/>}
        <Form
          form={form}
          name="basic"
          labelCol={{ span: 6 }}
          wrapperCol={{ span: 18 }}
          onFinish={onFinish}
          initialValues={opType === 'create' ? { groupType: GroupType.Nomal } : groupInfo}
          autoComplete="off"
          requiredMark={false}
          disabled={saving||uploading||!sessionValid}
        >
          <Form.Item
            label="名称"
            name="groupName"
            rules={[{ required: true, message: '请输入群组名称!' }]}
          >
            <Input placeholder="请输入群组名称" />
          </Form.Item>

          {opType === 'edit' && (
            <Form.Item label="群ID" name="groupID">
              <Input disabled placeholder="请输入群组名称" />
            </Form.Item>
          )}

          {/* <Form.Item
            label="群类型"
            name="groupType"
            rules={[{ required: true, message: '请选择群类型!' }]}
          >
            <Select disabled={opType === 'edit'}>
              <Select.Option value={GroupType.Nomal}>普通群</Select.Option>
              <Select.Option value={GroupType.Working}>工作群</Select.Option>
            </Select>
          </Form.Item> */}

          <Form.Item
            label="群验证"
            name="needVerification"
            rules={[{ required: true, message: '请选择群类型!' }]}
          >
            <Select>
              <Select.Option value={GroupVerificationType.AllNeed}>需要发送验证消息</Select.Option>
              <Select.Option value={GroupVerificationType.AllNot}>允许所有人加群</Select.Option>
              <Select.Option value={GroupVerificationType.ApplyNeedInviteNot}>
                群成员邀请无需验证
              </Select.Option>
            </Select>
          </Form.Item>

          {opType === 'edit' && (
            <>
              <Form.Item label="群公告" name="notification">
                <Input.TextArea
                  autoSize={{ maxRows: 3, minRows: 3 }}
                  maxLength={250}
                  placeholder="请输入群公告"
                />
              </Form.Item>
              <Form.Item label="群简介" name="introduction">
                <Input.TextArea
                  autoSize={{ maxRows: 3, minRows: 3 }}
                  maxLength={250}
                  placeholder="请输入群公告"
                />
              </Form.Item>
              <Form.Item
                label="添加好友"
                name="applyMemberFriend"
                rules={[{ required: true, message: '请选择群权限!' }]}
              >
                <Select>
                  <Select.Option value={AllowType.Allowed}>允许群内添加好友</Select.Option>
                  <Select.Option value={AllowType.NotAllowed}>不允许群内添加好友</Select.Option>
                </Select>
              </Form.Item>
              <Form.Item
                label="查看资料"
                name="lookMemberInfo"
                rules={[{ required: true, message: '请选择查看资料权限!' }]}
              >
                <Select>
                  <Select.Option value={AllowType.Allowed}>允许查看群员资料</Select.Option>
                  <Select.Option value={AllowType.NotAllowed}>不允许查看群员资料</Select.Option>
                </Select>
              </Form.Item>
            </>
          )}

          {opType === 'create' && (
            <>
              <Form.Item
                label="群主"
                name="ownerUserID"
                rules={[{ required: true, message: '请选择群主!' }]}
              >
                <div
                  onClick={() => {
                    setSelectModalOptions({
                      visible: true,
                      selectType: 'owner',
                      preSelectData: selectItems.owner,
                    });
                  }}
                  className="h-8 border border-solid border-[#D9D9D9] rounded cursor-pointer px-1 py-1 truncate"
                  // style={{ border: '1px solid black' }}
                >
                  {selectItems.owner.data.map((item) => (
                    <Tag key={item.userID}>{item.nickname}</Tag>
                  ))}
                </div>
                {/* <Select onClick={() => {}}></Select> */}
              </Form.Item>

              <Form.Item
                label="管理员"
                name="adminUserIDs"
                rules={[{ required: true, message: '请选择管理员!' }]}
              >
                <div
                  onClick={() => {
                    setSelectModalOptions({
                      visible: true,
                      selectType: 'admin',
                      preSelectData: selectItems.admin,
                    });
                  }}
                  className="h-8 border border-solid border-[#D9D9D9] rounded cursor-pointer px-1 py-1 truncate"
                >
                  {selectItems.admin.data.map((item) => (
                    <Tag key={item.userID}>{item.nickname}</Tag>
                  ))}
                </div>
              </Form.Item>

              <Form.Item
                label="群成员"
                name="memberUserIDs"
                rules={[{ required: true, message: '请选择群成员!' }]}
              >
                <div
                  onClick={() => {
                    setSelectModalOptions({
                      visible: true,
                      selectType: 'member',
                      preSelectData: selectItems.member,
                    });
                  }}
                  className="h-8 border border-solid border-[#D9D9D9] rounded cursor-pointer px-1 py-1 truncate"
                >
                  {selectItems.member.data.map((item) => (
                    <Tag key={item.userID}>{item.nickname}</Tag>
                  ))}
                </div>
              </Form.Item>
            </>
          )}

          <div className="w-full text-right">
            <Button className="px-6" type="primary" htmlType="submit" loading={saving} disabled={uploading||!sessionValid}>
              确定
            </Button>
          </div>
        </Form>
      </div>
      <SelectUserModal
        selectModalOptions={selectModalOptions}
        selectedCallBack={selectedCallBack}
        closeSelectModal={closeSelectModal}
      />
    </div>
  );
});

const GroupActionDrawer: FC<GroupActionProps> = (props) => {
  const { drawerOptions, closeDrawer, reload } = props;

  return (
    <Drawer
      title={drawerOptions.opType === 'create' ? '新建群组' : '群组设置'}
      placement="right"
      closable={false}
      onClose={closeDrawer}
      open={drawerOptions.visible}
    >
      {drawerOptions.visible && (
        <OpGroupPage
          key={`${drawerOptions.opType}:${drawerOptions.groupInfo?.groupID||'create'}`}
          opType={drawerOptions.opType}
          reload={reload}
          groupInfo={drawerOptions.groupInfo}
          closeDrawer={closeDrawer}
        />
      )}
    </Drawer>
  );
};

export default GroupActionDrawer;
