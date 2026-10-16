import { afterEach, describe, expect, it, vi } from 'vitest';
import { AdminSessions } from './adminSession';
import { logoutCurrentAdmin } from './adminLogout';

const fixture = () => {
  const records = new Map<string,string>();
  const storage = { getItem:(key:string)=>records.get(key) ?? null,
    setItem:(key:string,value:string)=>{records.set(key,value);},removeItem:(key:string)=>{records.delete(key);} };
  const owner = new AdminSessions(storage,'https://fixture.example');
  const values = (name:string) => ({account:name,adminToken:`admin-${name}`,imToken:`im-${name}`,imUserID:name,level:80});
  owner.commit(values('old'),0);
  return {owner,storage,values};
};
afterEach(()=>vi.useRealTimers());
describe('exact administrator logout',()=>{
  it('revokes the captured chart session before local destruction',async()=>{
    const {owner}=fixture();
    expect(await logoutCurrentAdmin(owner,async snapshot=>{
      expect(snapshot.adminToken).toBe('admin-old'); expect(owner.current).toBeDefined(); return {revoked:true};
    })).toBe('revoked');
    expect(owner.current).toBeUndefined();
  });
  it('still exits locally on server failure and inaccessible storage',async()=>{
    const {owner,storage}=fixture(); storage.removeItem=()=>{throw new Error('blocked');};
    expect(await logoutCurrentAdmin(owner,async()=>{throw new Error('network');})).toBe('unconfirmed');
    expect(owner.current).toBeUndefined();
  });
  it('does not let a late old-session completion erase a new administrator',async()=>{
    const {owner,values}=fixture();
    expect(await logoutCurrentAdmin(owner,async()=>{owner.commit(values('new'),owner.generation);return {revoked:true};})).toBe('stale');
    expect(owner.current?.account).toBe('new');
  });
  it('bounds an unresponsive endpoint and aborts without claiming server revocation',async()=>{
    vi.useFakeTimers(); const {owner}=fixture(); let signal:AbortSignal|undefined;
    const result=logoutCurrentAdmin(owner,async(_session,currentSignal)=>{signal=currentSignal;return new Promise(()=>{});},100);
    await vi.advanceTimersByTimeAsync(101);
    expect(await result).toBe('unconfirmed'); expect(signal?.aborted).toBe(true);expect(owner.current).toBeUndefined();
  });
});
