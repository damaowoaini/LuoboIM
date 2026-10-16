import { App as AntdApp, ConfigProvider } from "antd";
import enUS from "antd/locale/en_US";
import zhCN from "antd/locale/zh_CN";
import { Suspense, useEffect, useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { RouterProvider } from "react-router-dom";

import AntdGlobalComp from "./AntdGlobalComp";
import router from "./routes";
import { luoboAntdTheme, mobileTokenStyles } from "./design/tokens";
import {
  useUserStore,
  useContactStore,
  useConversationStore,
  useMessageStore,
} from "./store";
import { sessionCoordinator } from "@/core/session/browser";
import { BusinessUserInfo } from "@/api/login";

function App() {
  const locale = useUserStore((state) => state.appSettings.locale);
  const [viewGeneration, setViewGeneration] = useState(sessionCoordinator.generation);
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            refetchOnWindowFocus: false,
          },
        },
      }),
  );
  useEffect(() => {
    let generation = sessionCoordinator.generation;
    return sessionCoordinator.onChange(() => {
      if (generation === sessionCoordinator.generation) return;
      generation = sessionCoordinator.generation;
      setViewGeneration(generation);
      queryClient.clear();
      useUserStore.setState({
        selfInfo: {} as BusinessUserInfo,
        appConfig: {} as import("./store/type").AppConfig,
        appConfigStatus: "idle",
        imageCache: {},
        workMomentsUnreadCount: 0,
      });
      useContactStore.getState().clearContactStore();
      useConversationStore.getState().clearConversationStore();
      useMessageStore.getState().clearHistoryMessage();
      useMessageStore.setState({ downloadMap: {}, isCheckMode: false });
    });
  }, []);

  return (
    <ConfigProvider
      button={{ autoInsertSpace: false }}
      locale={locale === "zh-CN" ? zhCN : enUS}
      theme={luoboAntdTheme}
    >
      <style>{mobileTokenStyles}</style>
      <QueryClientProvider client={queryClient}>
        <Suspense fallback={<div>loading...</div>}>
          <AntdApp>
            <AntdGlobalComp />
            <RouterProvider key={viewGeneration} router={router} />
          </AntdApp>
        </Suspense>
      </QueryClientProvider>
    </ConfigProvider>
  );
}

export default App;
