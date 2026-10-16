// @vitest-environment jsdom
import {message} from 'antd';
import {afterEach,beforeEach,describe,expect,it,vi} from 'vitest';
const mocks=vi.hoisted(()=>({owner:{generation:1,current:{account:'synthetic'} as undefined|{account:string}}}));
vi.mock('@/core/adminSession',()=>({adminSessions:mocks.owner}));
vi.mock('@/services/admin/upload',()=>({getCosAuthorization:vi.fn(),minioUpload:vi.fn(),minioUploadType:{file:'1'}}));
vi.mock('@/utils/cos',()=>({cosUpload:vi.fn()}));
vi.mock('@/config',()=>({OBJECT_STORAGE:'minio'}));
import {copy2Text} from '../utils/common';
beforeEach(()=>{vi.clearAllMocks();window.location.hash='';mocks.owner.generation=1;mocks.owner.current={account:'synthetic'};vi.spyOn(message,'success').mockImplementation(()=>undefined as never);vi.spyOn(message,'error').mockImplementation(()=>undefined as never);Object.defineProperty(navigator,'clipboard',{configurable:true,value:undefined});Object.defineProperty(document,'execCommand',{configurable:true,value:vi.fn().mockReturnValue(false)});});
afterEach(()=>{document.body.replaceChildren();});
describe('real administrator copy action permission outcomes',()=>{
 it('does not claim success while the browser write is pending and reports it only after acknowledgement',async()=>{
  let finish!:()=>void;const write=vi.fn(()=>new Promise<void>(resolve=>{finish=resolve;}));Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:write}});
  const pending=copy2Text('synthetic-invitation');expect(message.success).not.toHaveBeenCalled();finish();expect(await pending).toBe(true);expect(message.success).toHaveBeenCalledOnce();expect(write).toHaveBeenCalledWith('synthetic-invitation');
 });
 it('contains a denied clipboard rejection and never reports copy success',async()=>{
  Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:vi.fn().mockRejectedValue(new DOMException('denied','NotAllowedError'))}});
  expect(await copy2Text('synthetic-invitation')).toBe(false);expect(message.success).not.toHaveBeenCalled();expect(message.error).toHaveBeenCalledWith('复制未完成，请手动选择文字复制。');
 });
 it('checks a denied legacy copy result, removes the temporary field and restores focus',async()=>{
  const input=document.createElement('input');document.body.append(input);input.focus();expect(await copy2Text('synthetic')).toBe(false);expect(document.querySelector('textarea')).toBeNull();expect(document.activeElement).toBe(input);expect(message.success).not.toHaveBeenCalled();
 });
 it('reports a confirmed fallback only after execCommand returns true',async()=>{
  vi.mocked(document.execCommand).mockReturnValue(true);expect(await copy2Text('synthetic')).toBe(true);expect(message.success).toHaveBeenCalledOnce();expect(message.error).not.toHaveBeenCalled();
 });
 it('does not dispatch without an active administrator or publish a late toast to a new session/page',async()=>{
  const write=vi.fn();Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:write}});mocks.owner.current=undefined;expect(await copy2Text('synthetic')).toBe(false);expect(write).not.toHaveBeenCalled();
  mocks.owner.current={account:'synthetic'};let finish!:()=>void;write.mockImplementation(()=>new Promise<void>(resolve=>{finish=resolve;}));const pending=copy2Text('synthetic');mocks.owner.generation++;finish();expect(await pending).toBe(false);expect(message.success).not.toHaveBeenCalled();expect(message.error).not.toHaveBeenCalled();
 });
 it('does not publish a delayed copy acknowledgement after navigation',async()=>{
  let finish!:()=>void;Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:()=>new Promise<void>(resolve=>{finish=resolve;})}});
  const pending=copy2Text('synthetic');window.location.hash='#another-page';finish();expect(await pending).toBe(false);expect(message.success).not.toHaveBeenCalled();expect(message.error).not.toHaveBeenCalled();
 });
});
