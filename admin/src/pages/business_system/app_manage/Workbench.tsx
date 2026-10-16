import type {AppletRecord} from '@/core/appletManagement';
import {deleteApplet,getApplet} from '@/services/server/app_manage';
import {type ActionType,PageContainer,type ProColumns,ProTable} from '@ant-design/pro-components';
import {Button,message,Popconfirm,Space,Tag} from 'antd';
import {useRef,useState} from 'react';
import AppModal from './AppModal';

export default function Workbench(){
  const actionRef=useRef<ActionType>();
  const [editor,setEditor]=useState<{open:boolean;record?:AppletRecord}>({open:false});
  const columns:ProColumns<AppletRecord>[]=[
    {dataIndex:'index',valueType:'indexBorder',width:48},
    {title:'应用名称',dataIndex:'name',align:'center'},
    {title:'图标',dataIndex:'icon',valueType:'avatar',hideInSearch:true,align:'center'},
    {title:'版本',dataIndex:'version',hideInSearch:true,align:'center'},
    {title:'AppID',dataIndex:'appID',hideInSearch:true,align:'center'},
    {title:'排序',dataIndex:'priority',hideInSearch:true,align:'center'},
    {title:'状态',dataIndex:'status',hideInSearch:true,align:'center',render:(_,record)=><Tag color={record.status===1?'green':undefined}>{record.status===1?'上架':'下架'}</Tag>},
    {title:'操作',valueType:'option',width:150,render:(_,record)=><Space>
      <Button type="link" size="small" onClick={()=>setEditor({open:true,record})}>编辑</Button>
      <Popconfirm title="确认移除小程序" description={`${record.name}（${record.appID}）将从工作台列表移除，请确认处理对象。`}
        okText="确认移除" cancelText="取消" okButtonProps={{danger:true}}
        onConfirm={async()=>{await deleteApplet([record.id]);message.success('已移除小程序');actionRef.current?.reload();}}>
        <Button type="link" danger size="small">移除</Button>
      </Popconfirm>
    </Space>},
  ];
  return <PageContainer>
    <ProTable<AppletRecord> columns={columns} actionRef={actionRef} cardBordered rowKey="id" scroll={{x:'max-content'}}
      request={async(params={})=>{const {data}=await getApplet({keyword:params.name??'',pagination:{pageNumber:params.current??1,showNumber:params.pageSize??10}});
        return {data:data.applets??[],success:true,total:data.total??0};}}
      search={{labelWidth:'auto'}} pagination={{defaultPageSize:10,showQuickJumper:true}}
      toolbar={{actions:[<Button key="new" type="primary" onClick={()=>setEditor({open:true})}>新建小程序</Button>],settings:[]}} />
    <AppModal isOpen={editor.open} isCreate={!editor.record} record={editor.record} setIsOpen={open=>setEditor(value=>({...value,open}))}
      onSaved={()=>actionRef.current?.reload()} />
  </PageContainer>;
}
