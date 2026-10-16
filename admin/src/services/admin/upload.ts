import { adminSessions } from '@/core/adminSession';
import { uploadAdminAsset, type AdminUploadInit } from '@/core/adminAssetUpload';
import { API_URL } from '@/config';
import { request } from '@umijs/max';
import { message } from 'antd';
import file2md5 from 'file2md5';
import md5 from 'md5';
import { UploadRequestOption } from 'rc-upload/lib/interface';

export enum minioUploadType {
  file = '1',
  video = '2',
  picture = '3',
}
export async function minioUpload(
  file: File,
  fileType: minioUploadType,
  onProgress?: (progress: number) => void,
) {
  const data = new FormData();
  data.append('file', file);
  data.append('fileType', fileType);
  data.append('operationID', new Date().getTime() + '');
  return request<{
    data: any;
  }>('/third/minio_upload', {
    method: 'POST',
    data,
    headers: {
      token: adminSessions?.current?.imToken || '',
    },
    baseURL: API_URL,
    onUploadProgress(progressEvent) {
      const complete = (progressEvent.loaded / progressEvent.total) * 100 || 0;
      if (onProgress) {
        onProgress(complete);
      }
    },
  });
}

export async function getCosAuthorization() {
  return request('/api/third/tencent_cloud_storage_credential', {
    method: 'POST',
    data: {},
    baseURL: API_URL,
  });
}

export async function upload(file: File, hash: string, signal?:AbortSignal) {
  return request<{data:AdminUploadInit}>('/object/initiate_multipart_upload', {
    method: 'POST',
    signal,
    data: {
      hash,
      size: file.size,
      partSize: file.size,
      maxParts: -1,
      cause: 'admin-asset',
      name: file.name,
      contentType: file.type || 'application/octet-stream',
    },
    baseURL: API_URL,
  });
}

export async function completeUpload(uploadID: string, hash: string, fileName: string, signal?:AbortSignal,contentType='application/octet-stream') {
  return request('/object/complete_multipart_upload', {
    method: 'POST',
    signal,
    data: {
      uploadID,
      parts: [hash],
      name: `${adminSessions?.current?.imUserID || ''}/${fileName}`,
      contentType,
      cause:'admin-asset',
    },
    baseURL: API_URL,
  });
}

export async function uploadAdminFile(file:File,maxBytes=64*1024*1024,signal?:AbortSignal) {
  if(!adminSessions)throw new Error('管理员会话不可用');
  return uploadAdminAsset(adminSessions,file,{
    hash:file2md5,
    initiate:async(current,hash,signal)=>(await upload(current,md5(hash),signal)).data,
    put:async(url,headers,current,signal)=>{
      const response=await fetch(url,{method:'PUT',headers,body:current,signal,credentials:'omit',redirect:'error'});
      if(!response.ok)throw new Error('文件上传未确认，请重试');
    },
    complete:async(id,hash,current,signal)=>(await completeUpload(id,hash,current.name,signal,current.type||'application/octet-stream')).data.url,
  },maxBytes,signal);
}

export async function handleFileUpload(data: UploadRequestOption) {
  try {
    const fileSize = (data.file as File).size;
    if (fileSize > 1024 * 1024 * 1) {
      message.error('图片尺寸过大');
      return;
    }

    return (await uploadAdminFile(data.file as File,1024*1024)).url;
  } catch (error) {
    void 0;
    message.error('上传失败！');
  }
}
