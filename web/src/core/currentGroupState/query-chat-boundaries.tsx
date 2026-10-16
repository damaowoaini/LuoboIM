import React from "react";
import { vi } from "vitest";
import "./harness";

// Keep QueryChat, ChatFooter, both hooks and the conversation reducer real.
// Unrelated history/media/drag/send widgets and OS DOM selection are adapted;
// this suite never sends messages or edits a draft via an artificial reducer.
vi.mock("@/pages/chat/queryChat/ChatContent", () => ({ default: () => <div data-testid="history" /> }));
vi.mock("@/pages/chat/queryChat/ChatHeader", () => ({ default: () => <header /> }));
vi.mock("@/pages/chat/queryChat/ChatFooter/MultipleActionBar", () => ({ default: () => <div /> }));
vi.mock("@/pages/chat/queryChat/useDropAndPaste", () => ({ useDropAndPaste: () => ({ droping: false }) }));
vi.mock("@/pages/chat/queryChat/useMessageReceipt", () => ({ useMessageReceipt: () => {} }));
vi.mock("@/pages/chat/queryChat/ChatFooter/AtSearchPanel", async () => {
  const { forwardRef } = await import("react");
  return { ForwardAtSearchPanel: forwardRef(() => null), SnapPreviewGroup: () => null };
});
vi.mock("@/pages/chat/queryChat/ChatFooter/SendActionBar", () => ({ default: () => null }));
vi.mock("@/pages/chat/queryChat/ChatFooter/SendActionBar/useFileMessage", () => ({ useFileMessage: () => ({ createFileMessage: vi.fn() }) }));
vi.mock("@/pages/chat/queryChat/ChatFooter/useDropDomOnly", () => ({ useDropDomOnly: () => {} }));
vi.mock("@/pages/chat/queryChat/ChatFooter/useSendMessage", () => ({ useSendMessage: () => ({ sendMessage: vi.fn() }) }));
vi.mock("@/core/messagePrivacyRuntime", () => ({ messageOwner: () => "fixture-owner",
  captureMessageAccess: () => ({ current: () => true, assertCurrent: () => {} }),
  captureMessageRetention: () => ({ current: () => true, assertCurrent: () => {} }),
}));
vi.mock("@/hooks/useMessagePrivacy", () => ({ useMessagePrivacy: () => ({ allowsRetention: true }) }));
vi.mock("@/components/Twemoji", () => ({ parseTwemoji: (value: string) => value }));
vi.mock("@/components/EditableDiv", async () => {
  const { forwardRef, useImperativeHandle, useRef } = await import("react");
  return {
  default: forwardRef(function Editor({ id, html }: { id: string; html: string }, ref) {
    const el = useRef<HTMLDivElement>(null);
    useImperativeHandle(ref, () => ({ el }));
    return <div id={id} ref={el} data-testid="actual-footer-editor" dangerouslySetInnerHTML={{ __html: html }} />;
  }), restoreSelection: () => {}, deleteBeforeAt: () => {}, insertAtCursor: () => {},
  };
});
vi.mock("react-router-dom", () => ({ useParams: () => ({ conversationID: "cid-A" }) }));
vi.mock("@ant-design/icons", () => ({ InfoCircleOutlined: () => <i /> }));
vi.mock("antd", () => ({
  Layout: ({ children, id }: any) => <main id={id}>{children}</main>, Spin: () => null,
  Button: ({ children, onClick }: any) => <button onClick={onClick}>{children}</button>,
  Popover: ({ children }: any) => <>{children}</>,
}));
