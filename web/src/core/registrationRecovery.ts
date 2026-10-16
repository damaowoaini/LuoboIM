export type RegistrationFlags = {registrationPending?:true;registrationRecovered?:true;requiresLogin?:true};
// Only these booleans survive an API error; arbitrary response objects may
// contain credentials and must not be attached to Error or persisted.
export function registrationFlags(value:unknown):RegistrationFlags {
  if(!value || typeof value!=="object" || Array.isArray(value))return {};
  const input=value as Record<string,unknown>,result:RegistrationFlags={};
  if(input.registrationPending===true&&input.registrationRecovered===true)return {};
  if(input.registrationPending===true)result.registrationPending=true;
  if(input.registrationRecovered===true)result.registrationRecovered=true;
  if(input.requiresLogin===true)result.requiresLogin=true;
  return result;
}
export type RegistrationDraft={loginMethod:"phone"|"email";email?:string;phoneNumber?:string;areaCode:string;invitationCode:string;nickname:string;password:string;verifyCode:string};
type Owner={issuer:string;generation:number};
export type RecoveryPhase="idle"|"submitting"|"unknown"|"pending"|"recovered"|"checking-login"|"complete";
export type RecoveryView={phase:RecoveryPhase;identity?:string;loginMethod?:"phone"|"email";notice:string};

/** One page lifetime, memory only. Returning to login preserves the freeze;
 * leaving the login page clears the original password and OTP references. */
export class RegistrationRecovery {
  #draft?:Readonly<RegistrationDraft>;
  #owner?:Readonly<Owner>;
  #ticket=0;
  #view:RecoveryView={phase:"idle",notice:""};
  #listeners=new Set<()=>void>();
  getSnapshot=()=>this.#view;
  subscribe=(listener:()=>void)=>{this.#listeners.add(listener);return ()=>{this.#listeners.delete(listener);};};
  #publish(phase:RecoveryPhase,notice:string){
    const draft=this.#draft;
    this.#view=Object.freeze({phase,notice,identity:draft?.loginMethod==="email"?draft.email:draft?`${draft.areaCode} ${draft.phoneNumber}`:undefined,loginMethod:draft?.loginMethod});
    for(const listener of this.#listeners)listener();
  }
  hasAttempt(){return this.#draft!==undefined;}
  currentTicket(){return this.#draft?this.#ticket:undefined;}
  isCurrent(ticket:number|undefined){return this.#draft!==undefined && ticket===this.#ticket;}
  begin(draft:RegistrationDraft,owner:Owner){
    if(this.#draft)throw new Error("注册结果尚未确认，请使用原账号密码登录，勿重新注册");
    this.#draft=Object.freeze({...draft});this.#owner=Object.freeze({...owner});this.#ticket++;
    this.#publish("submitting","正在提交注册，请勿关闭或重复提交");return this.#ticket;
  }
  registration(digest:(password:string)=>string){
    if(!this.#draft)throw new Error("没有待确认的注册");
    const d=this.#draft;
    return {verifyCode:d.verifyCode,invitationCode:d.invitationCode,autoLogin:true,user:{nickname:d.nickname,faceURL:"",areaCode:d.areaCode,password:digest(d.password),...(d.loginMethod==="email"?{email:d.email}:{phoneNumber:d.phoneNumber})}};
  }
  failed(ticket:number,error:unknown){
    if(!this.isCurrent(ticket))return;
    const flags=registrationFlags(error && typeof error==="object" ? (error as {registration?:unknown}).registration : undefined);
    if(flags.registrationRecovered)this.#publish("recovered","账号注册和登录路由已恢复，请使用注册时的原账号密码登录");
    else if(flags.registrationPending)this.#publish("pending","账号开通仍在确认，请稍后使用原账号密码登录；请勿重新获取验证码或再次注册");
    else this.#publish("unknown","注册结果未确认，账号可能已创建。请使用原账号密码登录，勿重新获取验证码或再次注册");
  }
  beginLogin(issuer:string,generation:number,googleAuthCode:string,digest:(password:string)=>string){
    if(!this.#draft || !this.#owner || ["submitting","checking-login"].includes(this.#view.phase))throw new Error("请等待当前请求完成");
    if(this.#owner.issuer!==issuer || this.#owner.generation!==generation)throw new Error("服务地址或登录会话已变化，未提交原注册凭据。请恢复原服务设置后再登录");
    const d=this.#draft;
    this.#publish("checking-login","正在用原账号密码确认登录，未重新提交注册");
    return {password:digest(d.password),areaCode:d.areaCode,googleAuthCode,...(d.loginMethod==="email"?{email:d.email}:{phoneNumber:d.phoneNumber})};
  }
  loginFailed(ticket:number,error:unknown){
    this.failed(ticket,error);
    if(this.isCurrent(ticket) && this.#view.phase==="unknown")this.#publish("unknown","暂时未能用原账号密码登录，注册状态仍未确认。请稍后再次尝试登录或联系管理员，勿重新注册");
  }
  complete(ticket:number){if(!this.isCurrent(ticket))return;this.#draft=undefined;this.#owner=undefined;this.#ticket++;this.#publish("complete","已完成登录");}
  dispose(){this.#draft=undefined;this.#owner=undefined;this.#ticket++;this.#publish("idle","");}
}
