// @vitest-environment jsdom
// Real AntD Form and disclosure; only transport/router/persistence boundaries
// are synthetic. No service login, MFA authority or layout success is claimed.
import React, { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const f = vi.hoisted(() => ({ login: vi.fn(), send: vi.fn(), clearFailure: vi.fn(), navigate: vi.fn(), generation: 1 }));
vi.mock("@/api/login", () => ({
  useLogin: () => ({ mutate: f.login, isPending: false }),
  useSendSms: () => ({ mutate: f.send, isPending: false }),
}));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: { get generation() { return f.generation; } } }));
vi.mock("@/core/session/loginFailure", () => ({ clearSdkLoginFailure: f.clearFailure }));
vi.mock("react-router-dom", () => ({
  useNavigate: () => f.navigate,
  useSearchParams: () => [new URLSearchParams(), vi.fn()],
}));
vi.mock("@/utils/storage", () => ({
  getEmail: () => "", getPhoneNumber: () => "", setAreaCode: vi.fn(),
  setEmail: vi.fn(), setPhoneNumber: vi.fn(), setIMProfile: vi.fn(),
}));
vi.mock("i18next", () => {
  const words: Record<string, string> = {
  "placeholder.welcome": "欢迎使用萝卜对话", "placeholder.phoneNumber": "手机号",
  "placeholder.email": "邮箱", "placeholder.password": "密码", "placeholder.verifyCode": "验证码",
  "placeholder.login": "登录", "placeholder.forgetPassword": "忘记密码",
  "placeholder.registerToast": "还没有注册？", "placeholder.toRegister": "立即注册",
  "placeholder.sendVerifyCode": "获取验证码", "toast.inputPhoneNumber": "请输入手机号",
  "toast.inputEmail": "请输入邮箱", "toast.inputPassword": "请输入密码", "toast.inputVerifyCode": "请输入验证码",
};
  return { t: (key: string) => words[key] ?? key };
});

import LoginForm from "./LoginForm";
import { ApiError, HttpApiError, TransportApiError } from "@/utils/request";

let root: Root | undefined;
let host: HTMLDivElement;
let matchMediaBefore: PropertyDescriptor | undefined;
const tick = () => new Promise<void>(resolve => setTimeout(resolve, 0));
function input(selector: string) {
  const field = host.querySelector<HTMLInputElement>(selector);
  expect(field).not.toBeNull();
  return field!;
}
async function enter(field: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(field, value);
    field.dispatchEvent(new Event("input", { bubbles: true }));
    await tick();
  });
}
async function clickText(selector: string, text: string) {
  const elements = [...host.querySelectorAll<HTMLElement>(selector)].filter(e => e.textContent === text);
  expect(elements).toHaveLength(1);
  await act(async () => { elements[0].click(); await tick(); });
}
async function submit() {
  await act(async () => {
    host.querySelector<HTMLButtonElement>('button[type="submit"]')!.focus();
    host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await tick();
  });
}
async function until(predicate: () => boolean) {
  const deadline = Date.now() + 2000;
  while (!predicate() && Date.now() < deadline) {
    await act(async () => { await new Promise<void>(resolve => setTimeout(resolve, 20)); });
  }
  expect(predicate()).toBe(true);
}
function Harness() {
  const [method, setMethod] = useState<"phone" | "email">("phone");
  return <LoginForm loginMethod={method} updateLoginMethod={setMethod} setFormType={() => {}} />;
}

beforeEach(() => {
  vi.clearAllMocks(); f.generation = 1;
  localStorage.clear(); localStorage.setItem("luobo.route.url", "https://issuer.example");
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  matchMediaBefore = Object.getOwnPropertyDescriptor(window, "matchMedia");
  Object.defineProperty(window, "matchMedia", { configurable: true, writable: true,
    value: vi.fn((media: string) => ({
      matches: false, media, onchange: null, addListener() {}, removeListener() {},
      addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
    })),
  });
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  host = document.createElement("div"); document.body.append(host);
});
afterEach(async () => {
  if (root) await act(async () => { root!.unmount(); });
  root = undefined; host.remove();
  if (matchMediaBefore) Object.defineProperty(window, "matchMedia", matchMediaBefore);
  else Reflect.deleteProperty(window, "matchMedia");
  vi.unstubAllGlobals(); localStorage.clear();
});

