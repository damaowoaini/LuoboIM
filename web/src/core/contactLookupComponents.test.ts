// @vitest-environment jsdom
// Actual SearchUserOrGroup component. Only SDK/API/session and UI primitives are
// adapted; this does not exercise a real backend, friend request, or login.
import React, { act, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { OverlayVisibleHandle } from "@/hooks/useOverlayVisible";

const fixture = vi.hoisted(() => ({
  find: vi.fn(), phone: vi.fn(), groups: vi.fn(), users: vi.fn(), warning: vi.fn(), feedback: vi.fn(),
  session: { current: { owner: "A" } as object | null, generation: 1 },
}));
vi.mock("@/api/login", () => ({ getBusinessUserInfo: fixture.find, searchBusinessUserInfo: fixture.phone }));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: fixture.session }));
vi.mock("@/layout/MainContentWrap", () => ({ IMSDK: { getSpecifiedGroupsInfo: fixture.groups, getUsersInfoWithCache: fixture.users } }));
vi.mock("i18next", () => ({ t: (key: string) => key }));
vi.mock("@/AntdGlobalComp", () => ({ message: { warning: fixture.warning } }));
vi.mock("@/utils/common", () => ({ feedbackToast: fixture.feedback }));
vi.mock("@ant-design/icons", () => ({ CloseOutlined: ({ onClick }: any) =>
  React.createElement("button", { onClick, "aria-label": "close lookup" }, "close") }));
vi.mock("@/components/DraggableModalWrap", () => ({ default: ({ open, children }: any) => open ?
  React.createElement("section", { "aria-label": "lookup modal" }, children) : null }));
vi.mock("antd", () => {
  const RadioContext = React.createContext<any>(null);
  return {
    Button: ({ children, onClick, disabled, loading }: any) =>
      React.createElement("button", { onClick, disabled: disabled || loading }, children),
    Input: { Search: ({ id, value, onChange, onSearch }: any) =>
      React.createElement("input", { id, value, onChange, onKeyDown: (event: any) => {
        if (event.key === "Enter") onSearch();
      } }) },
    Radio: {
      Group: ({ value, onChange, children }: any) => React.createElement(RadioContext.Provider,
        { value: { value, onChange } }, children),
      Button: ({ value, children }: any) => {
        const group = React.useContext(RadioContext);
        return React.createElement("button", { role: "radio", "aria-checked": group.value === value,
          onClick: () => group.onChange({ target: { value } }) }, children);
      },
    },
  };
});

// Independent config aliases this import to BEFORE or AFTER real source; after
// review the same test path also works with the normal production source tree.
import SearchUserOrGroup from "@/layout/TopSearchBar/SearchUserOrGroup";

