import { generateInvitationCode, getInvitationCode } from '@/services/server/register_manage';
import { copy2Text } from '@/utils/common';
import type { ActionType, ProColumns } from '@ant-design/pro-components';
import { PageContainer, ProTable } from '@ant-design/pro-components';
import { Alert, Button, InputNumber, message, Modal } from 'antd';
import { useEffect, useRef, useState } from 'react';
import { adminSessions } from '@/core/adminSession';
import { collectInvitationExport, invitationCSV, invitationFilter, invitationRow } from '@/core/invitationExport';
import type { InvitationQuery } from '@/core/invitationExport';
import type { InvitationCodeItem } from './data';

const columns: ProColumns<InvitationCodeItem>[] = [
  {
    key: 'index',
    title: '序号',
    dataIndex: 'index',
    valueType: 'indexBorder',
    width: 48,
    align: 'center',
  },
  {
    title: '邀请码',
    dataIndex: 'invitationCode',
    key: 'invitationCode',
    align: 'center',
  },
  {
    title: '使用人昵称',
    key: 'usedUserName',
    dataIndex: 'usedUserName',
    valueType: 'select',
    hideInSearch: true,
    align: 'center',
  },
  {
    title: '使用状态',
    key: 'status',
    dataIndex: 'status',
    valueType: 'select',
    initialValue: 0,
    valueEnum: {
      2: '未使用',
      1: '已使用',
      0: '全部',
    },
  },
  {
    title: '使用人手机号',
    key: 'usedUserPhoneNumber',
    dataIndex: 'usedUserPhoneNumber',
    valueType: 'select',
    hideInSearch: true,
    align: 'center',
  },
  {
    title: '操作',
    valueType: 'option',
    key: 'option',
    render: (_text, record) => {
      return [
        <a href="#" key="copy" onClick={() => copy2Text(record.invitationCode)}>
          复制
        </a>,
      ];
    },
  },
];

const InvitionCode = () => {
  const actionRef = useRef<ActionType>();
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [genNumber, setGenNumber] = useState(10);
  const [pageSize, setPageSize] = useState(10);
  const [generating,setGenerating]=useState(false);
  const generatingRef=useRef(false);
  const [generationUnknown,setGenerationUnknown]=useState(false);
  const [exporting,setExporting]=useState(false);
  const exportController=useRef<AbortController>();
  const filterRef=useRef<Pick<InvitationQuery,'status'|'keyword'>>({status:0,keyword:''});
  const mounted=useRef(true);
  useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;exportController.current?.abort();};},[]);

  const showModal = () => {
    setGenerationUnknown(false);
    setIsModalOpen(true);
  };

  const handleOk = async () => {
    if(generatingRef.current || generationUnknown)return;
    if(!Number.isInteger(genNumber)||genNumber<1||genNumber>2000){message.warning('请输入1至2000的整数');return;}
    generatingRef.current=true;setGenerating(true);
    try {
      await generateInvitationCode(genNumber);
      if(!mounted.current)return;
      message.success('生成成功');setIsModalOpen(false);actionRef.current?.reload();
    }catch {
      if(mounted.current)setGenerationUnknown(true);
    }finally{generatingRef.current=false;if(mounted.current)setGenerating(false);}
  };
  const fetchAllDataForExport = async () => {
    if(exportController.current)return;
    const session=adminSessions?.current;if(!session){message.error('请先登录');return;}
    const controller=new AbortController();exportController.current=controller;setExporting(true);
    const lease=adminSessions!.lease(),key='invitation-export-'+crypto.randomUUID();
    try {
      const rows=await collectInvitationExport({
        filter:{...filterRef.current},signal:controller.signal,assertCurrent:lease.assertCurrent,
        fetch:async query=>(await getInvitationCode(query,controller.signal)).data,
        onProgress:(loaded,total)=>message.loading({key,content:`正在导出 ${loaded}/${total} 条`,duration:0}),
      });
      lease.assertCurrent();controller.signal.throwIfAborted();if(!mounted.current)return;
      if(!rows.length){message.info('当前筛选没有数据可导出');return;}
      const url=URL.createObjectURL(new Blob([invitationCSV(rows)],{type:'text/csv;charset=utf-8;'}));
      try {
        const link=document.createElement('a');link.href=url;link.download=`邀请码筛选结果_${Date.now()}.csv`;
        document.body.appendChild(link);link.click();link.remove();
      }finally{setTimeout(()=>URL.revokeObjectURL(url),0);}
      message.success(`已导出${rows.length}条邀请码`);
    }catch(error){
      if(!mounted.current)return;
      try{lease.assertCurrent();}catch{return;}
      if(controller.signal.aborted)message.info('导出已取消');
      else message.error(error instanceof Error && /^(导出|筛选结果超过)/.test(error.message)?error.message:'导出未完成，请重新查询后重试');
    }finally{
      lease.release();message.destroy(key);exportController.current=undefined;if(mounted.current)setExporting(false);
    }
  };

  return (
    <PageContainer>
      <ProTable<InvitationCodeItem>
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
        request={async (params = {}) => {
          const filter=invitationFilter(params.status,params.invitationCode);filterRef.current=filter;
          const {data}=await getInvitationCode({...filter,pagination:{pageNumber:params.current??1,showNumber:params.pageSize??10}});
          return {data:(data.list??[]).map(invitationRow),success:true,total:data.total??0};
        }}
        rowKey="invitationCode"
        search={{
          labelWidth: 'auto',
        }}
        pagination={{
          pageSize: pageSize,
          showSizeChanger: true,
          pageSizeOptions: ['10', '20', '50', '100', '200', '500', '1000', '2000'],
          onShowSizeChange: (_current, size) => {
            setPageSize(size);
          },
        }}
        dateFormatter="string"
        scroll={{ x: 'max-content' }}
        toolbar={{
          actions: [
            <Button key="create" type="primary" onClick={showModal}>
              生成邀请码
            </Button>,
            <Button key="export" disabled={exporting} loading={exporting} onClick={fetchAllDataForExport}>
              导出当前筛选
            </Button>,
            ...(exporting?[<Button key="cancel-export" onClick={()=>exportController.current?.abort()}>取消导出</Button>]:[]),
          ],
          settings: [],
        }}
      />
      <Modal title="请选择生成数量" open={isModalOpen} onOk={handleOk} confirmLoading={generating} okButtonProps={{disabled:generationUnknown}} closable={!generating} maskClosable={!generating} keyboard={!generating} onCancel={()=>{if(!generating)setIsModalOpen(false);}}>
        {generationUnknown && <Alert className="mb-4" type="warning" showIcon message="生成结果未确认，请先查询列表，勿连续提交。"/>}
        <div className="w-full flex justify-center">
          <InputNumber
            min={1}
            max={2000}
            value={genNumber}
            onChange={(value) => {
              setGenNumber(value as number);
            }}
          />
        </div>
      </Modal>
    </PageContainer>
  );
};

export default InvitionCode;
