import { useEffect, useRef, useState } from 'react';
import { Alert, Avatar, Button, DatePicker, Form, Input, Popconfirm, Select, Space, Upload } from 'antd';
import { adminSessions } from '@/core/adminSession';
import { checkedProvisioningStatus, creationStatus, ProvisioningJournal, provisioningMessage, provisioningTerminal } from '@/core/adminProvisioning';
import type { ProvisioningStatus } from '@/core/adminProvisioning';
import { adminRegistrationBody } from '@/core/userManagement';
import { getAdminProvisioningStatus, registerUser, resolveAdminProvisioning } from '@/services/server/user_manage';
import { handleFileUpload } from '@/services/admin/upload';
import { countryCode } from '@/utils/areaCode';
import avatarUpload from '@/assets/images/avatar_upload.png';
import type { UploadRequestOption } from 'rc-upload/lib/interface';
import styles from './style.less';

type Fields={nickname:string;gender?:number;birth?:{valueOf():number};phoneNumber:string;password:string;areaCode:string};
export default function AdminUserCreation({reload,onBusyChange=()=>{}}:{reload:()=>void;onBusyChange?:(busy:boolean)=>void}) {
  const [form]=Form.useForm<Fields>();
  const [journalState]=useState(()=>{
    try {
      const session=adminSessions?.current;
      if(!session) throw new Error('管理员会话不可用，请重新登录');
      const journal=new ProvisioningJournal(localStorage,session.issuer,session.account,()=>crypto.randomUUID());
      return {journal,initial:journal.read(),ownerGeneration:adminSessions!.generation};
    } catch { return {error:'无法读取开户恢复记录，请保留当前资料并重新登录后检查。'}; }
  });
  const [records,setRecords]=useState(journalState.initial?.requestIDs??[]);
  const [result,setResult]=useState<{id:string;status:ProvisioningStatus}|undefined>(
    journalState.initial?.activeRequestID ? {id:journalState.initial.activeRequestID,status:{found:false,state:'unknown'}} : undefined);
  const [error,setError]=useState(journalState.error??'');
  const [busy,setBusy]=useState(false);
  const [uploadUrl,setUploadUrl]=useState('');
  const inFlight=useRef(false),generation=useRef(0);
  const formWasMounted=useRef(!journalState.initial?.activeRequestID);
  useEffect(()=>()=>{generation.current++;},[]);
  useEffect(()=>{if(!result){formWasMounted.current=true;form.resetFields();}},[!!result,form]);
  const alive=(expected:number)=>expected===generation.current && adminSessions?.generation===journalState.ownerGeneration;
  const enter=()=>{
    if(inFlight.current || !journalState.journal) return undefined;
    if(adminSessions?.generation!==journalState.ownerGeneration){setError('管理员会话已变更，请关闭后重新打开开户页。');return undefined;}
    inFlight.current=true;setBusy(true);onBusyChange(true);setError('');return generation.current;
  };
  const leave=(expected:number)=>{
    if(expected!==generation.current)return;
    inFlight.current=false;setBusy(false);onBusyChange(false);
  };
  const accept=async(id:string,status:ProvisioningStatus,expected:number)=>{
    if(!alive(expected))return;
    const normalized=checkedProvisioningStatus(status,id);
    setResult({id,status:normalized});
    if(normalized.created || normalized.definitiveNotCreated){
      if(formWasMounted.current)form.resetFields();setUploadUrl('');
      try {
        await journalState.journal!.settle(id,provisioningTerminal(normalized));
        if(!alive(expected))return;
        setRecords(journalState.journal!.read().requestIDs);
      }catch{if(alive(expected))setError('账号结果已确认，但本机恢复编号未能更新。请勿开始新的开户。');}
      if(alive(expected))reload();
    }
  };
  const query=async(id:string)=>{
    const expected=enter();if(expected===undefined)return;
    try {
      const response=await getAdminProvisioningStatus(id);
      if(alive(expected)) await accept(id,response.data,expected);
    }catch{
      if(alive(expected)){setResult({id,status:{found:false,state:'unknown'}});setError('暂时无法确认开户结果，请保留请求编号后继续查询。');}
    }finally{leave(expected);}
  };
  const submit=async(values:Fields)=>{
    const expected=enter();if(expected===undefined)return;
    let id:string|undefined;
    try {
      // Durable random ID must succeed before the first request is allowed.
      id=await journalState.journal!.reserve();
      if(expected!==generation.current || adminSessions?.generation!==journalState.ownerGeneration)return;
      setRecords(journalState.journal!.read().requestIDs);
      setResult({id,status:{found:false,state:'reserved'}});
      const response=await registerUser({...adminRegistrationBody({...values,birth:values.birth?.valueOf()},uploadUrl),adminRequestID:id});
      if(alive(expected))await accept(id,creationStatus(response.data,id),expected);
    }catch{
      if(alive(expected)){
        if(id)setResult({id,status:{found:false,state:'unknown'}});
        setError(id?'请求结果未确认。请查询同一请求编号，不要重新开户。':'无法保存开户请求编号，尚未发送开户请求。');
      }
    }finally{leave(expected);}
  };
  const stop=async(id:string)=>{
    const expected=enter();if(expected===undefined)return;
    try {
      const response=await resolveAdminProvisioning(id);
      if(alive(expected))await accept(id,response.data,expected);
    }catch{
      if(alive(expected))setError('服务端未确认关闭此意图，请保留请求编号后继续查询。');
    }finally{leave(expected);}
  };
  const upload=async(data:UploadRequestOption)=>{
    const expected=enter();if(expected===undefined)return;
    try {const url=await handleFileUpload(data);if(alive(expected)&&url)setUploadUrl(url);}
    catch {if(alive(expected))setError('头像上传未完成，请重试或不使用头像。');}
    finally{leave(expected);}
  };
  const canNew=result && (result.status.created || result.status.definitiveNotCreated) && !error;
  return <div>
    {error && <Alert type="error" showIcon message={error} className="mb-4"/>}
    {!!records.length && <div className="mb-4" aria-label="待确认开户记录">
      <p>恢复记录仅保存随机请求编号，不保存用户资料或口令。</p>
      <Space wrap>{records.map(id=><Button key={id} disabled={busy} onClick={()=>query(id)}>查询 {id.slice(0,8)}</Button>)}</Space>
    </div>}
    {result && <Alert className="mb-4" showIcon type={result.status.created?'info':'warning'}
      message={provisioningMessage(result.status)} description={<div>
        <p style={{overflowWrap:'anywhere'}}>请求编号：{result.id}</p>
        {result.status.userID && <p>用户ID：{result.status.userID}</p>}
        {result.status.receiptID && <p style={{overflowWrap:'anywhere'}}>路由回执：{result.status.receiptID}</p>}
        {result.status.state==='conflict' && <p>身份归属存在冲突，请联系超级管理员核实，勿重复创建账号。</p>}
        <Space wrap>
          <Button disabled={busy} loading={busy} onClick={()=>query(result.id)}>查询开户结果</Button>
          {!result.status.created && !result.status.definitiveNotCreated && <Popconfirm
            title="停止此开户意图？"
            description={<div style={{maxWidth:280,overflowWrap:'anywhere'}}>请求编号：{result.id}<br/>已创建账号会保留；确认尚未创建后，服务端才会关闭此意图并拒绝迟到开户。</div>}
            okText="确认停止" cancelText="继续保留" disabled={busy} okButtonProps={{disabled:busy}} onConfirm={()=>stop(result.id)}>
            <Button disabled={busy}>停止此意图并重新填写</Button>
          </Popconfirm>}
          {canNew && <Button disabled={busy} onClick={()=>{setResult(undefined);setUploadUrl('');}}>继续添加用户</Button>}
        </Space>
      </div>}/>} 
    {!result && <>
      <div className="w-full text-center mb-8"><Upload accept="image/*" customRequest={upload} disabled={busy||!journalState.journal} showUploadList={false}>
        <Avatar shape="square" size={55} src={uploadUrl||avatarUpload}/><div className="text-[#1890FFFF] mt-3">点击上传</div>
      </Upload></div>
      <Form<Fields> form={form} layout="vertical" onFinish={submit} autoComplete="off" initialValues={{areaCode:'86'}} disabled={busy||!journalState.journal}>
        <Form.Item label="昵称" name="nickname" rules={[{required:true,whitespace:true,message:'请输入用户昵称'}]}><Input maxLength={64} placeholder="请输入用户昵称"/></Form.Item>
        <Form.Item label="性别" name="gender"><Select options={[{value:1,label:'男'},{value:2,label:'女'}]}/></Form.Item>
        <Form.Item label="生日" name="birth"><DatePicker/></Form.Item>
        <Form.Item className={styles.no_mb} label="手机号"><Space.Compact>
          <Form.Item name="areaCode"><Select showSearch className="!w-24" options={countryCode.map(country=>({value:country.phone_code,label:`+${country.phone_code}`}))}/></Form.Item>
          <Form.Item name="phoneNumber" rules={[{required:true,message:'请输入用户手机号'}]}><Input placeholder="请输入用户手机号"/></Form.Item>
        </Space.Compact></Form.Item>
        <Form.Item label="密码" name="password" rules={[{required:true,message:'请输入用户密码'}]}><Input.Password autoComplete="new-password" placeholder="请输入用户密码"/></Form.Item>
        <Button type="primary" htmlType="submit" disabled={busy||!journalState.journal} loading={busy}>添加</Button>
      </Form>
    </>}
  </div>;
}
