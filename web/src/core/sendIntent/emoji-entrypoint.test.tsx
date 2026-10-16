// @vitest-environment jsdom
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { deferred, enter, fixture, flush, message, reset } from "./harness";
vi.mock("emoji-picker-element", () => ({ Picker: class { constructor() { const element = document.createElement("div"); element.attachShadow({ mode: "open" }); return element; } } }));
vi.mock("antd", () => ({ Popover: ({ children }: any) => <>{children}</>, Upload: ({ children }: any) => <>{children}</>, Divider: () => null }));
vi.mock("@/utils/storage", () => ({ getUserCustomEmojis: async () => [{ url: "https://media.synthetic.invalid/face.png", width: 24, height: 24 }], setUserCustomEmojis: vi.fn() }));
vi.mock("@/components/Twemoji", () => ({ parseTwemoji: (s: string) => s }));
vi.mock("@/components/EditableDiv", () => ({ insertAtCursor: fixture.insert }));
import EmojiPopContent from "@/pages/chat/queryChat/ChatFooter/SendActionBar/EmojiPopContent";
let host: HTMLDivElement, root: Root;
beforeEach(() => { reset(); host = document.createElement("div"); document.body.append(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });
it("does not retarget a custom emoji whose SDK creation completes after A→B", async () => {
  act(() => root.render(<EmojiPopContent />));
  const tabs = host.querySelectorAll(".flex.px-4.py-2 > div"); expect(tabs.length).toBe(2);
  await act(async () => { (tabs[1] as HTMLElement).click(); await flush(); });
  const img = host.querySelector('img[src="https://media.synthetic.invalid/face.png"]'); expect(img).not.toBeNull();
  const reply = deferred<any>(); fixture.sdk.createFaceMessage.mockReturnValueOnce(reply.promise);
  act(() => (img as HTMLElement).click()); expect(fixture.sdk.createFaceMessage).toHaveBeenCalledOnce();
  act(() => enter("B")); reply.resolve({ data: message("emoji") }); await act(flush);
  expect(fixture.sdk.sendMessage).not.toHaveBeenCalled();
});

it("preserves a same-chat custom emoji send", async () => {
  act(() => root.render(<EmojiPopContent />)); const tabs = host.querySelectorAll(".flex.px-4.py-2 > div");
  await act(async () => { (tabs[1] as HTMLElement).click(); await flush(); });
  const img = host.querySelector('img[src="https://media.synthetic.invalid/face.png"]'); expect(img).not.toBeNull();
  await act(async () => { (img as HTMLElement).click(); await flush(); });
  expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce(); expect(fixture.sdk.sendMessage.mock.calls[0][0]).toMatchObject({ recvID: "A", groupID: "" });
});
