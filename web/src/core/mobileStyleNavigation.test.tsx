// @vitest-environment jsdom
// Real navigation components; SDK/HTTP/OS and overlay primitives are isolated.
// These assert retained actions/focus, not pixel fidelity or native delivery.
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const f = vi.hoisted(() => ({
  confirm: vi.fn(), logout: vi.fn(async () => {}), emit: vi.fn(),
  sdk: { uploadFile: vi.fn() }, updateProfile: vi.fn(),
  user: { selfInfo: { userID: "fixture-user", nickname: "合成昵称", faceURL: "" }, workMomentsUnreadCount: 0 },
  contact: { unHandleFriendApplicationCount: 0, unHandleGroupApplicationCount: 0 },
}));
vi.mock("@/store", () => ({
  useUserStore: (select: (state: object) => unknown) => select({ ...f.user, userLogout: f.logout, updateSelfInfo: vi.fn() }),
  useContactStore: (select: (state: object) => unknown) => select(f.contact),
  useConversationStore: (select: (state: object) => unknown) => select({ unReadCount: 0 }),
}));
vi.mock("@/AntdGlobalComp", () => ({ modal: { confirm: f.confirm } }));
vi.mock("@/layout/MainContentWrap", () => ({ IMSDK: f.sdk }));
vi.mock("@/api/login", () => ({ updateBusinessUserInfo: f.updateProfile }));
vi.mock("@/utils/common", () => ({ feedbackToast: vi.fn() }));
vi.mock("@/utils/events", () => ({ default: { emit: f.emit } }));
vi.mock("@/utils/childWindows", () => ({ openAbout: vi.fn(), openPersonalSettings: vi.fn() }));
vi.mock("react-image-file-resizer", () => ({ default: { imageFileResizer: vi.fn() } }));
vi.mock("i18next", () => ({ default: { on: vi.fn() }, t: (key: string) => key }));
vi.mock("@/layout/LeftNavBar/About", () => ({ default: () => null }));
vi.mock("@/layout/LeftNavBar/PersonalSettings", () => ({ default: () => null }));
vi.mock("@/components/OIMAvatar", () => ({ default: () => <span aria-hidden="true" /> }));
vi.mock("antd", () => ({
  Layout: { Sider: ({ children, ...props }: React.PropsWithChildren<{ className?: string }>) => <aside className={props.className}>{children}</aside> },
  Badge: ({ children }: React.PropsWithChildren) => <>{children}</>,
  Popover: ({ children }: React.PropsWithChildren) => <>{children}</>,
  Divider: () => <hr />,
  Upload: ({ children }: React.PropsWithChildren) => <>{children}</>,
}));

import LeftNavBar from "@/layout/LeftNavBar";
import ContactSider from "@/pages/contact/ContactSider";

let host: HTMLDivElement;
let root: Root;
const RouteProbe = () => <output aria-label="当前路径">{useLocation().pathname}</output>;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  f.confirm.mockReset(); f.logout.mockClear(); f.emit.mockClear();
  f.sdk.uploadFile.mockClear(); f.updateProfile.mockClear();
  window.location.hash = "#/chat";
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
async function mount(node: React.ReactNode, route = "/chat") {
  window.location.hash = "#" + route;
  await act(async () => root.render(<MemoryRouter initialEntries={[route]}>{node}<RouteProbe /></MemoryRouter>));
}
function button(label: string) {
  const node = [...host.querySelectorAll<HTMLButtonElement>("button")].find(item => item.getAttribute("aria-label") === label);
  if (!node) throw new Error("Missing actual navigation button " + label);
  return node;
}
async function click(node: HTMLElement) { await act(async () => node.click()); }
function path() { return host.querySelector("output")?.textContent; }

describe("mobile style integration retains real navigation actions", () => {
  it("opens messages from the contact route", async () => {
    await mount(<LeftNavBar />, "/contact"); await click(button("placeholder.chat"));
    expect(path()).toBe("/chat"); expect(f.sdk.uploadFile).not.toHaveBeenCalled();
  });
  it("opens contacts without invoking message or profile writes", async () => {
    await mount(<LeftNavBar />); await click(button("placeholder.contact"));
    expect(path()).toBe("/contact"); expect(f.updateProfile).not.toHaveBeenCalled();
  });
  it("keeps moments as its existing overlay action", async () => {
    await mount(<LeftNavBar />); await click(button("placeholder.moments"));
    expect(f.emit).toHaveBeenCalledWith("OPEN_MOMENTS"); expect(path()).toBe("/chat");
  });
  it("keeps logout behind explicit confirmation and calls it once after confirm", async () => {
    await mount(<LeftNavBar />); await click(button("placeholder.logOut"));
    expect(f.confirm).toHaveBeenCalledOnce(); expect(f.logout).not.toHaveBeenCalled();
    const confirm = f.confirm.mock.calls[0][0] as { onOk: () => Promise<void> };
    await act(async () => confirm.onOk()); expect(f.logout).toHaveBeenCalledOnce();
  });
  it("keeps all four contact destinations and their selected state", async () => {
    await mount(<ContactSider />, "/contact");
    for (const [label, destination] of [["newFriends", "/contact/newFriends"], ["groupNotification", "/contact/groupNotifications"], ["myFriend", "/contact"], ["myGroup", "/contact/myGroups"]]) {
      const action = button("placeholder." + label); await click(action);
      expect(path()).toBe(destination); expect(action.getAttribute("aria-current")).toBe("page");
    }
    expect(f.confirm).not.toHaveBeenCalled(); expect(f.logout).not.toHaveBeenCalled();
  });
  it("provides a real focusable button for a contact destination", async () => {
    await mount(<ContactSider />, "/contact"); const action = button("placeholder.myGroup");
    action.focus(); expect(document.activeElement).toBe(action); expect(action.type).toBe("button");
    await click(action); expect(path()).toBe("/contact/myGroups"); expect(f.sdk.uploadFile).not.toHaveBeenCalled();
  });
});
