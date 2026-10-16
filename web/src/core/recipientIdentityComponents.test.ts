// @vitest-environment jsdom
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixture = vi.hoisted(() => ({ recent: [] as any[] }));
vi.mock("open-im-sdk-wasm", () => ({ SessionType: { Single: 1, WorkingGroup: 3 } }));
vi.mock("i18next", () => ({ default: { on: vi.fn() }, t: (key: string) => key }));
vi.mock("@/components/OIMAvatar", () => ({ default: () => null }));
vi.mock("@ant-design/icons", () => ({
  SearchOutlined: () => null,
  RightOutlined: () => null,
  CloseOutlined: () => null,
}));
vi.mock("antd", () => ({
  Checkbox: ({ checked, disabled }: any) =>
    React.createElement("input", {
      type: "checkbox",
      checked,
      disabled,
      readOnly: true,
    }),
  Input: ({ value, onChange, onPressEnter, disabled }: any) =>
    React.createElement("input", {
      value,
      onChange,
      disabled,
      "aria-label": "recipient search",
      onKeyDown: (event: React.KeyboardEvent) => {
        if (event.key === "Enter") onPressEnter?.();
      },
    }),
  Breadcrumb: () => null,
}));
vi.mock("react-virtuoso", () => ({
  Virtuoso: ({ data, itemContent }: any) =>
    React.createElement(
      "div",
      null,
      data.map((item: any, index: number) =>
        React.createElement(React.Fragment, { key: index }, itemContent(index, item)),
      ),
    ),
}));
vi.mock("@/hooks/useCurrentMemberRole", () => ({ useCurrentMemberRole: () => ({}) }));
vi.mock("@/hooks/useGroupMembers", () => ({
  default: () => ({ fetchState: { groupMemberList: [], searchMemberList: [] } }),
  REACH_SEARCH_FLAG: "last",
}));
vi.mock("@/layout/MainContentWrap", () => ({
  IMSDK: { getSpecifiedGroupMembersInfo: vi.fn() },
}));
vi.mock("@/utils/common", () => ({ feedbackToast: vi.fn() }));
vi.mock("@/store", () => ({
  useUserStore: {},
  useConversationStore: { getState: () => ({ conversationList: fixture.recent }) },
}));
vi.mock("@/store/contact", () => ({
  useContactStore: { getState: () => ({ friendList: [], groupList: [] }) },
}));

import CheckItem from "@/pages/common/ChooseModal/ChooseBox/CheckItem";
import ChooseBox from "@/pages/common/ChooseModal/ChooseBox";

let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  fixture.recent = [
    { conversationID: "si_first", userID: "7031156314", showName: "同名联系人" },
    { conversationID: "si_second", userID: "8488972824", showName: "同名联系人" },
    {
      conversationID: "sg_group",
      groupID: "group-distinct",
      showName: "同名联系人",
      conversationType: 3,
    },
  ];
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});
async function render(element: React.ReactNode) {
  await act(async () => root.render(element));
}
async function search(keyword: string) {
  const input = host.querySelector('input[aria-label="recipient search"]')!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(
      input,
      keyword,
    );
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () =>
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })),
  );
}
async function openRecent() {
  await render(React.createElement(ChooseBox));
  const menu = [...host.querySelectorAll("div")].find(
    (node) =>
      node.textContent === "placeholder.latestChat" && node.children.length === 0,
  )!;
  expect(menu).toBeDefined();
  await act(async () => menu.click());
}

describe("actual recipient identity presentation and recent-chat search", () => {
  it("distinguishes two recipients with the same name using their visible IDs", async () => {
    await render(
      React.createElement(
        React.Fragment,
        null,
        ...fixture.recent
          .slice(0, 2)
          .map((data) => React.createElement(CheckItem, { key: data.userID, data })),
      ),
    );
    expect(host.textContent).toContain("7031156314");
    expect(host.textContent).toContain("8488972824");
    expect(host.textContent?.match(/同名联系人/g)).toHaveLength(2);
  });
  it("shows a group ID, while a group member is identified by their user ID", async () => {
    await render(
      React.createElement(CheckItem, {
        data: { groupID: "group-distinct", groupName: "项目群" },
      }),
    );
    expect(host.textContent).toContain("group-distinct");
    await render(
      React.createElement(CheckItem, {
        data: { groupID: "parent-group", userID: "member-distinct", nickname: "成员" },
      }),
    );
    expect(host.textContent).toContain("member-distinct");
    expect(host.textContent).not.toContain("parent-group");
  });
  it("keeps a recipient recognizable when its name is missing", async () => {
    await render(
      React.createElement(CheckItem, { data: { userID: "name-not-loaded" } }),
    );
    expect(host.textContent).toContain("name-not-loaded");
  });
  it("finds the exact recent recipient by user ID and retains only that row", async () => {
    await openRecent();
    await search("7031156314");
    expect(host.querySelectorAll('input[type="checkbox"]')).toHaveLength(1);
    expect(host.textContent).toContain("7031156314");
    expect(host.textContent).not.toContain("8488972824");
  });
  it("finds recent groups by ID regardless of case and surrounding whitespace", async () => {
    await openRecent();
    await search("  GROUP-DISTINCT  ");
    expect(host.querySelectorAll('input[type="checkbox"]')).toHaveLength(1);
    expect(host.textContent).toContain("group-distinct");
  });
  it("preserves recent display-name search and does not search internal conversation IDs", async () => {
    await openRecent();
    await search("同名联系人");
    expect(host.querySelectorAll('input[type="checkbox"]')).toHaveLength(3);
    await search("si_first");
    expect(host.querySelectorAll('input[type="checkbox"]')).toHaveLength(0);
  });
  it("retains disabled-recipient protection after adding identity details", async () => {
    const choose = vi.fn();
    await render(
      React.createElement(CheckItem, {
        data: { userID: "disabled-user", nickname: "不可选", disabled: true },
        showCheck: true,
        itemClick: choose,
      }),
    );
    await act(async () =>
      host.firstElementChild!.dispatchEvent(new MouseEvent("click", { bubbles: true })),
    );
    expect(choose).not.toHaveBeenCalled();
  });
});
