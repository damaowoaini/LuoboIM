export type ProvisioningState = 'reserved' | 'unknown' | 'awaiting_im' | 'pending' | 'bound' | 'direct_only' | 'conflict' | 'rejected';
export interface AdminCreationResult {
  userID?: string;
  adminRequestID?: string;
  provisioningReceiptID?: string;
  provisioningState?: ProvisioningState;
  provisioningPending?: boolean;
  recovered?: boolean;
}
export interface ProvisioningStatus {
  found: boolean;
  adminRequestID?: string;
  receiptID?: string;
  userID?: string;
  state?: ProvisioningState;
  created?: boolean;
  definitiveNotCreated?: boolean;
  retryable?: boolean;
  reason?: string;
}
const UUID4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
type Journal = { version: 1; requestIDs: string[]; activeRequestID?: string };

/** Only random request IDs are persisted. No identity, form, password, OTP or token. */
export class ProvisioningJournal {
  readonly key: string;
  constructor(private storage: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>,
    issuer: string, account: string, private uuid: () => string,
    private exclusive: <T>(key:string,operation:()=>T)=>Promise<T> = async(key,operation)=>{
      if(!navigator.locks) throw new Error('当前浏览器不支持安全开户恢复，请使用受支持浏览器');
      return navigator.locks.request(key,operation);
    }) {
    if (!issuer || !account) throw new Error('管理员会话不可用');
    this.key = `luobo.admin.provisioning.v1:${encodeURIComponent(issuer)}:${encodeURIComponent(account)}`;
  }
  read(): Journal {
    const raw = this.storage.getItem(this.key);
    if (!raw) return {version:1,requestIDs:[]};
    const value = JSON.parse(raw);
    if (value?.version !== 1 || !Array.isArray(value.requestIDs) || value.requestIDs.length > 100 ||
      !value.requestIDs.every((id: unknown) => typeof id === 'string' && UUID4.test(id)) ||
      new Set(value.requestIDs).size !== value.requestIDs.length ||
      (value.activeRequestID !== undefined && !value.requestIDs.includes(value.activeRequestID)))
      throw new Error('开户恢复记录损坏，请保留记录并联系管理员');
    // Discard any unrecognized fields instead of carrying them into storage writes.
    return {version:1,requestIDs:[...value.requestIDs],...(value.activeRequestID ? {activeRequestID:value.activeRequestID} : {})};
  }
  private write(value: Journal) {
    if (!value.requestIDs.length) this.storage.removeItem(this.key);
    else this.storage.setItem(this.key,JSON.stringify(value));
  }
  async reserve(): Promise<string> { return this.exclusive(this.key,()=>{
    const value=this.read();
    if(value.activeRequestID) return value.activeRequestID;
    if(value.requestIDs.length>=100) throw new Error('待确认开户记录过多，请先查询处理');
    const id=this.uuid();if(!UUID4.test(id)) throw new Error('无法生成开户请求编号');
    this.write({version:1,requestIDs:[...value.requestIDs,id],activeRequestID:id});
    return id;
  }); }
  async settle(id: string, terminal: boolean) { return this.exclusive(this.key,()=>{
    const value=this.read();
    this.write({version:1,requestIDs:terminal ? value.requestIDs.filter(item=>item!==id) : value.requestIDs,
      ...(value.activeRequestID && value.activeRequestID!==id ? {activeRequestID:value.activeRequestID} : {})});
  }); }
}

export function creationStatus(data: AdminCreationResult, requestID: string): ProvisioningStatus {
  if (data.adminRequestID && data.adminRequestID !== requestID) throw new Error('开户响应编号不一致，请查询结果');
  if (!data.userID) throw new Error('开户结果缺少用户ID，请查询结果');
  if(data.provisioningPending && ['bound','direct_only'].includes(data.provisioningState??''))
    throw new Error('开户路由状态不一致，请查询结果');
  return {found:true,adminRequestID:requestID,receiptID:data.provisioningReceiptID,userID:data.userID,
    state:data.provisioningState ?? 'unknown',created:true,definitiveNotCreated:false};
}
export function checkedProvisioningStatus(status:ProvisioningStatus, requestID:string):ProvisioningStatus {
  if(typeof status?.found!=='boolean' || status.adminRequestID && status.adminRequestID!==requestID)
    throw new Error('开户查询响应编号不一致');
  if(status.created && (!status.found || !status.userID) ||
    status.definitiveNotCreated && (!status.found || status.state!=='rejected' || status.created) ||
    ['bound','direct_only'].includes(status.state??'') && !status.created)
    throw new Error('开户查询状态不一致');
  return {...status,definitiveNotCreated:status.state==='rejected' && status.definitiveNotCreated===true};
}
export function provisioningTerminal(status: ProvisioningStatus): boolean {
  return !!status.created && ['bound','direct_only'].includes(status.state ?? '') ||
    status.state === 'rejected' && status.definitiveNotCreated === true;
}
export function provisioningMessage(status: ProvisioningStatus): string {
  if(status.created && status.state==='bound') return '账号已创建，登录路由已确认';
  if(status.created && status.state==='direct_only') return '账号已创建，当前部署仅支持租户直连';
  if(status.created) return '账号已创建，登录路由待确认';
  if(status.state==='rejected' && status.definitiveNotCreated) return '服务端已确认未创建账号，可以开始新的开户';
  return '尚未取得权威开户结果，请保留请求编号并继续查询，勿重新开户';
}
