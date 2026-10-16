import { beforeEach, expect, it, vi } from "vitest";
const post = vi.hoisted(() => vi.fn());
vi.mock("@/core/session/browser", () => ({
  sessionCoordinator: { current: undefined, generation: 0 },
}));
vi.mock("@/config", () => ({ getChatUrl: () => "https://tenant.example/chat" }));
vi.mock("@/utils/request", () => ({ default: () => ({ post }) }));
import { fetchUserMoments } from "./moments";
beforeEach(() => {
  post.mockReset();
});
it("propagates transport and authorization failures instead of showing an empty feed", async () => {
  const unavailable = new Error("service unavailable");
  post.mockRejectedValue(unavailable);
  await expect(fetchUserMoments({ pageParam: 1 })).rejects.toThrow(
    "service unavailable",
  );
});
it("preserves real empty results and owner-specific paginated request contracts", async () => {
  post.mockResolvedValue({ data: { workMoments: [] } });
  await expect(fetchUserMoments({ pageParam: 1 })).resolves.toEqual([]);
  expect(post).toHaveBeenLastCalledWith("/office/work_moment/find/recv", {
    userID: undefined,
    pagination: { pageNumber: 1, showNumber: 20 },
  });
  const moment = { workMomentID: "owned-moment" };
  post.mockResolvedValue({ data: { workMoments: [moment] } });
  await expect(fetchUserMoments({ pageParam: 2 }, "friend-id")).resolves.toEqual([
    moment,
  ]);
  expect(post).toHaveBeenLastCalledWith("/office/work_moment/find/send", {
    userID: "friend-id",
    pagination: { pageNumber: 2, showNumber: 20 },
  });
});
