import { QueryClient } from "@tanstack/react-query";
import { expect, it } from "vitest";
import { momentsFeedKey, updateCachedMoment } from "./momentsCache";
import type { WorkMoments } from "@/types/moment";
const moment = (id: string, text: string) =>
  ({ workMomentID: id, content: { text } } as WorkMoments);
it("updates every visible owned feed page without affecting another tenant, logs or page cursors", () => {
  const client = new QueryClient();
  const original = {
    pages: [[moment("one", "before")], [moment("two", "unchanged")]],
    pageParams: [1, 2],
  };
  client.setQueryData(momentsFeedKey("tenant-a", "friend"), original);
  client.setQueryData(momentsFeedKey("tenant-a"), original);
  client.setQueryData(momentsFeedKey("tenant-b"), original);
  client.setQueryData(["moments", "tenant-a", "logs"], { marker: "unrelated shape" });
  updateCachedMoment(client, "tenant-a", moment("one", "after"));
  const result: any = client.getQueryData(momentsFeedKey("tenant-a", "friend"));
  expect(result.pages[0][0].content.text).toBe("after");
  expect(result.pages[1][0].content.text).toBe("unchanged");
  expect(result.pageParams).toEqual([1, 2]);
  expect(
    (client.getQueryData(momentsFeedKey("tenant-a")) as any).pages[0][0].content.text,
  ).toBe("after");
  expect(client.getQueryData(momentsFeedKey("tenant-b"))).toEqual(original);
  expect(client.getQueryData(["moments", "tenant-a", "logs"])).toEqual({
    marker: "unrelated shape",
  });
  expect(client.getQueryData(momentsFeedKey("tenant-a", "absent"))).toBeUndefined();
  client.clear();
});
it("does not confuse a user named SelfMoments with the received feed", () => {
  expect(momentsFeedKey("tenant", "SelfMoments")).not.toEqual(momentsFeedKey("tenant"));
});
