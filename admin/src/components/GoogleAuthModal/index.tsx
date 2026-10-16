import {bindGoogleAuth,getGoogleAuthQRCode,getUserGoogleAuthStatus,resetGoogleAuth} from '@/services/server/google_auth';
import {Alert,Button,Form,Input,message,Modal,QRCode,Space,Spin,Typography} from 'antd';
import {useEffect,useRef,useState} from 'react';

interface GoogleAuthModalProps {visible:boolean;userID:string;onCancel:()=>void;onSuccess:()=>void;}
export default function GoogleAuthModal({visible,userID,onCancel,onSuccess}:GoogleAuthModalProps){
  const [form]=Form.useForm();
  const [qrData,setQrData]=useState<{owner:string;secret:string;qrCodeURL:string}|null>(null);
  const [status,setStatus]=useState<{owner:string;enabled:boolean}|null>(null);
  const [loading,setLoading]=useState(false);
  const [pending,setPending]=useState(false);
  const [failed,setFailed]=useState(false);
  const generation=useRef(0);
  const inFlight=useRef(false);
  const confirmation=useRef<{destroy:()=>void}>();
  const [modal,contextHolder]=Modal.useModal();
  const latestTarget=useRef({visible,userID});latestTarget.current={visible,userID};
  const current=(turn:number,target:string)=>turn===generation.current&&latestTarget.current.visible&&latestTarget.current.userID===target;
  const visibleStatus=visible&&status?.owner===userID?status:null;
  const visibleQR=visible&&qrData?.owner===userID?qrData:null;
  const loadStatus=async(turn:number,target:string)=>{
    setLoading(true);setFailed(false);
    try{
      const response=await getUserGoogleAuthStatus(target);
      if(!current(turn,target))return;
      // protobuf false is omitted by the existing encoding/json envelope.
      if(!response.data||response.data.enabled!==undefined&&typeof response.data.enabled!=='boolean')throw new Error('invalid status');
      setStatus({owner:target,enabled:response.data.enabled===true});
    }catch{if(current(turn,target)){setStatus(null);setFailed(true);}}
    finally{if(current(turn,target))setLoading(false);}
  };
  useEffect(()=>{
    const turn=++generation.current;
    confirmation.current?.destroy();inFlight.current=false;
    setQrData(null);setStatus(null);setPending(false);setFailed(false);form.resetFields();
    if(visible&&userID)void loadStatus(turn,userID);
    return()=>{generation.current++;confirmation.current?.destroy();};
  },[visible,userID,form]);
  const cancel=()=>{if(pending)return;generation.current++;confirmation.current?.destroy();setQrData(null);setStatus(null);form.resetFields();onCancel();};
  const generate=async()=>{
    if(inFlight.current||!visibleStatus||visibleStatus.enabled)return;
    const turn=generation.current,target=userID;inFlight.current=true;setPending(true);setQrData(null);
    try{
      const response=await getGoogleAuthQRCode(target);
      if(!current(turn,target))return;
      const url=new URL(response.data.qrCodeURL),secret=url.searchParams.get('secret')||'';
      if(url.protocol!=='otpauth:'||!secret)throw new Error('invalid enrollment');
      setQrData({owner:target,qrCodeURL:url.toString(),secret});
    }catch{if(current(turn,target))message.error('生成绑定信息失败，请重新确认用户状态后重试');}
    finally{if(current(turn,target)){inFlight.current=false;setPending(false);}}
  };
  const bind=async(values:{code:string})=>{
    if(!visibleQR||inFlight.current)return;
    const turn=generation.current,target=userID;inFlight.current=true;setPending(true);
    try{
      await bindGoogleAuth({userID:target,secret:visibleQR.secret,code:values.code});
      if(current(turn,target)){message.success('绑定成功');form.resetFields();setQrData(null);onSuccess();}
    }catch{if(current(turn,target))message.error('绑定未完成，请核对验证码；已保留输入');}
    finally{if(current(turn,target)){inFlight.current=false;setPending(false);}}
  };
  const reset=(regenerate:boolean)=>{
    const turn=generation.current,target=userID;
    confirmation.current=modal.confirm({title:regenerate?'确认重新绑定':'确认清除绑定',
      content:<><p>用户ID：{target}</p><p>此操作将清除该用户当前的身份验证器，用户需要重新扫码绑定。请先核实账号归属。</p></>,
      okText:'确认清除',cancelText:'取消',okButtonProps:{danger:true},
      onOk:async()=>{
        if(!current(turn,target)||inFlight.current)return;
        inFlight.current=true;setPending(true);
        try{
          await resetGoogleAuth(target);
          if(!current(turn,target))return;
          setQrData(null);form.resetFields();
          if(regenerate){setStatus({owner:target,enabled:false});message.success('已清除，请点击生成新二维码完成重新绑定');}
          else{message.success('已清除绑定');onSuccess();}
        }catch{if(current(turn,target))message.error('清除绑定未确认，请先重新查询用户状态');throw new Error('reset unconfirmed');}
        finally{if(current(turn,target)){inFlight.current=false;setPending(false);}}
      },
    });
  };
  return <>{contextHolder}<Modal title={`身份验证器管理 · ${userID}`} open={visible} onCancel={cancel} footer={null}
    width={600} centered style={{maxWidth:'calc(100vw - 32px)'}} styles={{body:{maxHeight:'calc(100dvh - 160px)',overflowY:'auto'}}}
    closable={!pending} maskClosable={!pending} keyboard={!pending}>
    {loading?<Spin tip="正在查询验证器状态"/>:failed?<Alert type="error" showIcon message="验证器状态暂不可用" description="请重试查询，未确认状态前不能绑定或清除。"
      action={<Button onClick={()=>void loadStatus(generation.current,userID)}>重试查询</Button>}/>:visibleStatus?.enabled?
      <Space direction="vertical" style={{width:'100%'}} size="large">
        <Alert message="该用户已绑定身份验证器" type="success" showIcon/>
        <Space wrap><Button danger disabled={pending} onClick={()=>reset(false)}>清除绑定</Button><Button disabled={pending} onClick={()=>reset(true)}>重新绑定</Button></Space>
      </Space>:visibleStatus?<Space direction="vertical" style={{width:'100%'}} size="large">
        <Alert type="info" showIcon message="该用户尚未绑定身份验证器" description="生成二维码后请交由账号本人扫码，再输入其6位动态验证码完成绑定。重新生成会使此前尚未绑定的二维码失效。"/>
        {!visibleQR?<Button loading={pending} onClick={()=>void generate()}>生成绑定二维码</Button>:<>
          <div style={{textAlign:'center'}}><QRCode value={visibleQR.qrCodeURL} size={200}/>
            <Typography.Paragraph copyable style={{marginTop:16,fontSize:12,overflowWrap:'anywhere'}}>手动绑定密钥：{visibleQR.secret}</Typography.Paragraph></div>
          <Form form={form} onFinish={bind} layout="vertical" disabled={pending}>
            <Form.Item label="动态验证码" name="code" rules={[{required:true,message:'请输入6位动态验证码'},{pattern:/^\d{6}$/,message:'验证码须为6位数字'}]}>
              <Input autoComplete="one-time-code" inputMode="numeric" maxLength={6}/>
            </Form.Item>
            <Space wrap><Button onClick={cancel} disabled={pending}>取消</Button><Button type="primary" htmlType="submit" loading={pending}>确认绑定</Button></Space>
          </Form>
        </>}
      </Space>:null}
    {pending&&<Alert style={{marginTop:16}} type="info" message="操作正在提交，请稍候；暂不能关闭或重复提交。"/>}
  </Modal></>;
}
