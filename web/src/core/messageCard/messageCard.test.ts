import { describe, expect, it } from "vitest";
import { parseReceivedCard } from "@/core/messageCard";

describe("bounded OpenIM 108 identity hint", () => {
  it("accepts exact opaque IDs, trims a display name and discards URL/ex fields", () => {
    expect(parseReceivedCard({ userID: "sg_user_703", nickname: "  联系人  ", faceURL: "file:///synthetic-secret", ex: "{not-json" }))
      .toEqual({ userID: "sg_user_703", label: "联系人" });
    expect(parseReceivedCard({ userID: "703", nickname: "  " })).toEqual({ userID: "703", label: "703" });
    expect(parseReceivedCard({ userID: "https:703", nickname: "https://name.synthetic.test" })).toEqual({ userID: "https:703", label: "https://name.synthetic.test" });
  });
  it("rejects malformed shapes and invalid exact IDs without coercion", () => {
    for (const value of [null, [], "{}", {}, { userID: 703 }, { userID: "" }, { userID: " 703" }, { userID: "70 3" }, { userID: "x".repeat(129) }, { userID: "703\u202e" }])
      expect(parseReceivedCard(value)).toBeUndefined();
  });
  it("rejects control, bidi and non-lossless Unicode while preserving plain URL-like text", () => {
    for (const nickname of [7, "A\u0000B", "A\u0085B", "A\u202eB", "x\ud800"])
      expect(parseReceivedCard({ userID: "703", nickname })).toBeUndefined();
  });
  it("counts nickname Unicode scalars while enforcing optional field and total UTF8 limits", () => {
    expect(parseReceivedCard({ userID: "703", nickname: "😀".repeat(128) })).toBeDefined();
    expect(parseReceivedCard({ userID: "703", nickname: "😀".repeat(129) })).toBeUndefined();
    expect(parseReceivedCard({ userID: "703", faceURL: "x".repeat(2049) })).toBeUndefined();
    expect(parseReceivedCard({ userID: "703", ex: "x".repeat(4097) })).toBeUndefined();
    expect(parseReceivedCard({ userID: "703", faceURL: {}, ex: "" })).toBeUndefined();
    expect(parseReceivedCard({ userID: "703", faceURL: "中".repeat(2048), ex: "中".repeat(1000) })).toBeUndefined();
  });
});
