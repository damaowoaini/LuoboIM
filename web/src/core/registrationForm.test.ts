// @vitest-environment jsdom
import React,{act} from "react";
import {createRoot,type Root} from "react-dom/client";
import {MemoryRouter} from "react-router-dom";
import {App} from "antd";
import md5 from "md5";
import {beforeAll,beforeEach,afterEach,describe,it,expect,vi} from "vitest";
const mocks=vi.hoisted(()=>({register:vi.fn(),login:vi.fn(),send:vi.fn(),verify:vi.fn(),config:vi.fn(),back:vi.fn(),issuer:"https://route.synthetic.test",session:{generation:0},guard:undefined as undefined|{isCurrent?:()=>boolean}}));
vi.mock("@/api/login",()=>({BusinessAllowType:{Allow:1},getAppConfig:mocks.config,useRegister:(guard:typeof mocks.guard)=>{mocks.guard=guard;return {mutate:mocks.register,isPending:false};},useLogin:()=>({mutate:mocks.login,isPending:false}),useSendSms:()=>({mutate:mocks.send,isPending:false}),useVerifyCode:()=>({mutate:mocks.verify,isPending:false})}));
vi.mock("@/config",()=>({getRouteUrl:()=>mocks.issuer}));
vi.mock("@/core/session/browser",()=>({sessionCoordinator:mocks.session}));
vi.mock("@/store",()=>({useUserStore:(selector:(state:any)=>unknown)=>selector({appConfig:{needInvitationCodeRegister:0}})}));
vi.mock("@/utils/storage",()=>({setAreaCode:vi.fn(),setEmail:vi.fn(),setPhoneNumber:vi.fn()}));
vi.mock("i18next",()=>({t:(key:string)=>key}));
import RegisterForm from "../pages/login/RegisterForm";
import {RegistrationRecovery} from "./registrationRecovery";
let host:HTMLDivElement,root:Root;
beforeAll(()=>{
 Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true});
 window.matchMedia=vi.fn().mockImplementation(()=>({matches:false,addListener(){},removeListener(){},addEventListener(){},removeEventListener(){},dispatchEvent(){return true;}}));
 vi.stubGlobal("ResizeObserver",class{observe(){}unobserve(){}disconnect(){}});
 const computed=window.getComputedStyle.bind(window);vi.spyOn(window,"getComputedStyle").mockImplementation(element=>computed(element));
});
beforeEach(()=>{mocks.register.mockReset();mocks.login.mockReset();mocks.send.mockReset();mocks.verify.mockReset();mocks.config.mockReset();mocks.back.mockReset();mocks.issuer="https://route.synthetic.test";mocks.session.generation=0;host=document.createElement("div");document.body.append(host);root=createRoot(host);});
afterEach(async()=>{await act(async()=>root.unmount());host.remove();});
async function mount(recovery:RegistrationRecovery){await act(async()=>root.render(React.createElement(MemoryRouter,{initialEntries:["/login"]},React.createElement(App,null,React.createElement(RegisterForm,{loginMethod:"email",setFormType:mocks.back,recovery})))));}
function button(label:string){const found=[...host.querySelectorAll("button")].find(node=>node.textContent?.replace(/\s/g,"").includes(label));if(!found)throw new Error("missing visible action "+label);return found;}
async function click(node:HTMLElement){await act(async()=>{node.click();await new Promise(resolve=>setTimeout(resolve,0));});}
async function fill(placeholder:string,value:string){const input=host.querySelector(`input[placeholder="${placeholder}"]`) as HTMLInputElement;if(!input)throw new Error("missing form input "+placeholder);await act(async()=>{Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,"value")!.set!.call(input,value);input.dispatchEvent(new Event("input",{bubbles:true}));input.dispatchEvent(new Event("change",{bubbles:true}));});}
async function until(condition:()=>boolean){for(let i=0;i<50;i++){if(condition())return;await act(async()=>{await new Promise(resolve=>setTimeout(resolve,10));});}throw new Error("form transition did not settle");}
describe("actual register form uncertain result",()=>{
 it("keeps a pre-dispatch config failure editable without claiming a registration was attempted",async()=>{
  const recovery=new RegistrationRecovery();mocks.config.mockRejectedValueOnce(new Error("synthetic config unavailable"));await mount(recovery);
  await fill("toast.inputEmail","synthetic@example.test");await click(host.querySelector('button[type="submit"]')!);await until(()=>mocks.config.mock.calls.length===1);
  expect(recovery.hasAttempt()).toBe(false);expect(host.querySelector('button[type="submit"]')).not.toBeNull();expect(mocks.send).not.toHaveBeenCalled();expect(mocks.register).not.toHaveBeenCalled();
  mocks.config.mockResolvedValueOnce({data:{config:{needVerificationCodeRegister:"0"}}});await click(host.querySelector('button[type="submit"]')!);await until(()=>!!host.querySelector('input[placeholder="toast.inputNickName"]'));
  expect(mocks.config).toHaveBeenCalledTimes(2);expect(recovery.hasAttempt()).toBe(false);expect(mocks.register).not.toHaveBeenCalled();
 });
 it("freezes after dispatch1001, exposes only original-password login, and never resends OTP/register",async()=>{
  const recovery=new RegistrationRecovery();mocks.config.mockResolvedValue({data:{config:{needVerificationCodeRegister:"0"}}});await mount(recovery);
  await fill("toast.inputEmail","synthetic@example.test");await click(host.querySelector('button[type="submit"]')!);await until(()=>!!host.querySelector('input[placeholder="toast.inputNickName"]'));
  await fill("toast.inputNickName","synthetic nickname");await fill("toast.inputPassword","Original123");await fill("toast.reconfirmPassword","Original123");await click(host.querySelector('button[type="submit"]')!);await until(()=>mocks.register.mock.calls.length===1);
  expect(recovery.getSnapshot().phase).toBe("submitting");expect(host.querySelector('button[type="submit"]')).toBeNull();
  await act(async()=>mocks.register.mock.calls[0][1].onError({errCode:1001}));
  expect(host.textContent).toContain("注册结果未确认");expect(host.querySelector('button[type="submit"]')).toBeNull();expect(host.innerHTML).not.toContain("Original123");
  await click(button("使用原账号密码登录"));expect(mocks.login).toHaveBeenCalledOnce();
  expect(mocks.login.mock.calls[0][0]).toMatchObject({email:"synthetic@example.test",password:md5("Original123")});expect(mocks.login.mock.calls[0][0]).not.toHaveProperty("verifyCode");
  await click(button("使用原账号密码登录"));expect(mocks.login).toHaveBeenCalledOnce();
  await act(async()=>mocks.login.mock.calls[0][1].onError({registration:{registrationPending:true}}));
  expect(host.textContent).toContain("账号开通仍在确认");expect(mocks.register).toHaveBeenCalledOnce();expect(mocks.send).not.toHaveBeenCalled();
  await act(async()=>recovery.dispose());
 });
 it("keeps the uncertain draft across return/reopen without exposing its password",async()=>{
  const recovery=new RegistrationRecovery(),ticket=recovery.begin({loginMethod:"email",email:"synthetic@example.test",areaCode:"+86",nickname:"synthetic",invitationCode:"",verifyCode:"123456",password:"Original123"},{issuer:mocks.issuer,generation:0});recovery.failed(ticket,new Error("lost ACK"));
  await mount(recovery);await click(button("返回登录页"));expect(mocks.back).toHaveBeenCalledWith(0);expect(mocks.guard?.isCurrent?.()).toBe(false);
  await act(async()=>root.unmount());root=createRoot(host);await mount(recovery);
  expect(host.textContent).toContain("注册结果未确认");expect(host.querySelector('button[type="submit"]')).toBeNull();expect(host.innerHTML).not.toContain("Original123");expect(mocks.register).not.toHaveBeenCalled();await act(async()=>recovery.dispose());
 });
 it("does not send an OTP or register when a slow config resolves after back while the component is still mounted",async()=>{
  let finish!:(value:unknown)=>void;mocks.config.mockImplementation(()=>new Promise(resolve=>{finish=resolve;}));
  const recovery=new RegistrationRecovery();await mount(recovery);await fill("toast.inputEmail","synthetic@example.test");await click(host.querySelector('button[type="submit"]')!);await until(()=>mocks.config.mock.calls.length===1);
  await click(button("placeholder.getBack"));expect(mocks.back).toHaveBeenCalledWith(0);
  await act(async()=>{finish({data:{config:{needVerificationCodeRegister:"1"}}});await new Promise(resolve=>setTimeout(resolve,0));});
  expect(mocks.send).not.toHaveBeenCalled();expect(mocks.register).not.toHaveBeenCalled();expect(recovery.hasAttempt()).toBe(false);
 });
});
