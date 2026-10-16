import { Layout } from "antd";
import { Outlet } from "react-router-dom";

import ConversationSider from "./ConversationSider";

export const Chat = () => {
  return (
    <Layout className="app-chat flex-row">
      <ConversationSider />
      <Outlet />
    </Layout>
  );
};
