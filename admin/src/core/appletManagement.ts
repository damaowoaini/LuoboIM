import {secureAssetURL} from './adminAssetUpload';

export interface AppletDraft {
  name:string;appID:string;icon:string;url:string;md5:string;size:number;version:string;priority:number;status:1|2;
}
export interface AppletRecord extends AppletDraft {id:string;createTime:number;}
export function appletFromWire(input:Partial<AppletRecord>):AppletRecord {
  return {id:input.id??'',createTime:input.createTime??0,name:input.name??'',appID:input.appID??'',
    icon:input.icon??'',url:input.url??'',md5:input.md5??'',size:input.size??0,version:input.version??'',
    priority:input.priority??0,status:(input.status??0) as 1|2};
}
const fields=['name','appID','icon','url','md5','size','version','priority','status'] as const;
export function normalizedApplet(input:AppletDraft):AppletDraft {
  const value:AppletDraft={name:input.name.trim(),appID:input.appID.trim(),version:input.version.trim(),
    icon:input.icon.trim(),url:input.url.trim(),md5:input.md5.trim().toLowerCase(),
    size:input.size,priority:input.priority,status:input.status};
  if(!value.name||value.name.length>64||!value.appID||value.appID.length>255||!value.version||value.version.length>64)throw new Error('请填写有效的应用名称、AppID与版本');
  if(!Number.isSafeInteger(value.size)||value.size<=0||!Number.isSafeInteger(value.priority)||value.priority<0||value.priority>0xffffffff||![1,2].includes(value.status)||!/^[a-f0-9]{32}$/.test(value.md5))throw new Error('安装包大小、MD5、排序或状态无效');
  for(const url of [value.url,...(value.icon?[value.icon]:[])]){secureAssetURL(url);if(url.length>255)throw new Error('文件地址长度不能超过255个字符');}
  return value;
}
export function appletCreateBody(input:AppletDraft,id:string) {
  const data=normalizedApplet(input);
  return {id,...data};
}
export function appletUpdateBody(input:AppletDraft,original:AppletRecord) {
  const data=normalizedApplet(input);
  const result:{id:string;[key:string]:unknown}={id:original.id};
  // This API uses Gin's encoding/json into protobuf wrapper structs, not protojson scalars.
  for(const field of fields)if(data[field]!==original[field])result[field]={value:data[field]};
  return result;
}
