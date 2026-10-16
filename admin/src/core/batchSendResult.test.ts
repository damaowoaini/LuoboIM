import {describe,expect,it} from 'vitest';
import {batchSendReport,freezeBatchDraft,sameBatchDraft,unknownBatchReport,type BatchDraft} from './batchSendResult';
const draft=():BatchDraft=>({sendID:'system',isSendAll:false,recvIDList:['one','two'],text:'original synthetic content'});
const ack=(recvID:string)=>({recvID,clientMsgID:'client-'+recvID,serverMsgID:'server-'+recvID,sendTime:12345});
describe('actual HTTP batch result contract',()=>{
 it('reports HTTP0 with all failedUserIDs as unconfirmed, never success',()=>{
  const result=batchSendReport({results:null,failedUserIDs:['one','two']},draft());expect(result.state).toBe('unconfirmed');expect(result.confirmed).toHaveLength(0);expect(result.unconfirmedIDs).toEqual(['one','two']);expect(result.notice).toContain('可能已提交');
 });
 it('preserves each acknowledged message and separates partial/omitted targets without replaying the batch',()=>{
  const result=batchSendReport({results:[ack('one')],failedUserIDs:['two']},draft());expect(result.state).toBe('partial');expect(result.confirmed[0]).toEqual(ack('one'));expect(result.unconfirmedIDs).toEqual(['two']);
  const omitted=batchSendReport({results:[ack('one')],failedUserIDs:null},draft());expect(omitted.complete).toBe(false);expect(omitted.state).toBe('partial');expect(omitted.unconfirmedIDs).toEqual(['two']);
 });
 it('accepts complete acknowledgements, including a broadcast response, and does not call an empty response success',()=>{
  expect(batchSendReport({results:[ack('one'),ack('two')],failedUserIDs:null},draft()).state).toBe('confirmed');
  expect(batchSendReport({results:[ack('foreign')],failedUserIDs:[]},{...draft(),isSendAll:true}).state).toBe('confirmed');
  expect(batchSendReport({results:null,failedUserIDs:null},{...draft(),isSendAll:true}).state).toBe('empty');
 });
 it('rejects malformed or contradictory data and the incorrect failedIDs spelling',()=>{
  for(const data of [{results:[ack('one')],failedIDs:['two']},{results:[ack('one')],failedUserIDs:['one']},{results:[ack('foreign')],failedUserIDs:[]},{results:[ack('one'),ack('one')],failedUserIDs:[]},{results:[{recvID:'one'}],failedUserIDs:['two']},{}])expect(batchSendReport(data,draft()).state).toBe('unconfirmed');
 });
 it('freezes the attempted content and recipient set in memory, independent of later form edits',()=>{
  const original=draft(),frozen=freezeBatchDraft(original);original.text='edited';original.recvIDList.push('three');expect(frozen.text).toBe('original synthetic content');expect(frozen.recvIDList).toEqual(['one','two']);
  expect(sameBatchDraft(frozen,{...draft(),recvIDList:['two','one']})).toBe(true);expect(sameBatchDraft(frozen,{...draft(),recvIDList:['two']})).toBe(false);
  expect(unknownBatchReport(frozen).state).toBe('unconfirmed');
 });
});
