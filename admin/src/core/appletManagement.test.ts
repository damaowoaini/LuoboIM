import {describe,expect,it} from 'vitest';
import {appletCreateBody,appletFromWire,appletUpdateBody,type AppletRecord} from './appletManagement';
const original:AppletRecord={id:'task-owned',createTime:1,name:'合成应用',appID:'__UNI__SYNTHETIC',icon:'',url:'https://objects.fixture.example/package.wgt',md5:'a'.repeat(32),size:86,version:'1.0.0',priority:0,status:2};
describe('applet management actual chart contracts',()=>{
  it('restores protobuf omitted zero fields before an existing record enters the edit form',()=>{
    const wire={...original} as Partial<AppletRecord>;delete wire.icon;delete wire.priority;
    expect(appletUpdateBody(appletFromWire(wire),appletFromWire(wire))).toEqual({id:original.id});
  });
  it('uses flat create values and only-changed explicit protobuf wrappers for update',()=>{
    const value=appletCreateBody({...original,name:' 新名称 '},'stable-receipt');
    expect(value.id).toBe('stable-receipt');expect(value.name).toBe('新名称');expect(value).not.toHaveProperty('createTime');
    expect(appletUpdateBody({...original,name:' 新名称 ',status:1},original)).toEqual({id:'task-owned',name:{value:'新名称'},status:{value:1}});
    expect(appletUpdateBody(original,original)).toEqual({id:'task-owned'});
  });
  it('refuses unsafe URLs or invalid package integrity and capacity values before mutation',()=>{
    for(const changes of [{url:'javascript:alert(1)'},{url:'http://fixture.example/package'},{md5:'not-a-hash'},{size:0},{priority:-1},{status:3},{name:' '}])
      expect(()=>appletCreateBody({...original,...changes} as AppletRecord,'id')).toThrow();
  });
});
