export type BatchDraft={sendID:string;isSendAll:boolean;recvIDList:string[];text:string};
export type ConfirmedBatchMessage={recvID:string;serverMsgID:string;clientMsgID:string;sendTime:number};
export type BatchSendReport={state:'confirmed'|'partial'|'unconfirmed'|'empty';confirmed:ConfirmedBatchMessage[];unconfirmedIDs:string[];complete:boolean;notice:string};
export function freezeBatchDraft(draft:BatchDraft):Readonly<BatchDraft>{
  return Object.freeze({...draft,recvIDList:Object.freeze([...new Set(draft.recvIDList)]) as unknown as string[]});
}
export function sameBatchDraft(a:Readonly<BatchDraft>,b:Readonly<BatchDraft>){
  return a.sendID===b.sendID&&a.isSendAll===b.isSendAll&&a.text===b.text&&(a.isSendAll||JSON.stringify([...a.recvIDList].sort())===JSON.stringify([...b.recvIDList].sort()));
}
export function unknownBatchReport(draft:Readonly<BatchDraft>):BatchSendReport{
  return {state:'unconfirmed',confirmed:[],unconfirmedIDs:draft.isSendAll?[]:[...draft.recvIDList],complete:false,notice:'发送结果未确认。原草稿已保留，不会自动重发；请先查询消息记录，避免重复发送。'};
}

/** HTTP errCode0 means the loop completed, not that each recipient accepted.
 * FailedIDs has the actual JSON name failedUserIDs. An RPC error may follow
 * a committed send, so those IDs remain unconfirmed rather than retry-safe. */
export function batchSendReport(value:unknown,draft:Readonly<BatchDraft>):BatchSendReport{
  if(!value||typeof value!=='object'||Array.isArray(value))return unknownBatchReport(draft);
  const data=value as Record<string,unknown>;
  if(!Object.hasOwn(data,'results')||!Object.hasOwn(data,'failedUserIDs')||data.results!==null&&!Array.isArray(data.results)||data.failedUserIDs!==null&&!Array.isArray(data.failedUserIDs))return unknownBatchReport(draft);
  const confirmed:ConfirmedBatchMessage[]=[],failed=new Set<string>(),seen=new Set<string>(),expected=new Set(draft.recvIDList);
  for(const raw of (data.results??[]) as unknown[]){
    if(!raw||typeof raw!=='object'||Array.isArray(raw))return unknownBatchReport(draft);
    const item=raw as Record<string,unknown>;
    if(['recvID','serverMsgID','clientMsgID'].some(key=>typeof item[key]!=='string'||!(item[key] as string).trim())||!Number.isSafeInteger(item.sendTime)||Number(item.sendTime)<=0)return unknownBatchReport(draft);
    const message=item as ConfirmedBatchMessage;
    if(seen.has(message.recvID)||!draft.isSendAll&&!expected.has(message.recvID))return unknownBatchReport(draft);
    seen.add(message.recvID);confirmed.push({...message});
  }
  for(const id of (data.failedUserIDs??[]) as unknown[]){
    if(typeof id!=='string'||!id.trim()||seen.has(id)||failed.has(id)||!draft.isSendAll&&!expected.has(id))return unknownBatchReport(draft);
    failed.add(id);
  }
  const missing=draft.isSendAll?[]:[...expected].filter(id=>!seen.has(id)&&!failed.has(id));
  const unconfirmedIDs=[...failed,...missing],complete=missing.length===0;
  if(!confirmed.length&&!unconfirmedIDs.length)return {state:'empty',confirmed,unconfirmedIDs,complete,notice:'本次没有已确认的接收用户，没有可报告的发送成功。'};
  if(complete&&!unconfirmedIDs.length)return {state:'confirmed',confirmed,unconfirmedIDs,complete,notice:`已确认向${confirmed.length}位用户发送。`};
  return {state:confirmed.length?'partial':'unconfirmed',confirmed,unconfirmedIDs,complete,notice:`已确认发送${confirmed.length}位，${unconfirmedIDs.length}位未确认。不会自动重发，请先查询消息记录；服务端返回的失败对象也可能已提交。`};
}
