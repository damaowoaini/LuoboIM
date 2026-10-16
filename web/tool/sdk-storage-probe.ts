import { getSDK } from "open-im-sdk-wasm";
import { sdkDataDirectory } from "../src/core/session/sdkDirectory";
type Result = { errCode: number; data?: { nickname?: string } };
interface WorkerApi {
  setSqlWasmPath(path: string): Promise<unknown>;
  initDB(user: string, directory: string): Promise<string>;
  close(): Promise<string>;
  insertLoginUser(value: string): Promise<string>;
  getLoginUser(user: string): Promise<string>;
  initSDK(operationID: string, config: string): unknown;
  login(...args: unknown[]): Promise<unknown>;
}
const worker = window as unknown as WorkerApi;
const result = document.getElementById("result")!;
const button = document.getElementById("run") as HTMLButtonElement;
button.addEventListener("click", async () => {
  button.disabled = true;
  result.textContent = "正在初始化真实 SQL Worker…";
  const evidence: unknown[] = [];
  const check = (value: boolean, message: string) => {
    if (!value) throw new Error(message);
    evidence.push({ result: "PASS", case: message });
  };
  try {
    const sdk = getSDK({ coreWasmPath: "/openIM.wasm", sqlWasmPath: "/sql-wasm.wasm" });
    await (sdk as unknown as { wasmInitializedPromise: Promise<void> })
      .wasmInitializedPromise;
    await worker.setSqlWasmPath("/sql-wasm.wasm");
    const nonce = crypto.randomUUID();
    const user = "luobo-storage-probe-user";
    const directoryA = await sdkDataDirectory(`${nonce}:tenant-a:${user}`);
    const directoryB = await sdkDataDirectory(`${nonce}:tenant-b:${user}`);
    const read = async () => {
      const response = JSON.parse(await worker.getLoginUser(user));
      return {
        ...response,
        data: response.data ? JSON.parse(response.data) : undefined,
      } as Result;
    };
    const success = async (response: Promise<string>) => {
      const parsed = JSON.parse(await response);
      if (parsed.errCode !== 0)
        throw new Error("SQL Worker returned " + JSON.stringify(parsed));
    };
    await success(worker.initDB(user, directoryA));
    await success(
      worker.insertLoginUser(
        JSON.stringify({ userID: user, nickname: "tenant-A-marker" }),
      ),
    );
    check(
      (await read()).data?.nickname === "tenant-A-marker",
      "租户 A 合成记录可写入读取",
    );
    await success(worker.close());
    await success(worker.initDB(user, directoryB));
    check((await read()).errCode !== 0, "相同 userID 的租户 B 不能读取 A 记录");
    await success(
      worker.insertLoginUser(
        JSON.stringify({ userID: user, nickname: "tenant-B-marker" }),
      ),
    );
    check((await read()).data?.nickname === "tenant-B-marker", "租户 B 拥有独立记录");
    await success(worker.close());
    await success(worker.initDB(user, directoryA));
    check(
      (await read()).data?.nickname === "tenant-A-marker",
      "关闭并重新打开 A 后持久记录仍属于 A",
    );
    await success(worker.close());
    await success(worker.initDB(user, directoryB));
    check(
      (await read()).data?.nickname === "tenant-B-marker",
      "关闭并重新打开 B 后持久记录仍属于 B",
    );
    await success(worker.close());
    const init = worker.initSDK,
      login = worker.login;
    let transmittedDirectory = "";
    try {
      worker.initSDK = (_id, json) => {
        transmittedDirectory = JSON.parse(json).dataDir;
        return true;
      };
      worker.login = async () => ({ errCode: 0 });
      await sdk.login({
        userID: user,
        token: "synthetic-probe-token",
        platformID: 5,
        apiAddr: "https://example.invalid",
        wsAddr: "wss://example.invalid",
        dataDir: directoryA,
        isLogStandardOutput: false,
      });
      check(
        transmittedDirectory === directoryA,
        "真实 JS wrapper 把独立 dataDir 传给 initSDK",
      );
    } finally {
      worker.initSDK = init;
      worker.login = login;
    }
    evidence.push({
      lifecycleExports: ["initSDK", "unInitSDK", "unInitSdk", "logout"].map((name) => ({
        name,
        type: typeof (window as unknown as Record<string, unknown>)[name],
      })),
      databases: await indexedDB.databases(),
      limit:
        "真实 Worker 与 wrapper 探针；未认证连接业务后端，不能代替 Go WASM→IM 服务全链路验收。",
    });
    result.textContent = JSON.stringify({ status: "PASS", evidence }, null, 2);
  } catch (error) {
    result.textContent = JSON.stringify(
      {
        status: "FAIL",
        evidence,
        message: error instanceof Error ? error.message : "probe failed",
      },
      null,
      2,
    );
  } finally {
    button.disabled = false;
  }
});
