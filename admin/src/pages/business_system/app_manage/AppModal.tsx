import {appletCreateBody,appletUpdateBody,type AppletDraft,type AppletRecord} from '@/core/appletManagement';
import {uploadAdminFile} from '@/services/admin/upload';
import {crteateApplet,updateApplet} from '@/services/server/app_manage';
import {UploadOutlined} from '@ant-design/icons';
import {Alert,Button,Form,Input,InputNumber,message,Modal,Select,Space,Upload} from 'antd';
import {useEffect,useRef,useState} from 'react';
import type {UploadRequestOption} from 'rc-upload/lib/interface';

type AppModalProps={isOpen:boolean;isCreate:boolean;setIsOpen:(open:boolean)=>void;record?:AppletRecord;onSaved?:()=>void};
const initial:AppletDraft={name:'',appID:'',icon:'',url:'',md5:'',size:0,version:'',priority:0,status:2};
export default function AppModal({isOpen,isCreate,setIsOpen,record,onSaved}:AppModalProps){
  const [form]=Form.useForm<AppletDraft>();
  const [pending,setPending]=useState(false);
  const [uploading,setUploading]=useState(false);
  const generation=useRef(0);
  const uploadController=useRef<AbortController>();
  const uploadBusy=useRef(false);
  const saving=useRef(false);
  const createID=useRef('');
  const [modal,contextHolder]=Modal.useModal();
  useEffect(()=>{
    generation.current++;
    uploadController.current?.abort();uploadBusy.current=false;setUploading(false);
    if(isOpen){createID.current=crypto.randomUUID();form.setFieldsValue({...initial,...record});}
    return()=>{generation.current++;uploadController.current?.abort();};
  },[isOpen,record,form]);
  const cancel=()=>{if(pending)return;generation.current++;uploadController.current?.abort();form.resetFields();setIsOpen(false);};
  const uploadFile=async(kind:'icon'|'package',data:UploadRequestOption)=>{
    if(uploadBusy.current)return;
    const file=data.file as File;
    if(kind==='package'&&!/\.wgt$/i.test(file.name)){message.warning('请选择.wgt安装包');return;}
    const turn=generation.current;
    const controller=new AbortController();uploadController.current=controller;uploadBusy.current=true;setUploading(true);
    try{
      const asset=await uploadAdminFile(file,kind==='icon'?1024*1024:64*1024*1024,controller.signal);
      if(turn!==generation.current||controller.signal.aborted)return;
      form.setFieldsValue(kind==='icon'?{icon:asset.url}:{url:asset.url,md5:asset.md5,size:asset.size});
      data.onSuccess?.({url:asset.url});message.success('文件上传成功，请核对资料后保存');
    }catch{if(turn===generation.current&&!controller.signal.aborted){data.onError?.(new Error('上传未完成'));message.error('上传未完成，资料尚未保存');}}
    finally{if(turn===generation.current){uploadBusy.current=false;setUploading(false);}}
  };
  const submit=async(values:AppletDraft)=>{
    if(saving.current||uploadBusy.current)return;
    let body:ReturnType<typeof appletCreateBody>|ReturnType<typeof appletUpdateBody>;
    try{body=isCreate?appletCreateBody(values,createID.current):appletUpdateBody(values,record!);}
    catch(error){message.error(error instanceof Error?error.message:'应用资料无效');return;}
    if(!isCreate&&Object.keys(body).length===1){cancel();return;}
    saving.current=true;
    const turn=generation.current;
    const perform=async()=>{
      setPending(true);
      try{
        if(isCreate)await crteateApplet(body as ReturnType<typeof appletCreateBody>);else await updateApplet(body);
        if(turn!==generation.current)return;
        message.success('应用资料已保存');onSaved?.();setIsOpen(false);
      }catch{
        if(turn===generation.current)message.warning('提交结果未确认，请刷新列表核实后再操作；已保留填写内容');
      }finally{saving.current=false;if(turn===generation.current)setPending(false);}
    };
    if(values.status===1&&(isCreate||record?.status!==1)){
      modal.confirm({title:'确认上架小程序',content:`${values.name}（${values.appID}）上架后将出现在工作台列表。请核对可信来源和发布版本。`,
        okText:'确认上架',cancelText:'取消',onOk:perform,onCancel:()=>{saving.current=false;}});
    }else await perform();
  };
  return <>{contextHolder}<Modal title={isCreate?'新建小程序':'编辑小程序'} open={isOpen} onCancel={cancel} onOk={()=>form.submit()}
    okText="保存" cancelText="取消" confirmLoading={pending} okButtonProps={{disabled:uploading}}
    cancelButtonProps={{disabled:pending}} closable={!pending} maskClosable={!pending} keyboard={!pending} destroyOnClose
    width={640} centered style={{maxWidth:'calc(100vw - 32px)'}} styles={{body:{maxHeight:'calc(100dvh - 200px)',overflowY:'auto'}}}>
    <Alert type="info" showIcon message="请使用可信来源的.wgt安装包，并核对AppID和版本。新应用默认下架，保存后可单独上架。" style={{marginBottom:16}} />
    <Form form={form} layout="vertical" onFinish={submit} disabled={pending||uploading} preserve={false}>
      <Form.Item label="AppID" name="appID" rules={[{required:true,whitespace:true,message:'请输入AppID'}]}><Input maxLength={255} /></Form.Item>
      <Form.Item label="应用名称" name="name" rules={[{required:true,whitespace:true,message:'请输入应用名称'}]}><Input maxLength={64} /></Form.Item>
      <Form.Item label="版本" name="version" rules={[{required:true,whitespace:true,message:'请输入版本'}]}><Input maxLength={64} placeholder="例如1.0.0" /></Form.Item>
      <Form.Item label="应用图标HTTPS地址" name="icon"><Input maxLength={255} /></Form.Item>
      <Space style={{marginBottom:20}}><Upload accept="image/*" showUploadList={false} customRequest={data=>void uploadFile('icon',data)} disabled={pending||uploading}>
        <Button icon={<UploadOutlined />} loading={uploading} disabled={pending}>上传图标（最多1MiB）</Button>
      </Upload></Space>
      <Form.Item label="安装包HTTPS地址" name="url" rules={[{required:true,message:'请上传安装包或填写可信HTTPS地址'}]}><Input maxLength={255} /></Form.Item>
      <Space style={{marginBottom:20}}><Upload accept=".wgt" showUploadList={false} customRequest={data=>void uploadFile('package',data)} disabled={pending||uploading}>
        <Button icon={<UploadOutlined />} loading={uploading} disabled={pending}>上传安装包（最多64MiB）</Button>
      </Upload></Space>
      <Form.Item label="安装包MD5" name="md5" rules={[{required:true,pattern:/^[0-9a-fA-F]{32}$/,message:'请输入32位MD5'}]}><Input maxLength={32} /></Form.Item>
      <Form.Item label="安装包字节数" name="size" rules={[{required:true,message:'请输入安装包大小'}]}><InputNumber min={1} precision={0} style={{width:'100%'}} /></Form.Item>
      <Form.Item label="排序" name="priority"><InputNumber min={0} max={4294967295} precision={0} style={{width:'100%'}} /></Form.Item>
      <Form.Item label="状态" name="status" rules={[{required:true}]}><Select options={[{label:'下架',value:2},{label:'上架',value:1}]} /></Form.Item>
    </Form>
  </Modal></>;
}
