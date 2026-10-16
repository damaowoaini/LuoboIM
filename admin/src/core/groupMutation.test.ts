import {describe,expect,it} from 'vitest';
import {groupCreatePayload,groupEditPayload,repeatedGroupSelection} from './groupMutation';
const original={groupID:'owned-group',groupName:'original',faceURL:'https://objects.synthetic.test/old.png',notification:'notice',introduction:'intro',ex:'{"custom":"retain"}',needVerification:1,lookMemberInfo:1,applyMemberFriend:1};
describe('exact existing group mutation wire contract',()=>{
 it('puts the uploaded avatar into the only consumed nested request and preserves every other field',()=>{
  const result=groupEditPayload({groupID:'ignored-other',groupName:'edited',groupInfoForSet:{futureField:'retain'}},original,'https://objects.synthetic.test/new.png');
  expect(result).toEqual({groupInfoForSet:{...original,groupName:'edited',faceURL:'https://objects.synthetic.test/new.png',futureField:'retain'}});expect(result).not.toHaveProperty('faceURL');expect(original.faceURL).toContain('old.png');
 });
 it('preserves the existing avatar when no new upload completed and keeps raw zero/string codec values',()=>{
  const result=groupEditPayload({lookMemberInfo:0,applyMemberFriend:0,needVerification:0,ex:''},original);
  expect(result.groupInfoForSet).toMatchObject({faceURL:original.faceURL,lookMemberInfo:0,applyMemberFriend:0,needVerification:0,ex:''});
  expect(result.groupInfoForSet.needVerification).not.toEqual({value:0});
 });
 it('rejects duplicate owner/admin/member assignments without changing role semantics',()=>{
  expect(repeatedGroupSelection('owner',['admin'],['member'])).toBe(false);expect(repeatedGroupSelection('owner',['owner'],[])).toBe(true);expect(repeatedGroupSelection('owner',['admin'],['admin'])).toBe(true);
 });
 it('keeps the create-group path as ordinary GroupInfo with working-group2 and distinct administrator/member IDs',()=>{
  const payload=groupCreatePayload({...original,ownerUserID:'owner',adminUserIDs:['admin'],memberUserIDs:['member'],needVerification:0,lookMemberInfo:0},'https://objects.synthetic.test/new.png');
  expect(payload.groupInfo).toMatchObject({groupType:2,faceURL:'https://objects.synthetic.test/new.png',needVerification:0,lookMemberInfo:0,ex:''});expect(payload).not.toHaveProperty('groupInfoForSet');expect(payload.adminUserIDs).toEqual(['admin']);expect(payload.memberUserIDs).toEqual(['member']);
 });
});
