import { describe, expect, it, vi } from "vitest";
import { resolveContactLookup } from "./contactLookup";

describe("explicit ID and phone namespaces", () => {
  it("finds only the exact supplied ID and returns no profile/private fields", async () => {
    const api = {
      findByIDs: vi.fn().mockResolvedValue([{ userID: "7031156314", email: "not-retained", allowAddFriend: 2 }]),
      searchPhone: vi.fn(),
    };
    await expect(resolveContactLookup("userID", " 7031156314 ", api))
      .resolves.toEqual({ state: "found", userID: "7031156314" });
    expect(api.findByIDs).toHaveBeenCalledExactlyOnceWith(["7031156314"]);
    expect(api.searchPhone).not.toHaveBeenCalled();
  });
  it("does not fall back to a phone whose value equals a missing user ID", async () => {
    const api = { findByIDs: vi.fn().mockResolvedValue([]), searchPhone: vi.fn() };
    expect(await resolveContactLookup("userID", "7031156314", api)).toEqual({ state: "missing" });
    expect(api.searchPhone).not.toHaveBeenCalled();
  });
  it("refuses wrong, duplicate and mixed IDs rather than selecting the first result", async () => {
    for (const rows of [[{ userID: "other" }], [{ userID: "7031156314" }, { userID: "7031156314" }],
      [{ userID: "7031156314" }, { userID: "other" }]]) {
      expect(await resolveContactLookup("userID", "7031156314", {
        findByIDs: async () => rows, searchPhone: vi.fn(),
      })).toEqual({ state: "ambiguous" });
    }
  });
  it("preserves explicit phone lookup and never calls the ID endpoint", async () => {
    const api = {
      findByIDs: vi.fn(), searchPhone: vi.fn().mockResolvedValue({ total: 1, users: [{ userID: "phone-owner" }] }),
    };
    expect(await resolveContactLookup("phoneNumber", " 13800138000 ", api))
      .toEqual({ state: "found", userID: "phone-owner" });
    expect(api.searchPhone).toHaveBeenCalledExactlyOnceWith("13800138000");
    expect(api.findByIDs).not.toHaveBeenCalled();
  });
  it("rejects phone ambiguity even when showNumber1 returns only its first row", async () => {
    expect(await resolveContactLookup("phoneNumber", "13800138000", {
      findByIDs: vi.fn(), searchPhone: async () => ({ total: 2, users: [{ userID: "first" }] }),
    })).toEqual({ state: "ambiguous" });
  });
  it("never enumerates email/nickname or sends invalid phone search text", async () => {
    const api = { findByIDs: vi.fn(), searchPhone: vi.fn() };
    for (const value of ["", "friend@example.test", "nickname", "%703%", "138_", "123", "1".repeat(33)])
      expect(await resolveContactLookup("phoneNumber", value, api)).toEqual({ state: "invalid" });
    expect(api.findByIDs).not.toHaveBeenCalled();
    expect(api.searchPhone).not.toHaveBeenCalled();
  });
});
