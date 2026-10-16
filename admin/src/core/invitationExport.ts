export interface InvitationWire {
  invitationCode:string;createTime?:number|string;usedUserID?:string;
  usedUser?:{userID?:string;nickname?:string};
  usedUserName?:string;usedUserPhoneNumber?:string;
}
export interface InvitationRow extends InvitationWire {
  usedUserName:string;usedUserPhoneNumber:string;status:1|2;
}
export interface InvitationQuery {
  status:0|1|2;keyword?:string;codes?:string[];userIDs?:string[];
  pagination:{pageNumber:number;showNumber:number};
}
export function invitationRow(wire:InvitationWire):InvitationRow {
  if(typeof wire?.invitationCode!=='string'||!wire.invitationCode)throw new Error('邀请码数据不完整');
  return {...wire,usedUserName:wire.usedUser?.nickname||wire.usedUserName||'',
    usedUserPhoneNumber:wire.usedUserPhoneNumber||'',status:wire.usedUserID||wire.usedUser?.userID?1:2};
}
export function invitationFilter(status:unknown,keyword:unknown):Pick<InvitationQuery,'status'|'keyword'> {
  const number=Number(status);
  return {status:number===1||number===2?number:0,keyword:typeof keyword==='string'?keyword.trim():''};
}
/** Quotes delimiters/newlines and prevents formulas from user-controlled cells. */
export function csvCell(value:unknown):string {
  let text=String(value??'');
  if(/^[\s\u0000-\u001f]*[=+@-]/.test(text))text="'"+text;
  return '"'+text.replace(/"/g,'""')+'"';
}
export function invitationCSV(rows:InvitationRow[]):string {
  return '\uFEFF'+[['序号','邀请码','使用人昵称','使用人手机号','使用状态'],...rows.map((row,index)=>[
    index+1,row.invitationCode,row.usedUserName||'—',row.usedUserPhoneNumber||'—',row.status===1?'已使用':'未使用',
  ])].map(row=>row.map(csvCell).join(',')).join('\r\n');
}
export async function collectInvitationExport(options:{
  fetch:(query:InvitationQuery)=>Promise<{list?:InvitationWire[];total?:number}>;
  filter:Pick<InvitationQuery,'status'|'keyword'>;signal:AbortSignal;assertCurrent:()=>void;
  onProgress?:(loaded:number,total:number)=>void;maxRows?:number;pageSize?:number;
}):Promise<InvitationRow[]> {
  const max=options.maxRows??50000,size=options.pageSize??200,rows:InvitationRow[]=[],seen=new Set<string>();
  let total:number|undefined;
  for(let page=1;;page++){
    options.signal.throwIfAborted();options.assertCurrent();
    const data=await options.fetch({...options.filter,pagination:{pageNumber:page,showNumber:size}});
    options.signal.throwIfAborted();options.assertCurrent();
    const count=data.total??0;
    if(!Number.isSafeInteger(count)||count<0)throw new Error('导出总数无效，请刷新后重试');
    if(count>max)throw new Error(`筛选结果超过${max}条，请缩小范围后导出`);
    if(total!==undefined&&count!==total)throw new Error('导出期间列表发生变化，请重新查询后导出');
    total=count;
    const list=data.list??[];
    if(!Array.isArray(list)||list.length>size)throw new Error('导出分页响应无效');
    for(const wire of list){
      const row=invitationRow(wire);
      if(seen.has(row.invitationCode))throw new Error('导出期间出现重复页，请重新查询后导出');
      seen.add(row.invitationCode);rows.push(row);
    }
    if(rows.length>total||rows.length>max)throw new Error('导出分页总数不一致');
    options.onProgress?.(rows.length,total);
    if(rows.length===total)return rows;
    if(!list.length)throw new Error('导出未取得完整结果，请重新查询后导出');
  }
}
