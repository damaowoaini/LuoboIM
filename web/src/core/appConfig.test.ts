// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";

const f = vi.hoisted(() => ({
  post: vi.fn(), session: { current: undefined, restore: vi.fn(async () => undefined) },
}));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: f.session }));
vi.mock("@/config", () => ({ getChatUrl: () => "", getRouteUrl: () => "http://127.0.0.1:7778" }));
vi.mock("@/store", () => ({ useUserStore: vi.fn() }));
vi.mock("@/utils/storage", () => ({ getChatToken: vi.fn() }));
vi.mock("@/api/errorHandle", () => ({ errorHandle: vi.fn() }));
vi.mock("@/utils/request", () => ({ default: () => ({ post: f.post }) }));
import { getAppConfig } from "@/api/login";

beforeEach(() => { f.post.mockReset(); f.session.restore.mockClear(); });
describe("successful client config wire data", () => {
  it("normalizes the real omitted protobuf map and retains string policy without disabling default verification", async () => {
    f.post.mockResolvedValueOnce({ data: {} });
    const empty = await getAppConfig();
    expect(empty.data.config).toEqual({});
    expect(String(empty.data.config.needVerificationCodeRegister ?? "1")).toBe("1");
    expect(f.post).toHaveBeenCalledWith("/api/client_config/get", { invitationCode: undefined }, expect.any(Object));
    const config = { needVerificationCodeRegister: "0", needInvitationCodeRegister: "1", discoverPageURL: "https://public.example" };
    f.post.mockResolvedValueOnce({ data: { config } });
    expect((await getAppConfig("synthetic-invite")).data.config).toEqual(config);
    expect(f.post.mock.calls[1][1]).toEqual({ invitationCode: "synthetic-invite" });
  });

  it("rejects malformed successful data and preserves transport or business rejection instead of inventing empty success", async () => {
    for (const data of [undefined, null, [], "", 0, { config: null }, { config: [] }, { config: "" },
      { config: { needVerificationCodeRegister: false } }, { config: { needVerificationCodeRegister: 0 } },
      { config: { nested: {} } }]) {
      f.post.mockResolvedValueOnce({ data });
      await expect(getAppConfig()).rejects.toThrow("服务配置响应格式错误");
    }
    for (const error of [new Error("synthetic network failure"), Object.assign(new Error("synthetic business failure"), { errCode: 1001 })]) {
      f.post.mockRejectedValueOnce(error);
      await expect(getAppConfig()).rejects.toBe(error);
    }
  });
});
