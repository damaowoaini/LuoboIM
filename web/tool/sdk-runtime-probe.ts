import { SessionCoordinator } from "../src/core/session/session";
import { SdkRuntimeBoundary } from "../src/core/session/sdkRuntime";
const prefix = "luobo.runtime-boundary-probe.";
const output = document.getElementById("result")!;
const button = document.getElementById("run") as HTMLButtonElement;
const realm = window as unknown as { oldProbeRealm?: boolean };
const coordinator = new SessionCoordinator({
  read: async () => JSON.parse(localStorage.getItem(prefix + "record") || "null"),
  write: async (value) => {
    await new Promise((resolve) => setTimeout(resolve, 100));
    localStorage.setItem(prefix + "record", JSON.stringify(value));
  },
  clear: async () => {
    await new Promise((resolve) => setTimeout(resolve, 100));
    localStorage.removeItem(prefix + "record");
  },
});
const boundary = new SdkRuntimeBoundary(
  coordinator,
  () => window.location.reload(),
  async () => {
    sessionStorage.setItem(prefix + "shutdown", "settled");
  },
);
const response = (tenant: string) => ({
  issuer: "https://synthetic-route.example",
  userID: "same-synthetic-user",
  chatToken: "synthetic-chat",
  imToken: "synthetic-im",
  serverInfo: {
    serverID: tenant,
    serverName: tenant,
    domains: ["synthetic-tenant.example"],
  },
});
const evidence: string[] = [];
const check = (condition: boolean, message: string) => {
  if (!condition) throw new Error(message);
  evidence.push(message);
};
const show = (status: string, message?: string) => {
  output.textContent = JSON.stringify(
    {
      status,
      evidence,
      message,
      limit: "真实document重载与存储顺序，未认证调用Go/WASM或业务服务。",
    },
    null,
    2,
  );
};
const stage = sessionStorage.getItem(prefix + "stage");
try {
  if (stage === "switched") {
    await coordinator.restore();
    check(!realm.oldProbeRealm, "重载已销毁旧document JavaScript realm");
    check(
      coordinator.current?.tenant.serverID === "tenant-b",
      "新页只恢复已完成写入的B租户整组凭证",
    );
    check(
      sessionStorage.getItem(prefix + "shutdown") === "settled",
      "重载在旧运行时shutdown结束之后执行",
    );
    boundary.beforeLogin();
    check(true, "新document首次登录不触发持久化重载循环");
    sessionStorage.setItem(prefix + "stage", "ready-for-logout");
    button.textContent = "验证退出清理后重载";
    show("PASS_SWITCH");
  } else if (stage === "logged-out") {
    await coordinator.restore();
    check(!realm.oldProbeRealm, "退出重载已销毁旧document realm");
    check(
      !coordinator.current && !localStorage.getItem(prefix + "record"),
      "新页无法恢复已撤销的合成凭证",
    );
    check(
      sessionStorage.getItem(prefix + "shutdown") === "settled",
      "退出重载在shutdown完成后执行",
    );
    show("PASS_LOGOUT");
    button.textContent = "重新运行重载边界验收";
  }
} catch (error) {
  show("FAIL", String(error));
}
button.addEventListener("click", async () => {
  button.disabled = true;
  try {
    if (sessionStorage.getItem(prefix + "stage") === "ready-for-logout") {
      realm.oldProbeRealm = true;
      sessionStorage.removeItem(prefix + "shutdown");
      sessionStorage.setItem(prefix + "stage", "logged-out");
      await coordinator.clear();
      show("RELOADING_LOGOUT");
    } else {
      sessionStorage.removeItem(prefix + "shutdown");
      sessionStorage.setItem(prefix + "stage", "switched");
      await coordinator.commit(response("tenant-a"));
      boundary.beforeLogin();
      realm.oldProbeRealm = true;
      await coordinator.commit(response("tenant-b"));
      show("RELOADING_SWITCH");
    }
  } catch (error) {
    show("FAIL", String(error));
    button.disabled = false;
  }
});
