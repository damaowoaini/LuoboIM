import {describe,it,expect,vi}from 'vitest';
import {collectInvitationExport,csvCell,invitationCSV,invitationFilter,invitationRow}from './invitationExport';
const code=(invitationCode:string)=>({invitationCode});
const defaults=()=>({filter:{status:0 as const,keyword:'specific'},signal:new AbortController().signal,assertCurrent:()=>{},pageSize:2});
describe('invitation export real API contract and bounded lifecycle',()=>{
 it('collects every filtered page directly instead of exporting a previous React state page',async()=>{
  const fetch=vi.fn().mockResolvedValueOnce({total:5,list:[code('A'),code('B')]}).mockResolvedValueOnce({total:5,list:[code('C'),code('D')]}).mockResolvedValueOnce({total:5,list:[code('E')]});
  const rows=await collectInvitationExport({...defaults(),fetch});expect(rows.map(r=>r.invitationCode)).toEqual(['A','B','C','D','E']);
  expect(fetch.mock.calls.map(call=>call[0])).toEqual([1,2,3].map(pageNumber=>({status:0,keyword:'specific',pagination:{pageNumber,showNumber:2}})));
 });
 it('stops before another page when explicitly cancelled',async()=>{
  const controller=new AbortController();const fetch=vi.fn().mockImplementation(async()=>{controller.abort();return {total:3,list:[code('A'),code('B')]};});
  await expect(collectInvitationExport({...defaults(),fetch,signal:controller.signal})).rejects.toThrow();expect(fetch).toHaveBeenCalledOnce();
 });
 it('rejects a late page from an old administrator generation',async()=>{
  let active=true;const fetch=vi.fn().mockImplementation(async()=>{active=false;return {total:3,list:[code('A'),code('B')]};});
  await expect(collectInvitationExport({...defaults(),fetch,assertCurrent:()=>{if(!active)throw new Error('old generation');}})).rejects.toThrow('old generation');expect(fetch).toHaveBeenCalledOnce();
 });
 it('refuses changed totals, duplicate pages and missing pages instead of exporting partial data',async()=>{
  for(const second of [{total:4,list:[code('C')]},{total:3,list:[code('A')]},{total:3,list:[]}]){
   const fetch=vi.fn().mockResolvedValueOnce({total:3,list:[code('A'),code('B')]}).mockResolvedValueOnce(second);
   await expect(collectInvitationExport({...defaults(),fetch})).rejects.toThrow();
  }
 });
 it('honors the explicit row bound and accepts protobuf empty defaults',async()=>{
  await expect(collectInvitationExport({...defaults(),fetch:async()=>({total:50001,list:[]})})).rejects.toThrow('超过50000');
  expect(await collectInvitationExport({...defaults(),fetch:async()=>({})})).toEqual([]);
 });
 it('uses server IDs for usage state and escapes CSV text/formulas/newlines',()=>{
  const row=invitationRow({invitationCode:'SYNTHETIC',usedUserID:'removed-profile',usedUser:{nickname:'=SUM(1,2)\n"昵称"'}});
  expect(row.status).toBe(1);expect(invitationCSV([row])).toContain('"\'=SUM(1,2)\n""昵称"""');
  expect(invitationCSV([row])).toContain('已使用');expect(csvCell(' \t+123')).toBe('"\' \t+123"');
  expect(invitationFilter('全部',null)).toEqual({status:0,keyword:''});expect(invitationFilter('1','  code  ')).toEqual({status:1,keyword:'code'});
 });
});
