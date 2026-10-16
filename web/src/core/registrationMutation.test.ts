// @vitest-environment jsdom
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  issuer: "https://route.synthetic.test",
  session: { generation: 0, commit: vi.fn() },
  post: vi.fn(),
  requests: [] as { base: unknown }[],
}));
vi.mock("@/config", () => ({ getChatUrl: () => "", getRouteUrl: () => mocks.issuer }));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: mocks.session }));
vi.mock("@/store", () => ({ useUserStore: vi.fn() }));
vi.mock("@/utils/storage", () => ({ getChatToken: vi.fn() }));
vi.mock("@/api/errorHandle", () => ({ errorHandle: vi.fn() }));
vi.mock("@/utils/request", () => ({ default: (base: unknown) => { mocks.requests.push({ base }); return { post: mocks.post }; } }));
import { useRegister } from "@/api/login";
import { StaleSessionError } from "./session/session";

let host: HTMLDivElement, root: Root, client: QueryClient;
let submit: ReturnType<typeof useRegister>["mutateAsync"];
let active: boolean;
const params = { verifyCode: "123456", invitationCode: "synthetic", autoLogin: true, user: { nickname: "synthetic", faceURL: "", areaCode: "+86", email: "synthetic@example.test", password: "synthetic-wire-value" } };

async function mount() {
  function Probe() { submit = useRegister({ isCurrent: () => active }).mutateAsync; return null; }
  await act(async () => root.render(React.createElement(QueryClientProvider, { client }, React.createElement(Probe))));
}
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  mocks.post.mockReset(); mocks.session.commit.mockReset().mockResolvedValue(undefined); mocks.session.generation = 0;
  mocks.issuer = "https://route.synthetic.test"; mocks.requests.length = 0; active = true;
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  client = new QueryClient({ defaultOptions: { mutations: { retry: 3 } } });
});
afterEach(async () => { await act(async () => root.unmount()); client.clear(); host.remove(); });

describe("registration mutation owner and page boundary", () => {
  it("uses a fixed original issuer and does not commit a response after the page was retired", async () => {
    let finish!: (value: unknown) => void;
    mocks.post.mockImplementation(() => new Promise(resolve => { finish = resolve; })); await mount();
    let pending!: Promise<unknown>; await act(async () => { pending = submit(params); await Promise.resolve(); });
    expect(mocks.requests.at(-1)?.base).toBe("https://route.synthetic.test");
    active = false;
    await act(async () => { finish({ data: { userID: "synthetic", chatToken: "synthetic-chat", imToken: "synthetic-im" } }); await expect(pending).rejects.toBeInstanceOf(StaleSessionError); });
    expect(mocks.post).toHaveBeenCalledOnce(); expect(mocks.session.commit).not.toHaveBeenCalled();
  });
  it("rejects a late response when the configured issuer changed without sending credentials again", async () => {
    let finish!: (value: unknown) => void;
    mocks.post.mockImplementation(() => new Promise(resolve => { finish = resolve; })); await mount();
    let pending!: Promise<unknown>; await act(async () => { pending = submit(params); await Promise.resolve(); });
    mocks.issuer = "https://other.synthetic.test";
    await act(async () => { finish({ data: { userID: "synthetic", chatToken: "synthetic-chat", imToken: "synthetic-im" } }); await expect(pending).rejects.toBeInstanceOf(StaleSessionError); });
    expect(mocks.post).toHaveBeenCalledOnce(); expect(mocks.session.commit).not.toHaveBeenCalled();
  });
  it("overrides a client retry policy so an unknown registration acknowledgement is never retried", async () => {
    mocks.post.mockRejectedValueOnce(new Error("synthetic lost acknowledgement")); await mount();
    await act(async () => { await expect(submit(params)).rejects.toThrow("synthetic lost acknowledgement"); });
    expect(mocks.post).toHaveBeenCalledOnce(); expect(mocks.session.commit).not.toHaveBeenCalled();
  });
  it("commits one confirmed response for the original owner", async () => {
    mocks.post.mockResolvedValueOnce({ data: { userID: "synthetic", chatToken: "synthetic-chat", imToken: "synthetic-im" } });await mount();
    await act(async () => { await submit(params); });
    expect(mocks.post).toHaveBeenCalledOnce();expect(mocks.session.commit).toHaveBeenCalledOnce();
    expect(mocks.session.commit).toHaveBeenCalledWith({ userID: "synthetic", chatToken: "synthetic-chat", imToken: "synthetic-im", issuer: "https://route.synthetic.test" }, 0);
  });
});
