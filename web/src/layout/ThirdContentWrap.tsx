import { Alert, Button } from "antd";
import { useEffect, useState } from "react";
import { Outlet } from "react-router-dom";
import { sessionCoordinator } from "@/core/session/browser";
import { applyDesktopSnapshot } from "@/core/session/desktopBridge";
export default function ThirdContentWrap() {
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  useEffect(() => {
    let cancelled = false;
    const unsubscribe = window.electronAPI?.subscribe(
      "desktopSessionState",
      (value) => {
        try {
          applyDesktopSnapshot(value);
        } catch {
          if (!cancelled) setStatus("error");
        }
      },
    );
    void sessionCoordinator
      .restore()
      .then(async (session) => {
        if (!session || !window.electronAPI) throw new Error("请从已登录的主窗口打开");
        const lease = sessionCoordinator.lease();
        try {
          const data = await window.electronAPI.ipcInvoke(
            "callDesktopSession",
            "snapshot",
            [],
          );
          lease.assertCurrent();
          if (cancelled) return;
          applyDesktopSnapshot(data);
          setStatus("ready");
        } finally {
          lease.release();
        }
      })
      .catch(() => {
        if (!cancelled) setStatus("error");
      });
    return () => {
      cancelled = true;
      unsubscribe?.();
    };
  }, []);
  if (status === "error")
    return (
      <Alert
        type="error"
        showIcon
        message="主窗口会话不可用，请关闭后从主窗口重新打开。"
        action={<Button onClick={() => window.close()}>关闭</Button>}
      />
    );
  return status === "ready" ? (
    <Outlet />
  ) : (
    <div role="status" className="flex h-screen items-center justify-center">
      正在同步主窗口…
    </div>
  );
}