describe("LoginForm authenticator disclosure", () => {
  it("reveals an invalid folded MFA field and preserves values without weakening existing mode clearing", async () => {
    root = createRoot(host);
    await act(async () => { root!.render(<Harness />); await tick(); });
    const disclosure = host.querySelector("details")!;
    expect(disclosure.open).toBe(false);
    await enter(input('input[aria-label="手机号"]'), "13800138000");
    await enter(input('input[placeholder="请输入密码"]'), "synthetic-password");
    disclosure.open = true;
    await enter(input('input[aria-label="验证器动态码"]'), "12");
    disclosure.open = false;
    await submit();
    await until(() => disclosure.open && document.activeElement === input('input[aria-label="验证器动态码"]') && host.textContent?.includes("请输入6位验证器动态码") === true);
    expect(host.textContent).toContain("请输入6位验证器动态码");
    expect(input('input[aria-label="验证器动态码"]').value).toBe("12");
    expect(f.login).not.toHaveBeenCalled();

    await enter(input('input[aria-label="验证器动态码"]'), "654321");
    disclosure.open = false;
    await clickText("button", "验证码登录");
    expect(input('input[aria-label="验证码"]').value).toBe("");
    await enter(input('input[aria-label="验证码"]'), "123456");
    await clickText('[role="tab"]', "邮箱");
    await enter(input('input[placeholder="请输入邮箱"]'), "synthetic@example.test");
    await clickText('[role="tab"]', "手机号");
    expect(input('input[aria-label="手机号"]').value).toBe("13800138000");
    expect(input('input[aria-label="验证器动态码"]').value).toBe("654321");
    await clickText("button", "密码登录");
    expect(input('input[placeholder="请输入密码"]').value).toBe("");
    await clickText("button", "验证码登录");
    expect(input('input[aria-label="验证码"]').value).toBe("");
    await enter(input('input[aria-label="验证码"]'), "123456");
    expect(disclosure.open).toBe(false);
    await submit();
    await until(() => f.login.mock.calls.length === 1);
    const sent = f.login.mock.calls[0][0] as API.Login.LoginParams;
    expect(sent).toMatchObject({ phoneNumber: "13800138000", areaCode: "+86", verifyCode: "123456", googleAuthCode: "654321" });
    expect(sent).not.toHaveProperty("password");
    expect(sent).not.toHaveProperty("email");
    expect(f.clearFailure).toHaveBeenCalledTimes(1);
    expect(f.send).not.toHaveBeenCalled();

    const missing = "该账户已启用谷歌验证码，请输入验证码: 1001 ArgsError";
    const wrong = "谷歌验证码错误: 1001 ArgsError";
    const reject = async (index: number, error: unknown) => {
      const callbacks = f.login.mock.calls[index][1] as { onError(error: unknown): void };
      await act(async () => { callbacks.onError(error); await tick(); });
    };
    for (const ordinary of [
      new ApiError(20001, "账号或密码错误"), new ApiError(1001, "密码或验证码必须设置其中一个: 1001 ArgsError"),
      new ApiError(1001, "必须提供区号: 1001 ArgsError"), new ApiError(1001, "谷歌验证码错误"),
      new HttpApiError(401, wrong), new TransportApiError("connection"),
      { name: "ApiError", errCode: 1001, errDlt: wrong },
    ]) { disclosure.open = false; await reject(0, ordinary); expect(disclosure.open).toBe(false); }
    await reject(0, new ApiError(1001, wrong));
    expect(disclosure.open).toBe(true);
    expect(document.activeElement).toBe(input('input[aria-label="验证器动态码"]'));
    expect(f.login).toHaveBeenCalledTimes(1);

    // A -> B -> A is stale even though the visible identity returns to A.
    disclosure.open = false;
    await enter(input('input[aria-label="手机号"]'), "13900139000");
    await enter(input('input[aria-label="手机号"]'), "13800138000");
    await reject(0, new ApiError(1001, wrong)); expect(disclosure.open).toBe(false);
    await submit(); await until(() => f.login.mock.calls.length === 2);
    await clickText("button", "密码登录"); await clickText("button", "验证码登录");
    await enter(input('input[aria-label="验证码"]'), "123456");
    await reject(1, new ApiError(1001, missing)); expect(disclosure.open).toBe(false);

    // A newer equal-value submission retires the old callback as well.
    await submit(); await until(() => f.login.mock.calls.length === 3);
    await submit(); await until(() => f.login.mock.calls.length === 4);
    await reject(2, new ApiError(1001, wrong)); expect(disclosure.open).toBe(false);
    await reject(3, new ApiError(1001, wrong)); expect(disclosure.open).toBe(true);
    disclosure.open = false;
    localStorage.setItem("luobo.route.url", "https://other-issuer.example");
    await reject(3, new ApiError(1001, wrong)); expect(disclosure.open).toBe(false);
    localStorage.setItem("luobo.route.url", "https://issuer.example"); f.generation++;
    await reject(3, new ApiError(1001, wrong)); expect(disclosure.open).toBe(false);

    // Blank remains server-authoritative: no local forced-MFA bypass or retry.
    await enter(input('input[aria-label="验证器动态码"]'), "");
    await submit(); await until(() => f.login.mock.calls.length === 5);
    expect(f.login.mock.calls[4][0].googleAuthCode).toBe("");
    await reject(4, new ApiError(1001, missing)); expect(disclosure.open).toBe(true);
    expect(document.activeElement).toBe(input('input[aria-label="验证器动态码"]'));
    expect(input('input[aria-label="手机号"]').value).toBe("13800138000");
    expect(input('input[aria-label="验证码"]').value).toBe("123456");
    expect(f.login).toHaveBeenCalledTimes(5);
    disclosure.open = false;
    await act(async () => { root!.unmount(); }); root = undefined;
    await reject(4, new ApiError(1001, missing)); expect(disclosure.open).toBe(false);
    expect(f.login).toHaveBeenCalledTimes(5);
  });
});
