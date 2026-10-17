import { describe, expect, it, vi } from "vitest";
import { AxiosAdapter, AxiosHeaders } from "axios";
import { endpoints, SessionCoordinator, StaleSessionError } from "@/core/session/session";
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: undefined }));
import createAxiosInstance, { ApiError, HttpApiError, TransportApiError } from "./request";
const response = (id = "u", host = "tenant.example") => ({ issuer: "https://route.example", userID: id, imToken: `im-${id}`, chatToken: `chat-${id}`, serverInfo: { serverID: host, serverName: "tenant", domains: [host] } });
const coordinator = () => new SessionCoordinator({ read: async () => undefined, write: async () => {}, clear: async () => {} });
const adapter: AxiosAdapter = async config => ({ config, data: { errCode: 0, data: { baseURL: config.baseURL, token: config.headers.token } }, headers: new AxiosHeaders(), status: 200, statusText: "OK" });
describe("Tenant HTTP boundary", () => {
  it("retains only strict registration outcome booleans from failed envelopes",async()=>{
    const session=coordinator();const request=createAxiosInstance("https://route.example","none",session);
    request.defaults.adapter=async config=>({config,data:{errCode:1002,errMsg:"pending",data:{registrationPending:true,imToken:"must-not-escape",password:"must-not-escape"}},headers:new AxiosHeaders(),status:200,statusText:"OK"});
    try {await request.post("/api/auth/register");throw new Error("expected failure");}catch(error){
      expect(error).toBeInstanceOf(ApiError);expect(error).toMatchObject({errCode:1002,registration:{registrationPending:true}});
      expect(JSON.stringify(error)).not.toContain("must-not-escape");expect(error).not.toHaveProperty("config");expect(error).not.toHaveProperty("data");
      expect(error).not.toBeInstanceOf(HttpApiError);
    }
    const outcomes = [
      { data: { registrationPending: true, requiresLogin: true }, registration: { registrationPending: true, requiresLogin: true } },
      { data: { registrationRecovered: true, requiresLogin: true }, registration: { registrationRecovered: true, requiresLogin: true } },
      { data: { registrationPending: "true", registrationRecovered: 1, requiresLogin: 1 }, registration: {} },
      { data: { registrationPending: true, registrationRecovered: true, requiresLogin: true }, registration: {} },
    ];
    for (const status of [502, 503, 504]) for (const outcome of outcomes) {
      request.defaults.adapter = async config => { throw Object.assign(new Error("must-not-escape"), {
        config, code: "ERR_BAD_RESPONSE", response: { status, data: { data: {
          ...outcome.data, imToken: "must-not-escape", password: "must-not-escape", upstreamURL: "must-not-escape",
        } } },
      }); };
      const error: unknown = await request.post("/api/auth/register").catch(value => value);
      expect(error).toBeInstanceOf(ApiError); expect(error).toBeInstanceOf(HttpApiError);
      expect(error).not.toBeInstanceOf(TransportApiError);
      expect(error).toMatchObject({ name: "ApiError", errCode: status, httpStatus: status,
        errDlt: "网络连接失败，请重试", message: "网络连接失败，请重试" });
      expect((error as ApiError).registration).toEqual(outcome.registration);
      expect(JSON.stringify(error)).not.toContain("must-not-escape");
      for (const field of ["config", "data", "response", "cause"]) expect(error).not.toHaveProperty(field);
    }
  });
  it("resolves the active tenant and correct token on every request, replacing caller tokens", async () => {
    const session = coordinator(); await session.commit(response());
    const im = createAxiosInstance(() => endpoints(session.current!).im, "im", session);
    im.defaults.adapter = adapter;
    expect((await im.post("/message/search", {}, { headers: { token: "stale" } })).data).toEqual({ baseURL: "https://tenant.example/api", token: "im-u" });
    await session.commit(response("second", "second.example"));
    expect((await im.post("/message/search")).data).toEqual({ baseURL: "https://second.example/api", token: "im-second" });
    const chart = createAxiosInstance(() => endpoints(session.current!).chat, "chat", session); chart.defaults.adapter = adapter;
    expect((await chart.post("/account/info")).data.token).toBe("chat-second");
  });
  it("never sends credentials through anonymous auth/config requests", async () => {
    const session = coordinator(); await session.commit(response());
    const anonymous = createAxiosInstance("https://route.example", "none", session); anonymous.defaults.adapter = adapter;
    expect((await anonymous.post("/api/client_config/get", {}, { headers: { token: "must-remove" } })).data.token).toBeUndefined();
  });
  it("rejects cross-origin and protocol-relative requests before a credentialed adapter runs", async () => {
    const session = coordinator(); await session.commit(response()); const spy = vi.fn(adapter);
    const request = createAxiosInstance("https://tenant.example/api", "im", session); request.defaults.adapter = spy;
    await expect(request.post("https://other.example/upload")).rejects.toMatchObject({ errCode: 400 });
    await expect(request.post("//other.example/upload")).rejects.toMatchObject({ errCode: 400 });
    expect(spy).not.toHaveBeenCalled();
  });
  it("drops a response which arrives after account replacement", async () => {
    const session = coordinator(); await session.commit(response());
    let finish!: () => void; let started!: () => void; const ready = new Promise<void>(resolve => { started = resolve; });
    const request = createAxiosInstance("https://tenant.example/api", "im", session);
    request.defaults.adapter = async config => { started(); await new Promise<void>(resolve => { finish = resolve; }); return { config, data: { errCode: 0, data: "old account" }, headers: new AxiosHeaders(), status: 200, statusText: "OK" }; };
    const pending = request.post("/message/search"); await ready; await session.commit(response("new")); finish();
    await expect(pending).rejects.toThrow(StaleSessionError);
  });
  it("preserves actionable local errors and hides Axios configs containing credentials", async () => {
    const session = coordinator(); const request = createAxiosInstance("", "im", session);
    await expect(request.post("/account")).rejects.toMatchObject({ errCode: 401 });
    await session.commit(response()); const network = createAxiosInstance("https://tenant.example/api", "im", session);
    network.defaults.adapter = async config => { throw Object.assign(new Error("sensitive detail"), { config, code: "ECONNABORTED" }); };
    try { await network.post("/message/search"); throw new Error("expected failure"); } catch (error) { expect(error).toBeInstanceOf(ApiError); expect(error).toBeInstanceOf(TransportApiError); expect(error).toMatchObject({ errCode: 503, message: "请求超时，请重试", transportKind: "timeout" }); expect(error).not.toHaveProperty("config"); }
    for (const code of ["ETIMEDOUT", "ERR_NETWORK"]) {
      network.defaults.adapter = async config => { throw Object.assign(new Error("sensitive detail"), { config, code }); };
      const error: unknown = await network.post("/message/search").catch(value => value);
      expect(error).toBeInstanceOf(TransportApiError);
      expect(error).toMatchObject({ transportKind: code === "ERR_NETWORK" ? "connection" : "timeout" });
      expect(error).not.toHaveProperty("config");
      expect(JSON.stringify(error)).not.toContain("sensitive detail");
    }
    network.defaults.adapter = async config => ({ config, data: { errCode: 503, errMsg: "请求超时，请重试" }, headers: new AxiosHeaders(), status: 200, statusText: "OK" });
    const business: unknown = await network.post("/message/search").catch(value => value);
    expect(business).toBeInstanceOf(ApiError); expect(business).not.toBeInstanceOf(TransportApiError);
    expect(business).toMatchObject({ errCode: 503, message: "请求超时，请重试" });
    expect(business).not.toBeInstanceOf(HttpApiError);
    network.defaults.adapter = async config => ({ config, data: { errCode: 502, errMsg: "synthetic business rejection" }, headers: new AxiosHeaders(), status: 200, statusText: "OK" });
    const business502: unknown = await network.post("/message/search").catch(value => value);
    expect(business502).toBeInstanceOf(ApiError); expect(business502).not.toBeInstanceOf(HttpApiError);
    expect(business502).not.toBeInstanceOf(TransportApiError);
    expect(business502).toMatchObject({ errCode: 502, errDlt: "synthetic business rejection", message: "synthetic business rejection" });
    network.defaults.adapter = async config => { throw Object.assign(new Error("sensitive server detail"), { config, code: "ERR_BAD_RESPONSE", response: { status: 500 } }); };
    const http: unknown = await network.post("/message/search").catch(value => value);
    expect(http).toBeInstanceOf(ApiError); expect(http).not.toBeInstanceOf(TransportApiError);
    expect(http).toMatchObject({ errCode: 500 }); expect(http).not.toHaveProperty("config");
    expect(http).toBeInstanceOf(HttpApiError); expect(http).toMatchObject({ name: "ApiError", httpStatus: 500 });
    for (const status of [401, 502, 503, 504]) {
      network.defaults.adapter = async config => { throw Object.assign(new Error("sensitive server detail"), {
        config, code: "ERR_BAD_RESPONSE", response: { status },
      }); };
      const responseError: unknown = await network.post("/message/search").catch(value => value);
      expect(responseError).toBeInstanceOf(ApiError); expect(responseError).toBeInstanceOf(HttpApiError);
      expect(responseError).not.toBeInstanceOf(TransportApiError);
      expect(responseError).toMatchObject({ name: "ApiError", errCode: status, httpStatus: status,
        errDlt: "网络连接失败，请重试", message: "网络连接失败，请重试", registration: {} });
      expect(responseError).not.toHaveProperty("config"); expect(responseError).not.toHaveProperty("response");
      expect(JSON.stringify(responseError)).not.toContain("sensitive server detail");
    }
    network.defaults.adapter = async config => { throw Object.assign(new Error("sensitive server detail"), { config, code: "ERR_BAD_RESPONSE" }); };
    const noResponse: unknown = await network.post("/message/search").catch(value => value);
    expect(noResponse).toBeInstanceOf(ApiError); expect(noResponse).not.toBeInstanceOf(HttpApiError);
    expect(noResponse).not.toBeInstanceOf(TransportApiError); expect(noResponse).toMatchObject({ errCode: 503 });
  });
});

