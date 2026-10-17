import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AxiosAdapter } from "axios";
import { SessionCoordinator, StaleSessionError, endpoints, type Session } from "./session";

const f = vi.hoisted(() => ({ issuer: "http://127.0.0.1:7778", session: undefined as SessionCoordinator | undefined }));
vi.mock("./browser", () => ({ get sessionCoordinator() { return f.session; } }));
import { getApiUrl, getChatUrl, getWsUrl } from "@/config";
import createAxiosInstance from "@/utils/request";
import { revokeCurrentSession } from "./logout";
import {
  browserEndpoints, decodeDevEndpointMapping, DEV_ENDPOINT_BOOTSTRAP_PATH,
  initializeDevEndpoints, resolveDevEndpoints, browserSdkLoopbackOptIn, resolveDevSdkLoopbackOptIn,
} from "./devEndpoints";

const origin = "http://127.0.0.1:7778";
const bootstrap = { version: 1, issuer: origin, serverID: "fixture-tenant", domain: "luobo.fixture.test" };
const auth = (tenant = "fixture-tenant", domain = "luobo.fixture.test", user = "synthetic-user") => ({
  issuer: f.issuer, userID: user, chatToken: `synthetic-chat-${user}`, imToken: `synthetic-im-${user}`,
  serverInfo: { serverID: tenant, serverName: "synthetic", domains: [domain] },
});
const reply = (value: unknown) => new Response(JSON.stringify(value), { headers: { "Content-Type": "application/json" } });
const install = () => initializeDevEndpoints(vi.fn(async () => reply(bootstrap)));

beforeEach(async () => {
  f.issuer = origin;
  vi.stubGlobal("window", { location: { origin } });
  vi.stubGlobal("localStorage", { getItem: () => f.issuer });
  f.session = new SessionCoordinator({ read: async () => undefined, write: async () => {}, clear: async () => {} });
  await initializeDevEndpoints(vi.fn(async () => new Response(null, { status: 404 })));
});
afterEach(() => vi.unstubAllGlobals());

describe("explicit loopback tenant transport", () => {
  it("keeps ordinary HTTPS/443 endpoints and avoids bootstrap requests outside its exact loopback issuer", async () => {
    await f.session!.commit(auth());
    const original = endpoints(f.session!.current!);
    expect(browserEndpoints(f.session!.current!)).toEqual(original);
    const send = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => reply(bootstrap));
    f.issuer = "https://route.example";
    await initializeDevEndpoints(send);
    expect(send).not.toHaveBeenCalled();
    vi.stubGlobal("window", { location: { origin: "https://app.example" } });
    f.issuer = "https://app.example";
    await initializeDevEndpoints(send);
    expect(send).not.toHaveBeenCalled();
    expect(browserEndpoints(f.session!.current!)).toEqual(original);
  });

  it("maps only the original issuer, tenant and active raw domain without changing persisted ownership", async () => {
    await f.session!.commit(auth());
    const session = f.session!.current!, raw = JSON.stringify(session), namespace = f.session!.namespace;
    const selected = decodeDevEndpointMapping(bootstrap, { origin, issuer: origin })!;
    expect(resolveDevEndpoints(session, { origin, issuer: origin }, selected)).toEqual({
      chat: `${origin}/chat`, im: `${origin}/im`, ws: "ws://127.0.0.1:7778/msg_gateway",
    });
    for (const changed of [
      { ...session, issuer: "http://localhost:7778" },
      { ...session, tenant: { ...session.tenant, serverID: "fixture-tenant-b" } },
      { ...session, tenant: { ...session.tenant, domains: ["tenant-b.luobo.fixture.test"] } },
      { ...session, tenant: { ...session.tenant, domains: [...session.tenant.domains, "backup.example"], activeLine: 1 } },
    ]) expect(resolveDevEndpoints(changed, { origin, issuer: origin }, selected)).toEqual(endpoints(changed));
    expect(resolveDevEndpoints(session, { origin: "http://localhost:7778", issuer: origin }, selected)).toEqual(endpoints(session));
    expect(resolveDevEndpoints(session, { origin, issuer: "https://route.example" }, selected)).toEqual(endpoints(session));
    expect(JSON.stringify(session)).toBe(raw);
    expect(f.session!.namespace).toBe(namespace);
    const secure = "https://localhost:7778";
    const secureMapping = decodeDevEndpointMapping({ ...bootstrap, issuer: secure }, { origin: secure, issuer: secure })!;
    expect(resolveDevEndpoints({ ...session, issuer: secure }, { origin: secure, issuer: secure }, secureMapping).ws)
      .toBe("wss://localhost:7778/msg_gateway");
  });

  it("fetches only bounded anonymous same-origin deployment data and rejects malformed mappings", async () => {
    await f.session!.commit(auth());
    const send = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => reply(bootstrap));
    await initializeDevEndpoints(send);
    expect(send).toHaveBeenCalledWith(`${origin}${DEV_ENDPOINT_BOOTSTRAP_PATH}`, expect.objectContaining({
      method: "GET", credentials: "omit", redirect: "error", cache: "no-store",
    }));
    expect(send.mock.calls[0][1]).not.toHaveProperty("headers");
    expect(browserEndpoints(f.session!.current!).im).toBe(`${origin}/im`);
    for (const value of [null, [], {}, { ...bootstrap, version: 2 }, { ...bootstrap, issuer: "https://other.example" },
      { ...bootstrap, domain: "other.example/path" }, { ...bootstrap, domain: "luobo.fixture.test:34443" },
      { ...bootstrap, serverID: "*" , endpoints: { im: "http://other.example" } }]) {
      await initializeDevEndpoints(vi.fn(async () => reply(value)));
      expect(browserEndpoints(f.session!.current!)).toEqual(endpoints(f.session!.current!));
    }
    await initializeDevEndpoints(vi.fn(async () => new Response(" ".repeat(8193))));
    expect(browserEndpoints(f.session!.current!)).toEqual(endpoints(f.session!.current!));
    await initializeDevEndpoints(vi.fn(async () => { throw new Error("synthetic offline"); }));
    expect(browserEndpoints(f.session!.current!)).toEqual(endpoints(f.session!.current!));
  });

  it("does not install an old bootstrap response after a service-setting change", async () => {
    await f.session!.commit(auth());
    let complete!: (response: Response) => void;
    const started = initializeDevEndpoints(() => new Promise((resolve) => { complete = resolve; }));
    f.issuer = "https://route.example";
    complete(reply(bootstrap));
    await started;
    f.issuer = origin;
    expect(browserEndpoints(f.session!.current!)).toEqual(endpoints(f.session!.current!));
  });

  it("uses the real HTTP token and lease boundary across a different tenant while retaining SDK endpoint getters", async () => {
    await install();
    await f.session!.commit(auth());
    expect([getChatUrl(), getApiUrl(), getWsUrl()]).toEqual([`${origin}/chat`, `${origin}/im`, "ws://127.0.0.1:7778/msg_gateway"]);
    const requests: { base: string | undefined; token: unknown }[] = [];
    let finish!: () => void, entered!: () => void;
    const ready = new Promise<void>((resolve) => { entered = resolve; });
    const adapter: AxiosAdapter = async (config) => {
      requests.push({ base: config.baseURL, token: config.headers.token });
      if (requests.length === 1) { entered(); await new Promise<void>((resolve) => { finish = resolve; }); }
      return { config, status: 200, statusText: "OK", headers: {}, data: { errCode: 0, data: {} } };
    };
    const request = createAxiosInstance(getApiUrl, "im", f.session!);
    request.defaults.adapter = adapter;
    const pending = request.post("/user/get_users_info", {});
    await ready;
    await f.session!.commit(auth("fixture-tenant-b", "tenant-b.luobo.fixture.test", "second"));
    finish();
    await expect(pending).rejects.toBeInstanceOf(StaleSessionError);
    await request.post("/user/get_users_info", {});
    expect(requests).toEqual([
      { base: `${origin}/im`, token: "synthetic-im-synthetic-user" },
      { base: "https://tenant-b.luobo.fixture.test/api", token: "synthetic-im-second" },
    ]);
  });

  it("revokes the captured tenant through the same adapter without using a replacement account token", async () => {
    await install();
    await f.session!.commit(auth());
    const captured: Session = f.session!.current!;
    await f.session!.commit(auth("fixture-tenant-b", "tenant-b.luobo.fixture.test", "second"));
    const calls: { url: RequestInfo | URL; init?: RequestInit }[] = [];
    const send: typeof fetch = async (url, init) => {
      calls.push({ url, init });
      return reply({ errCode: 0, data: { revoked: true, sessionClosed: true, pending: false } });
    };
    await expect(revokeCurrentSession(captured, new AbortController().signal, send)).resolves.toBe("closed");
    expect(calls).toEqual([{ url: `${origin}/chat/account/logout`, init: expect.objectContaining({
      headers: expect.objectContaining({ token: "synthetic-chat-synthetic-user" }),
      body: JSON.stringify({ imToken: "synthetic-im-synthetic-user" }), credentials: "omit", redirect: "error",
    }) }]);
    expect(f.session!.current!.userID).toBe("second");
  });
});


