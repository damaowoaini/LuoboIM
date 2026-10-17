import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SessionCoordinator, type Session } from "./session";

const f = vi.hoisted(() => ({ issuer: "http://127.0.0.1:7785", current: undefined as Session | undefined }));
vi.mock("./browser", () => ({ sessionCoordinator: { get current() { return f.current; } } }));
const origin = "http://127.0.0.1:7785";
const canonical = "https://objects.luobo.fixture.test/luobo-stack/synthetic%2Fimage.png?X-Amz-Signature=synthetic%2Bvalue&partNumber=1";
const reply = (value: unknown) => new Response(JSON.stringify(value));
let endpoints: typeof import("./devEndpoints");
let transport: typeof import("./devObjectTransport");
beforeEach(async () => {
  vi.resetModules(); f.issuer = origin;
  vi.stubGlobal("window", { location: { origin }, fetch: vi.fn(async () => new Response(null, { status: 404 })) });
  vi.stubGlobal("localStorage", { getItem: () => f.issuer });
  const coordinator = new SessionCoordinator({ read: async () => undefined, write: async () => {}, clear: async () => {} });
  await coordinator.commit({ issuer: origin, userID: "synthetic-user", chatToken: "synthetic-chat", imToken: "synthetic-im",
    serverInfo: { serverID: "fixture-tenant", serverName: "synthetic", domains: ["luobo.fixture.test"] } });
  f.current = coordinator.current;
  endpoints = await import("./devEndpoints"); transport = await import("./devObjectTransport");
});
afterEach(() => vi.unstubAllGlobals());
const installMapping = () => endpoints.initializeDevEndpoints(async () => reply({ version: 1, issuer: origin,
  serverID: "fixture-tenant", domain: "luobo.fixture.test" }));

describe("explicit fixture object transport", () => {
  it("maps only the two canonical HTTPS paths without changing source URLs or binding to a QA port", () => {
    for (const current of [origin, "http://127.0.0.1:7786", "http://127.0.0.1:7778", "http://localhost:7778"])
      expect(transport.mapDevObjectTransport(canonical, current)).toBe(current + "/__luobo_dev_objects__/storage" + canonical.slice("https://objects.luobo.fixture.test".length));
    expect(transport.mapDevObjectTransport("https://luobo.fixture.test/api/object/synthetic%2Ffile?x=%2B", origin))
      .toBe(origin + "/__luobo_dev_objects__/alias/api/object/synthetic%2Ffile?x=%2B");
    for (const raw of ["http://objects.luobo.fixture.test/luobo-stack/file", "https://objects.luobo.fixture.test.evil/luobo-stack/file",
      "https://objects.luobo.fixture.test:34443/luobo-stack/file", "https://luobo.fixture.test/api/other/file",
      "https://objects.luobo.fixture.test/luobo-stack/%2e%2e/file", "https://objects.luobo.fixture.test/luobo-stack/file#x"])
      expect(transport.mapDevObjectTransport(raw, origin)).toBeUndefined();
    expect(transport.mapDevObjectTransport(canonical, "https://app.example")).toBeUndefined();
    expect(canonical).toBe("https://objects.luobo.fixture.test/luobo-stack/synthetic%2Fimage.png?X-Amz-Signature=synthetic%2Bvalue&partNumber=1");
  });

  it("requires the explicit bounded anonymous capability and current validated tenant issuer mapping", async () => {
    const absent = vi.fn(async () => reply({ version: 1, issuer: origin, profile: "not-authorized" }));
    window.fetch = absent;
    await transport.initializeDevObjectTransport();
    expect(window.fetch).toBe(absent);
    expect(transport.devObjectTransportURL(canonical)).toBe(canonical);
    const original = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => reply({ version: 1, issuer: origin, profile: "luobo-task-objects-v1" }));
    window.fetch = original;
    await transport.initializeDevObjectTransport();
    expect(original.mock.calls[0][0]).toBe(origin + "/__luobo_dev__/object-transport");
    expect(original.mock.calls[0][1]).toEqual(expect.objectContaining({ credentials: "omit", redirect: "error", cache: "no-store" }));
    expect(original.mock.calls[0][1]).not.toHaveProperty("headers");
    expect(transport.devObjectTransportURL(canonical)).toBe(canonical);
    await installMapping();
    expect(transport.devObjectTransportURL(canonical)).not.toBe(canonical);
    const selected = f.current!;
    for (const changed of [undefined, { ...selected, issuer: "http://localhost:7785" },
      { ...selected, tenant: { ...selected.tenant, serverID: "other-tenant" } },
      { ...selected, tenant: { ...selected.tenant, domains: ["other.example"] } }]) {
      f.current = changed; expect(transport.devObjectTransportURL(canonical)).toBe(canonical);
    }
    f.current = selected; f.issuer = "https://other.example";
    expect(transport.devObjectTransportURL(canonical)).toBe(canonical);
  });

  it("forwards only eligible fetch strings or URLs while preserving Request objects and ordinary failures", async () => {
    await installMapping();
    const calls: { input: RequestInfo | URL; init?: RequestInit }[] = [];
    const ordinaryError = new Error("synthetic ordinary fetch failure");
    const original = vi.fn((input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      calls.push({ input, init });
      if (input === origin + "/__luobo_dev__/object-transport") return Promise.resolve(reply({ version: 1, issuer: origin, profile: "luobo-task-objects-v1" }));
      if (input === "https://ordinary.example/failure") return Promise.reject(ordinaryError);
      return Promise.resolve(new Response(null, { status: 204 }));
    });
    window.fetch = original; await transport.initializeDevObjectTransport();
    const body = new Uint8Array([1, 2, 3]), headers = new Headers({ Authorization: "synthetic-signed-header" });
    const init: RequestInit = { method: "PUT", headers, body, credentials: "include" };
    await window.fetch(canonical, init);
    const mapped = calls[calls.length - 1];
    expect(mapped.input).toBe(transport.mapDevObjectTransport(canonical, origin));
    expect(mapped.init).toEqual({ ...init, credentials: "omit" });
    expect(mapped.init!.body).toBe(body); expect(mapped.init!.headers).toBe(headers);
    expect(init.credentials).toBe("include");
    await window.fetch(new URL(canonical));
    expect(calls[calls.length - 1].input).toBe(mapped.input);
    const request = new Request(canonical);
    await window.fetch(request, init);
    expect(calls[calls.length - 1]).toEqual({ input: request, init });
    expect(calls[calls.length - 1].input).toBe(request);
    await expect(window.fetch("https://ordinary.example/failure")).rejects.toBe(ordinaryError);
    expect(calls[calls.length - 1].input).toBe("https://ordinary.example/failure");
    f.current = undefined; await window.fetch(canonical, init);
    expect(calls[calls.length - 1]).toEqual({ input: canonical, init });
  });
});
