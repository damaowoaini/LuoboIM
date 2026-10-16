import type {AdminSessions}from './adminSession';

/** Capture one owner across upload, profile mutation and list refresh. */
export async function updateCapturedUserAvatar(owner:AdminSessions,userID:string,
  upload:()=>Promise<string|undefined>,update:(body:{userID:string;faceURL:string})=>Promise<unknown>,reload:()=>void) {
  if(!owner.current || typeof userID!=='string' || !userID)throw new Error('管理员会话或目标用户无效');
  const lease=owner.lease();
  try {
    const faceURL=await upload();
    lease.assertCurrent();if(lease.signal.aborted)throw new Error('头像更新已取消');
    if(!faceURL)return;
    await update({userID,faceURL});
    lease.assertCurrent();if(lease.signal.aborted)throw new Error('头像更新已取消');
    reload();
  }finally{lease.release();}
}