describe("controlled SDK initialization transport", () => {
  it("opts in only for an exact validated HTTP loopback issuer tenant and active domain", async () => {
    await install();
    await f.session!.commit(auth());
    const session = f.session!.current!;
    expect(browserSdkLoopbackOptIn(session)).toBe(true);
    const selected = decodeDevEndpointMapping(bootstrap, { origin, issuer: origin })!;
    for (const changed of [
      { ...session, issuer: "http://localhost:7778" },
      { ...session, tenant: { ...session.tenant, serverID: "foreign-tenant" } },
      { ...session, tenant: { ...session.tenant, domains: ["foreign.example"] } },
      { ...session, tenant: { ...session.tenant, domains: [...session.tenant.domains, "backup.example"], activeLine: 1 } },
    ]) expect(resolveDevSdkLoopbackOptIn(changed, { origin, issuer: origin }, selected)).toBe(false);
    expect(resolveDevSdkLoopbackOptIn(session, { origin, issuer: "https://route.example" }, selected)).toBe(false);
    expect(resolveDevSdkLoopbackOptIn(session, { origin, issuer: origin })).toBe(false);
    for (const alternate of ["http://localhost:7781", "https://localhost:7781", "http://[::1]:7781", "http://remote.example"]) {
      const context = { origin: alternate, issuer: alternate };
      const mapping = decodeDevEndpointMapping({ ...bootstrap, issuer: alternate }, context);
      expect(resolveDevSdkLoopbackOptIn({ ...session, issuer: alternate }, context, mapping)).toBe(alternate === "http://localhost:7781");
    }
  });

  it("does not grant SDK opt-in from an absent bootstrap or a changed current issuer", async () => {
    await f.session!.commit(auth());
    expect(browserSdkLoopbackOptIn(f.session!.current!)).toBe(false);
    await install();
    expect(browserSdkLoopbackOptIn(f.session!.current!)).toBe(true);
    f.issuer = "https://route.example";
    expect(browserSdkLoopbackOptIn(f.session!.current!)).toBe(false);
    f.issuer = origin;
    vi.stubGlobal("window", { location: { origin: "http://localhost:7778" } });
    expect(browserSdkLoopbackOptIn(f.session!.current!)).toBe(false);
  });
});
