import {describe,it,expect}from 'vitest';
import {ProvisioningJournal,creationStatus,checkedProvisioningStatus,provisioningMessage,provisioningTerminal}from './adminProvisioning';
const first='11111111-1111-4111-8111-111111111111',second='22222222-2222-4222-8222-222222222222';
function fixture(){
 const values=new Map<string,string>();const storage={getItem:(k:string)=>values.get(k)??null,setItem:(k:string,v:string)=>{values.set(k,v);},removeItem:(k:string)=>{values.delete(k);}};
 let queue=Promise.resolve();const exclusive=<T>(_key:string,op:()=>T):Promise<T>=>{const next=queue.then(op);queue=next.then(()=>undefined,()=>undefined);return next;};
 return {values,storage,exclusive};
}
describe('durable administrator creation intent',()=>{
 it('persists only a stable random ID before dispatch and resumes after reload',async()=>{
  const f=fixture();const j=new ProvisioningJournal(f.storage,'https://a.test','admin',()=>first,f.exclusive);
  expect(await j.reserve()).toBe(first);expect(await j.reserve()).toBe(first);
  expect(new ProvisioningJournal(f.storage,'https://a.test','admin',()=>second,f.exclusive).read().activeRequestID).toBe(first);
  expect(JSON.parse(f.values.get(j.key)!)).toEqual({version:1,requestIDs:[first],activeRequestID:first});
 });
 it('serializes concurrent tabs so one active intent cannot be silently overwritten',async()=>{
  const f=fixture();const a=new ProvisioningJournal(f.storage,'a','admin',()=>first,f.exclusive),b=new ProvisioningJournal(f.storage,'a','admin',()=>second,f.exclusive);
  expect(await Promise.all([a.reserve(),b.reserve()])).toEqual([first,first]);expect(a.read().requestIDs).toEqual([first]);
 });
 it('keeps created pending IDs recoverable while allowing an explicit next creation',async()=>{
  const f=fixture();let count=0;const j=new ProvisioningJournal(f.storage,'a','admin',()=>count++?second:first,f.exclusive);
  await j.reserve();await j.settle(first,false);expect(await j.reserve()).toBe(second);expect(j.read().requestIDs).toEqual([first,second]);
  await j.settle(first,true);expect(j.read()).toEqual({version:1,requestIDs:[second],activeRequestID:second});
 });
 it('isolates tenant/account namespaces and fails closed on local persistence failure',async()=>{
  const f=fixture(),a=new ProvisioningJournal(f.storage,'a','same',()=>first,f.exclusive),b=new ProvisioningJournal(f.storage,'b','same',()=>second,f.exclusive);
  await a.reserve();expect(b.read().requestIDs).toEqual([]);
  const broken=new ProvisioningJournal({...f.storage,setItem(){throw new Error('quota');}},'a','other',()=>second,f.exclusive);
  await expect(broken.reserve()).rejects.toThrow('quota');expect(broken.read().requestIDs).toEqual([]);
 });
 it('does not mistake unknown/reserved or malformed final responses for no creation',()=>{
  expect(()=>creationStatus({userID:'user',adminRequestID:second},first)).toThrow();
  expect(()=>creationStatus({},first)).toThrow();
  expect(()=>creationStatus({userID:'user',provisioningState:'bound',provisioningPending:true},first)).toThrow();
  expect(()=>checkedProvisioningStatus({found:false,state:'rejected',definitiveNotCreated:true},first)).toThrow();
  expect(()=>checkedProvisioningStatus({found:true,state:'bound',created:false},first)).toThrow();
  expect(provisioningTerminal({found:false,state:'unknown',definitiveNotCreated:true})).toBe(false);
  expect(provisioningTerminal({found:true,state:'reserved'})).toBe(false);
  expect(provisioningTerminal({found:true,state:'rejected',definitiveNotCreated:true})).toBe(true);
  expect(provisioningMessage(creationStatus({userID:'user',provisioningState:'pending'},first))).toBe('账号已创建，登录路由待确认');
  expect(provisioningTerminal(creationStatus({userID:'user',provisioningState:'pending'},first))).toBe(false);
 });
});