it("keeps an optional request scope through restore and both response boundaries", async () => {
  function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>(yes => { resolve = yes; });
    return { promise, resolve };
  }
  const read = deferred<unknown>(), restoring = deferred<void>();
  const stored = {
    version: 1, issuer: "https://route.example", userID: "u", imToken: "im-u", chatToken: "chat-u",
    tenant: { serverID: "tenant.example", serverName: "tenant", domains: ["tenant.example"], activeLine: 0 },
  };
  const session = new SessionCoordinator({
    read: async () => { restoring.resolve(undefined); return read.promise; },
    write: async () => {}, clear: async () => {},
  });
  let current = true;
  const guarded: import("./request").GuardedRequestConfig = { luoboCurrent: () => current };
  const request = createAxiosInstance("https://tenant.example/chat", "chat", session);
  const transport = vi.fn(adapter); request.defaults.adapter = transport;
  const blocked = request.post("/user/login_record/latest", { userID: "target" }, guarded);
  const rejected = expect(blocked).rejects.toThrow(StaleSessionError);
  await restoring.promise; current = false; read.resolve(stored); await rejected;
  expect(transport).not.toHaveBeenCalled();

  current = true;
  expect((await request.post("/user/login_record/latest", { userID: "target" }, guarded)).data)
    .toEqual({ baseURL: "https://tenant.example/chat", token: "chat-u" });
  expect(transport).toHaveBeenCalledTimes(1);
  const realLease = session.lease.bind(session), releases: unknown[] = [];
  const leaseSpy = vi.spyOn(session, "lease").mockImplementation(() => {
    const lease = realLease(), release = vi.fn(lease.release); releases.push(release);
    return { ...lease, release };
  });
  try {
    for (const failed of [false, true]) {
      current = true;
      const started = deferred<void>(), finish = deferred<void>();
      request.defaults.adapter = async config => {
        started.resolve(undefined); await finish.promise;
        if (failed) throw Object.assign(new Error("synthetic transport failure"), {
          config, response: { status: 503, data: { data: { ip: "198.51.100.70" } } },
        });
        return adapter(config);
      };
      const pending = request.post("/user/login_record/list", { userID: "target" }, guarded);
      const refused = expect(pending).rejects.toThrow(StaleSessionError);
      await started.promise; current = false; finish.resolve(undefined); await refused;
      expect(releases).toHaveLength(failed ? 2 : 1);
      expect(releases[failed ? 1 : 0]).toHaveBeenCalledTimes(1);
      expect(session.current?.userID).toBe("u");
    }
  } finally {
    leaseSpy.mockRestore();
  }
});
