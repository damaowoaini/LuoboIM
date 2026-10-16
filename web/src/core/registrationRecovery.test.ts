import {describe,expect,it} from "vitest";
import {RegistrationRecovery,registrationFlags,type RegistrationDraft} from "./registrationRecovery";
const owner={issuer:"https://route.example",generation:3};
const draft=():RegistrationDraft=>({loginMethod:"email",email:"synthetic@example.test",areaCode:"+86",nickname:"before",password:"Original123",verifyCode:"123456",invitationCode:"synthetic-invite"});
describe("registration uncertain commit boundary",()=>{
  it("freezes the first draft before dispatch and never permits another register even for plain1001",()=>{
    const recovery=new RegistrationRecovery(),input=draft(),ticket=recovery.begin(input,owner);
    input.password="Changed456";input.nickname="after";
    recovery.failed(ticket,{errCode:1001});
    expect(recovery.getSnapshot().phase).toBe("unknown");
    expect(()=>recovery.begin(draft(),owner)).toThrow();
    expect(recovery.registration(p=>`digest:${p}`).user).toMatchObject({password:"digest:Original123",nickname:"before"});
    const login=recovery.beginLogin(owner.issuer,owner.generation,"",p=>`digest:${p}`);
    expect(login).toMatchObject({email:"synthetic@example.test",password:"digest:Original123"});
    expect(login).not.toHaveProperty("verifyCode");expect(login).not.toHaveProperty("invitationCode");
    expect(()=>recovery.beginLogin(owner.issuer,owner.generation,"",p=>p)).toThrow();
  });
  it("uses strict structured flags and never translated text or errCode alone",()=>{
    for(const [error,phase] of [[{errCode:1002},"unknown"],[{errCode:500,errMsg:"账号注册和路由已恢复"},"unknown"],[{registration:{registrationPending:true}},"pending"],[{registration:{registrationRecovered:true,requiresLogin:true}},"recovered"],[{registration:{registrationPending:"true"}},"unknown"]] as const){
      const recovery=new RegistrationRecovery(),ticket=recovery.begin(draft(),owner);recovery.failed(ticket,error);
      expect(recovery.getSnapshot().phase).toBe(phase);expect(()=>recovery.begin(draft(),owner)).toThrow();
    }
  });
  it("preserves pending state on failed original-password login, without freeing a registration retry",()=>{
    const recovery=new RegistrationRecovery(),ticket=recovery.begin(draft(),owner);recovery.failed(ticket,new Error("lost ACK"));
    recovery.beginLogin(owner.issuer,owner.generation,"654321",p=>p);
    recovery.loginFailed(ticket,{registration:{registrationPending:true}});
    expect(recovery.getSnapshot().phase).toBe("pending");expect(recovery.isCurrent(ticket)).toBe(true);
    expect(()=>recovery.begin(draft(),owner)).toThrow();
  });
  it("does not disclose original credentials to a changed issuer or session",()=>{
    const recovery=new RegistrationRecovery(),ticket=recovery.begin(draft(),owner);recovery.failed(ticket,new Error("timeout"));
    let digests=0;const digest=()=>{digests++;return "wire";};
    expect(()=>recovery.beginLogin("https://different.example",3,"",digest)).toThrow();
    expect(()=>recovery.beginLogin(owner.issuer,4,"",digest)).toThrow();
    expect(digests).toBe(0);expect(recovery.getSnapshot().phase).toBe("unknown");
  });
  it("retains only in-memory private fields; dispose/completion drop secrets and reject late callbacks",()=>{
    const recovery=new RegistrationRecovery(),ticket=recovery.begin(draft(),owner);
    expect(JSON.stringify(recovery)).not.toContain("Original123");expect(JSON.stringify(recovery.getSnapshot())).not.toContain("123456");
    recovery.dispose();recovery.failed(ticket,{registration:{registrationRecovered:true}});
    expect(recovery.getSnapshot().phase).toBe("idle");expect(recovery.currentTicket()).toBeUndefined();
    expect(()=>recovery.registration(p=>p)).toThrow();
    const next=recovery.begin(draft(),owner);recovery.complete(next);
    expect(recovery.getSnapshot().phase).toBe("complete");expect(()=>recovery.registration(p=>p)).toThrow();
  });
  it("keeps telephone and email recovery identities separate",()=>{
    const recovery=new RegistrationRecovery(),ticket=recovery.begin({...draft(),loginMethod:"phone",phoneNumber:"10000000000"},owner);
    recovery.failed(ticket,new Error());const wire=recovery.beginLogin(owner.issuer,3,"",p=>p);
    expect(wire).toMatchObject({phoneNumber:"10000000000",areaCode:"+86"});expect(wire).not.toHaveProperty("email");
  });
  it("sanitizes reply hints without preserving credentials or arbitrary upstream data",()=>{
    expect(registrationFlags({registrationPending:true,requiresLogin:true,password:"secret",imToken:"secret",userID:"private",registrationRecovered:false})).toEqual({registrationPending:true,requiresLogin:true});
    expect(registrationFlags({registrationPending:"true"})).toEqual({});
    expect(registrationFlags({registrationPending:true,registrationRecovered:true})).toEqual({});
  });
});
