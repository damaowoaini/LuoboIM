import {describe,it,expect,vi}from 'vitest';
import {AdminSessions}from './adminSession';
import {updateCapturedUserAvatar}from './adminUserAvatar';
function session(){const values=new Map<string,string>();const owner=new AdminSessions({getItem:k=>values.get(k)??null,setItem:(k,v)=>{values.set(k,v);},removeItem:k=>{values.delete(k);}},'https://synthetic.test');
 owner.commit({account:'first',adminToken:'synthetic-chart',imToken:'synthetic-delegate',imUserID:'manager',level:80},0);return owner;}
describe('avatar mutation keeps the uploading administrator generation',()=>{
 it('uses exactly the captured target and refreshes only after profile acknowledgement',async()=>{
  const owner=session(),update=vi.fn().mockResolvedValue({}),reload=vi.fn();
  await updateCapturedUserAvatar(owner,'synthetic-target',async()=> 'https://synthetic.test/avatar',update,reload);
  expect(update).toHaveBeenCalledExactlyOnceWith({userID:'synthetic-target',faceURL:'https://synthetic.test/avatar'});expect(reload).toHaveBeenCalledOnce();
 });
 it('never sends a profile mutation when the upload returns after an admin replacement',async()=>{
  const owner=session(),update=vi.fn(),reload=vi.fn();let finish!:(url:string)=>void;
  const pending=updateCapturedUserAvatar(owner,'same-ID-in-old-tenant',()=>new Promise(resolve=>{finish=resolve;}),update,reload);
  owner.commit({account:'second',adminToken:'new-chart',imToken:'new-delegate',imUserID:'new-manager',level:80},owner.generation);finish('https://synthetic.test/avatar');
  await expect(pending).rejects.toThrow('管理员会话已变更');expect(update).not.toHaveBeenCalled();expect(reload).not.toHaveBeenCalled();
 });
 it('does not refresh a new admin view when a previous mutation response arrives late',async()=>{
  const owner=session(),reload=vi.fn();let finish!:()=>void;
  const update=vi.fn().mockImplementation(()=>new Promise<void>(resolve=>{finish=resolve;}));
  const pending=updateCapturedUserAvatar(owner,'target',async()=> 'https://synthetic.test/avatar',update,reload);
  await vi.waitFor(()=>expect(update).toHaveBeenCalledOnce());owner.clear();finish();
  await expect(pending).rejects.toThrow();expect(reload).not.toHaveBeenCalled();
 });
 it('does not claim a refresh for an empty upload or failed profile write',async()=>{
  const owner=session(),update=vi.fn(),reload=vi.fn();await updateCapturedUserAvatar(owner,'target',async()=>undefined,update,reload);expect(update).not.toHaveBeenCalled();
  update.mockRejectedValue(new Error('synthetic write failed'));await expect(updateCapturedUserAvatar(owner,'target',async()=> 'https://synthetic.test/avatar',update,reload)).rejects.toThrow();expect(reload).not.toHaveBeenCalled();
 });
});
