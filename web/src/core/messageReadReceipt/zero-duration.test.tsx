// @vitest-environment jsdom
import React, { act, createRef } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
const fixture = vi.hoisted(() => ({ duration: vi.fn(), current: { conversationID: "si-peer", userID: "peer", isPrivateChat: true, burnDuration: 0 } }));
vi.mock("@/hooks/useConversationSettings", () => ({ useConversationSettings: () => ({ currentConversation: fixture.current,
  updateBurnDuration: fixture.duration, updateDestructDuration: vi.fn(), updateConversationPin: vi.fn(), updateConversationMessageRemind: vi.fn(),
  updateConversationPrivateState: vi.fn(), updateConversationMsgDestructState: vi.fn(), clearConversationMessages: vi.fn() }) }));
vi.mock("@/store/contact", () => ({ useContactStore: (selector: Function) => selector({ blackList: [], friendList: [] }) }));
vi.mock("@/layout/MainContentWrap", () => ({ IMSDK: {} }));
vi.mock("@/AntdGlobalComp", () => ({ modal: { confirm: vi.fn() } }));
vi.mock("@/utils/common", () => ({ feedbackToast: vi.fn() }));
vi.mock("@/utils/events", () => ({ default: { emit: vi.fn() } }));
vi.mock("@/components/OIMAvatar", () => ({ default: () => <span /> }));
vi.mock("@/components/SettingRow", () => ({ default: ({ title, children }: any) => <section aria-label={title}>{children}</section> }));
vi.mock("@/pages/chat/queryChat/MsgDestructSetting", () => ({ default: () => null }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("open-im-sdk-wasm", () => ({ MessageReceiveOptType: { Nomal: 0, NotNotify: 1 } }));
vi.mock("@ant-design/icons", () => ({ ArrowLeftOutlined: () => null, CheckOutlined: () => null, RightOutlined: () => null }));
vi.mock("antd", () => ({ Drawer: ({ open, children }: any) => open ? <div>{children}</div> : null,
  Modal: ({ open, children }: any) => open ? <div>{children}</div> : null, Divider: () => null,
  Button: ({ children }: any) => <button>{children}</button>, Spin: ({ children }: any) => children,
  Select: () => null, Space: ({ children }: any) => children }));
import SingleSetting from "@/pages/chat/queryChat/SingleSetting";
it("renders legal zero burn duration as the existing 30-second default without a settings write", () => {
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
  const ref = createRef<any>(); act(() => root.render(<SingleSetting ref={ref} />)); act(() => ref.current.openOverlay());
  expect(host.querySelector('[aria-label="placeholder.privateChatTime"]')?.textContent).toContain("30s");
  expect(fixture.current.burnDuration).toBe(0); expect(fixture.duration).not.toHaveBeenCalled();
  act(() => root.unmount()); host.remove();
});
