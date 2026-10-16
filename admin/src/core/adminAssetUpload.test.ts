import {describe,expect,it,vi} from 'vitest';
import {AdminSessions} from './adminSession';
import {uploadAdminAsset,type AdminAssetTransport} from './adminAssetUpload';
const hash='a'.repeat(32);
const fixture=()=>{
  const store=new Map<string,string>();const owner=new AdminSessions({getItem:k=>store.get(k)||null,setItem:(k,v)=>{store.set(k,v);},removeItem:k=>{store.delete(k);}},'https://fixture.example');
  const login=(name:string)=>owner.commit({account:name,imUserID:name,adminToken:'chart-'+name,imToken:'delegate-'+name,level:80},owner.generation);login('a');
  const transport:AdminAssetTransport={hash:vi.fn().mockResolvedValue(hash),initiate:vi.fn().mockResolvedValue({upload:{uploadID:'opaque',partSize:64,sign:{header:[{key:'x-provider',values:['shared']}],parts:[{partNumber:1,url:'https://objects.fixture.example/temp',query:[{key:'synthetic',values:['signature']}]}]}}}),put:vi.fn().mockResolvedValue(undefined),complete:vi.fn().mockResolvedValue('https://objects.fixture.example/final')};
  return {owner,login,transport,file:new File(['synthetic'],'task.wgt',{type:'application/octet-stream'})};
};
describe('administrator asset upload ownership and acknowledgement',()=>{
  it('completes only after PUT confirmation and retains original hash/size and provider signing fields',async()=>{
    const {owner,transport,file}=fixture();const result=await uploadAdminAsset(owner,file,transport,64);
    expect(result).toEqual({url:'https://objects.fixture.example/final',md5:hash,size:file.size});
    const [url,headers]=vi.mocked(transport.put).mock.calls[0];expect(url).toContain('synthetic=signature');expect(headers.get('x-provider')).toBe('shared');
    expect(transport.complete).toHaveBeenCalledWith('opaque',hash,file,expect.any(AbortSignal));
  });
  it('never calls complete after rejected PUT or malformed multipart signing data',async()=>{
    const {owner,transport,file}=fixture();vi.mocked(transport.put).mockRejectedValue(new Error('507'));
    await expect(uploadAdminAsset(owner,file,transport,64)).rejects.toThrow('507');expect(transport.complete).not.toHaveBeenCalled();
    vi.mocked(transport.initiate).mockResolvedValue({upload:{uploadID:'opaque',partSize:1,sign:{parts:[]}}});
    await expect(uploadAdminAsset(owner,file,transport,64)).rejects.toThrow('单分片');expect(transport.complete).not.toHaveBeenCalled();
  });
  it('does not initiate under a replacement administrator after hashing or finalize after a mid-PUT replacement',async()=>{
    const {owner,login,transport,file}=fixture();vi.mocked(transport.hash).mockImplementation(async()=>{login('b');return hash;});
    await expect(uploadAdminAsset(owner,file,transport,64)).rejects.toThrow('会话已变更');expect(transport.initiate).not.toHaveBeenCalled();
    vi.mocked(transport.hash).mockResolvedValue(hash);vi.mocked(transport.put).mockImplementation(async(_url,_headers,_file,signal)=>{login('c');expect(signal.aborted).toBe(true);});
    await expect(uploadAdminAsset(owner,file,transport,64)).rejects.toThrow('会话已变更');expect(transport.complete).not.toHaveBeenCalled();
  });
  it('honors dialog cancellation before any network request and refuses insecure success URLs',async()=>{
    const {owner,transport,file}=fixture();const stop=new AbortController();stop.abort();
    await expect(uploadAdminAsset(owner,file,transport,64,stop.signal)).rejects.toThrow('取消');expect(transport.initiate).not.toHaveBeenCalled();
    vi.mocked(transport.initiate).mockResolvedValue({url:'http://insecure.fixture.example/final'});
    await expect(uploadAdminAsset(owner,file,transport,64)).rejects.toThrow('HTTPS');
  });
});
