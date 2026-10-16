import type { AdminSessions } from './adminSession';

type KeyValues = {key:string;values:string[]}[];
type SignedPart = {partNumber?:number;url?:string;query?:KeyValues;header?:KeyValues};
export type AdminUploadInit = {url?:string;upload?:{uploadID:string;partSize?:number;sign:{url?:string;query?:KeyValues;header?:KeyValues;parts:SignedPart[]}}};
export interface AdminAssetTransport {
  hash(file:File):Promise<string>;
  initiate(file:File,partHash:string,signal:AbortSignal):Promise<AdminUploadInit>;
  put(url:string,headers:Headers,file:File,signal:AbortSignal):Promise<void>;
  complete(uploadID:string,partHash:string,file:File,signal:AbortSignal):Promise<string>;
}
export function secureAssetURL(value:string) {
  let url:URL;
  try {url=new URL(value);} catch {throw new Error('文件地址无效');}
  if(url.protocol!=='https:'||url.username||url.password||url.hash)throw new Error('文件地址须使用HTTPS且不含账号信息');
  return url;
}
export async function uploadAdminAsset(owner:AdminSessions,file:File,transport:AdminAssetTransport,maxBytes:number,external?:AbortSignal) {
  if(!owner.current)throw new Error('管理员会话已失效');
  if(!Number.isSafeInteger(file.size)||file.size<=0||file.size>maxBytes)throw new Error('文件为空或超过上传大小限制');
  const lease=owner.lease();
  const signal=external ? AbortSignal.any([lease.signal,external]) : lease.signal;
  const assertActive=()=>{lease.assertCurrent();if(signal.aborted)throw new Error('文件上传已取消');};
  try {
    const partHash=await transport.hash(file);
    assertActive();
    if(!/^[0-9a-f]{32}$/.test(partHash))throw new Error('文件校验失败');
    const initialized=await transport.initiate(file,partHash,signal);
    assertActive();
    if(initialized.url)return {url:secureAssetURL(initialized.url).toString(),md5:partHash,size:file.size};
    const upload=initialized.upload;
    if(!upload?.uploadID||upload.sign.parts?.length!==1||upload.partSize&&upload.partSize<file.size)throw new Error('服务端未提供可用的单分片上传信息');
    const part=upload.sign.parts[0];
    if(part.partNumber!==undefined&&part.partNumber!==1)throw new Error('上传分片编号无效');
    const url=secureAssetURL(part.url||upload.sign.url||'');
    for(const item of [...(upload.sign.query??[]),...(part.query??[])])for(const value of item.values)url.searchParams.append(item.key,value);
    const headers=new Headers();
    for(const item of [...(upload.sign.header??[]),...(part.header??[])])for(const value of item.values)headers.append(item.key,value);
    if(!headers.has('Content-Type'))headers.set('Content-Type',file.type||'application/octet-stream');
    await transport.put(url.toString(),headers,file,signal);
    assertActive();
    const completed=await transport.complete(upload.uploadID,partHash,file,signal);
    assertActive();
    return {url:secureAssetURL(completed).toString(),md5:partHash,size:file.size};
  } finally {lease.release();}
}
