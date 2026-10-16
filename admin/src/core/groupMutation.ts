type GroupFields={groupID?:string;groupName?:string;notification?:string;introduction?:string;faceURL?:string;ex?:string;needVerification?:number;lookMemberInfo?:number;applyMemberFriend?:number;groupInfoForSet?:Record<string,unknown>};

export function groupCreatePayload<T extends GroupFields>(values:T,uploadedAvatar=''){
  return {...values,faceURL:uploadedAvatar,groupInfo:{groupName:values.groupName,notification:values.notification,introduction:values.introduction,faceURL:uploadedAvatar,ex:'',groupType:2,needVerification:values.needVerification,lookMemberInfo:values.lookMemberInfo,applyMemberFriend:values.applyMemberFriend}};
}

/** This protocol's local wrapperspb JSON codec accepts primitive strings/ints.
 * SetGroupInfoReq consumes only groupInfoForSet, including the new avatar. */
export function groupEditPayload(values:GroupFields,original:GroupFields,uploadedAvatar?:string){
  if(!original.groupID)throw new Error('缺少正在编辑的群组ID');
  const groupInfoForSet:{groupID:string;[key:string]:unknown}={...values.groupInfoForSet,groupID:original.groupID};
  for(const field of ['groupName','notification','introduction','ex','needVerification','lookMemberInfo','applyMemberFriend'] as const){
    const value=values[field]??values.groupInfoForSet?.[field]??original[field];if(value!==undefined)groupInfoForSet[field]=value;
  }
  groupInfoForSet.faceURL=uploadedAvatar??values.faceURL??values.groupInfoForSet?.faceURL??original.faceURL??'';
  return {groupInfoForSet};
}

export function repeatedGroupSelection(ownerUserID:string,adminUserIDs:readonly string[]=[],memberUserIDs:readonly string[]=[]){
  const values=[ownerUserID,...adminUserIDs,...memberUserIDs];return new Set(values).size!==values.length;
}
