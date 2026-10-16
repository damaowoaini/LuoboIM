import { expect, it } from "vitest";
import { sdkDataDirectory, closeSdkWorker } from "./sdkDirectory";
it("gives the same user separate stable SQL worker files for issuer, tenant and account", async () => {
  const a = await sdkDataDirectory("issuer-one:tenant-a:same-user");
  expect(a).toMatch(/^\.\/luobo_[a-f0-9]{64}_$/);
  expect(await sdkDataDirectory("issuer-one:tenant-a:same-user")).toBe(a);
  expect(await sdkDataDirectory("issuer-one:tenant-b:same-user")).not.toBe(a);
  expect(await sdkDataDirectory("issuer-two:tenant-a:same-user")).not.toBe(a);
  expect(await sdkDataDirectory("issuer-one:tenant-a:different-user")).not.toBe(a);
});
it("rejects worker close error envelopes instead of opening another account", async () => {
  await expect(
    closeSdkWorker(async () =>
      JSON.stringify({ errCode: 10001, errMsg: "private detail" }),
    ),
  ).rejects.toThrow("数据库关闭失败");
  await expect(
    closeSdkWorker(async () => JSON.stringify({ errCode: 0, data: "" })),
  ).resolves.toBeUndefined();
  await expect(closeSdkWorker(async () => undefined)).rejects.toThrow("数据库关闭失败");
});
