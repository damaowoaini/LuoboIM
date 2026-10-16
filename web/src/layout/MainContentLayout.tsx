import { bindDesktopMainSdk } from "@/core/session/desktopBridge";
import { IMSDK } from "./MainContentWrap";
import { useEffect } from "react";
import { useMount } from "ahooks";
import { Alert, Button, Layout, Spin } from "antd";
import { t } from "i18next";
import { Outlet, useMatches, useNavigate } from "react-router-dom";

import LeftNavBar from "./LeftNavBar";
import TopSearchBar from "./TopSearchBar";
import { useGlobalEvent } from "./useGlobalEvents";

export const MainContentLayout = () => {
  const matches = useMatches();
  const navigate = useNavigate();
  const [connectState] = useGlobalEvent();
  useEffect(() => (window.electronAPI ? bindDesktopMainSdk(IMSDK) : undefined), []);

  useMount(() => {
    const isRoot = !matches.find((item) => item.pathname !== "/");
    const inConversation = matches.some((item) => item.params.conversationID);
    if (isRoot || inConversation) {
      navigate("chat", {
        replace: true,
      });
    }
  });

  const loadingTip = connectState.isLogining ? t("toast.loading") : t("toast.syncing");

  return (
    <Spin
      className="!max-h-none"
      spinning={connectState.isLogining || connectState.isSyncing}
      tip={loadingTip}
    >
      <Layout className="app-shell h-full">
        <TopSearchBar />
        {!connectState.isConnected && <Alert banner type={connectState.isConnecting ? "info" : "warning"} showIcon
          message={connectState.isConnecting ? "正在连接消息服务…" : "消息服务连接已中断，请检查网络后重新连接。"}
          action={<Button size="small" onClick={() => window.location.reload()}>重新连接</Button>} />}
        <Layout className="app-workspace">
          <LeftNavBar />
          <Outlet />
        </Layout>
      </Layout>
    </Spin>
  );
};