let root: Root, host: HTMLDivElement;
let ref: React.RefObject<OverlayVisibleHandle | null>;
let openUser = vi.fn<(userID: string) => void>();
let openGroup = vi.fn<(group: { groupID: string }) => void>();
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  vi.clearAllMocks();
  fixture.session.current = { owner: "A" }; fixture.session.generation = 1;
  fixture.find.mockResolvedValue({ data: { users: [{ userID: "7031156314", allowAddFriend: 2 }] } });
  fixture.phone.mockResolvedValue({ data: { total: 0, users: [] } });
  fixture.users.mockResolvedValue({ data: [{ friendInfo: { userID: "phone-owner", remark: "" } }] });
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  ref = createRef();
  openUser = vi.fn<(userID: string) => void>();
  openGroup = vi.fn<(group: { groupID: string }) => void>();
});
afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
async function open(group = false) {
  const props = {
    ref, isSearchGroup: group, openUserCardWithID: openUser,
    // BEFORE's callback name differs. Give it a valid, explicit boundary rather
    // than allowing undefined callback/type errors to masquerade as a RED.
    openUserCardWithData: (data: { userID?: unknown }) => {
      if (typeof data.userID === "string") openUser(data.userID);
    },
    openGroupCardWithData: openGroup,
  };
  await act(async () => root.render(React.createElement(SearchUserOrGroup, props)));
  await act(async () => ref.current!.openOverlay());
}
async function type(value: string) {
  await act(async () => {
    const input = host.querySelector("input")!;
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
async function confirm() {
  const button = [...host.querySelectorAll("button")].find(item => item.textContent === "confirm")!;
  expect(button).toBeDefined();
  await act(async () => button.click());
}
const deferred = () => {
  let resolve!: (value: any) => void;
  const promise = new Promise<any>(value => { resolve = value; });
  return { promise, resolve };
};

describe("actual contact lookup entry", () => {
  it("routes default ID entry through exact lookup and opens only target userID", async () => {
    await open(); await type("7031156314"); await confirm();
    expect(fixture.find.mock.calls.length + fixture.phone.mock.calls.length).toBe(1);
    if (fixture.phone.mock.calls.length)
      expect(fixture.phone).toHaveBeenCalledExactlyOnceWith("7031156314");
    expect(fixture.find).toHaveBeenCalledExactlyOnceWith(["7031156314"]);
    expect(fixture.phone).not.toHaveBeenCalled();
    expect(openUser).toHaveBeenCalledExactlyOnceWith("7031156314");
    // There is no partial cardInfo passed to bypass UserCard's normal hydration.
    expect(openUser.mock.calls[0]).toEqual(["7031156314"]);
  });
  it("preserves explicit phone mode without a hidden ID fallback", async () => {
    fixture.phone.mockResolvedValue({ data: { total: 1, users: [{ userID: "phone-owner" }] } });
    await open();
    const button = [...host.querySelectorAll('[role="radio"]')].find(item => item.textContent === "placeholder.phoneNumber")!;
    expect(button).toBeDefined(); await act(async () => (button as HTMLButtonElement).click());
    await type("13800138000"); await confirm();
    expect(fixture.phone).toHaveBeenCalledExactlyOnceWith("13800138000");
    expect(fixture.find).not.toHaveBeenCalled();
    expect(openUser).toHaveBeenCalledExactlyOnceWith("phone-owner");
  });
  it("does not open wrong, missing or non-unique exact lookup results", async () => {
    fixture.find.mockResolvedValue({ data: { users: [{ userID: "other" }] } });
    await open(); await type("7031156314"); await confirm();
    expect(openUser).not.toHaveBeenCalled();
    expect(fixture.warning).toHaveBeenCalledWith("toast.contactLookupAmbiguous");
  });
  it("drops a lookup response after the user closes its real overlay", async () => {
    const slow = deferred(); fixture.find.mockReturnValue(slow.promise);
    await open(); await type("7031156314"); await confirm();
    expect(fixture.find).toHaveBeenCalledExactlyOnceWith(["7031156314"]);
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="close lookup"]')!.click());
    await act(async () => slow.resolve({ data: { users: [{ userID: "7031156314" }] } }));
    expect(openUser).not.toHaveBeenCalled();
  });
  it("drops an old result when the input changes, without automatically resending", async () => {
    const slow = deferred(); fixture.find.mockReturnValue(slow.promise);
    await open(); await type("7031156314"); await confirm();
    expect(fixture.find).toHaveBeenCalledExactlyOnceWith(["7031156314"]);
    await type("8488972824");
    await act(async () => slow.resolve({ data: { users: [{ userID: "7031156314" }] } }));
    expect(openUser).not.toHaveBeenCalled(); expect(fixture.find).toHaveBeenCalledTimes(1);
  });
  it("drops an old owner result even if its component remains mounted", async () => {
    const slow = deferred(); fixture.find.mockReturnValue(slow.promise);
    await open(); await type("7031156314"); await confirm();
    expect(fixture.find).toHaveBeenCalledExactlyOnceWith(["7031156314"]);
    fixture.session.current = { owner: "B" }; ++fixture.session.generation;
    await act(async () => slow.resolve({ data: { users: [{ userID: "7031156314" }] } }));
    expect(openUser).not.toHaveBeenCalled(); expect(fixture.feedback).not.toHaveBeenCalled();
  });
  it("retains the exact group lookup path and does not call either user resolver", async () => {
    fixture.groups.mockResolvedValue({ data: [{ groupID: "group-one" }] });
    await open(true); await type("group-one"); await confirm();
    expect(fixture.groups).toHaveBeenCalledExactlyOnceWith(["group-one"]);
    expect(openGroup).toHaveBeenCalledExactlyOnceWith({ groupID: "group-one" });
    expect(fixture.find).not.toHaveBeenCalled(); expect(fixture.phone).not.toHaveBeenCalled();
  });
});
