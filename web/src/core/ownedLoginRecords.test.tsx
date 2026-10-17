// @vitest-environment jsdom
// Real login-record components and hook; only transport/session/store boundaries
// are adapted. No network, actual login, or private production record is used.
import React, { act, useLayoutEffect } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { BusinessUserInfo } from "@/api/login";
import type { Session } from "@/core/session/session";

const f = vi.hoisted(() => ({
  latest: vi.fn(), list: vi.fn(), listeners: new Set<() => void>(),
  current: undefined as Session | undefined, generation: 1,
}));
vi.mock("@/api/login", () => ({ getUserLatestLoginRecord: f.latest, getUserLoginRecords: f.list }));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: {
  get current() { return f.current; }, get generation() { return f.generation; },
  onChange(listener: () => void) { f.listeners.add(listener); return () => { f.listeners.delete(listener); }; },
} }));
vi.mock("@/store", async () => {
  const { create } = await import("zustand");
  return { useUserStore: create<{ selfInfo: BusinessUserInfo }>(() => ({ selfInfo: {} as BusinessUserInfo })) };
});

import { useUserStore } from "@/store";
import UserLoginInfo from "@/components/UserLoginInfo";
import UserLoginRecordList from "@/components/UserLoginRecordList";

type Pending<T> = {
  params: T; current: () => boolean;
  resolve(value: unknown): void; reject(error: Error): void; promise: Promise<unknown>;
};
const latest: Pending<API.User.GetLatestLoginRecordParams>[] = [];
const lists: Pending<API.User.GetLoginRecordsParams>[] = [];
const frames: { target: string; text: string }[] = [];
let root: Root | undefined, host: HTMLDivElement;
let logs: { mockRestore(): void }[];

const owner = (tenant = "tenant-a", issuer = "https://issuer.example"): Session => ({
  version: 1, issuer, userID: "viewer", imToken: "synthetic-im", chatToken: "synthetic-chat",
  tenant: { serverID: tenant, serverName: tenant, domains: ["tenant.example"], activeLine: 0 },
});
const viewer = (level = 100): BusinessUserInfo => ({
  userID: "viewer", level, password: "", account: "", phoneNumber: "", areaCode: "", email: "",
  nickname: "Viewer", faceURL: "", gender: 0, birth: 0, allowAddFriend: 1, allowBeep: 1,
  allowVibration: 1, globalRecvMsgOpt: 0,
});
const record = (userID: string, ip: string): API.User.UserLoginRecord => ({
  userID, ip, loginTime: Date.UTC(2026, 9, 17), deviceID: "synthetic-device", platform: "Web",
  deviceModel: "Synthetic browser", location: "Synthetic region", isMultiDevice: false, otherUserIDs: [],
});
function pending<T>(params: T, current: () => boolean): Pending<T> {
  let resolve!: (value: unknown) => void, reject!: (error: Error) => void;
  const promise = new Promise<unknown>((yes, no) => { resolve = yes; reject = no; });
  return { params, current, promise, resolve, reject };
}
async function flush() { await Promise.resolve(); await Promise.resolve(); }
function Frame({ target, kind }: { target: string; kind: "latest" | "list" | "both" }) {
  useLayoutEffect(() => { frames.push({ target, text: host.textContent ?? "" }); });
  return <>{kind !== "list" && <UserLoginInfo userID={target} />}
    {kind !== "latest" && <UserLoginRecordList userID={target} />}</>;
}
async function render(target: string, kind: "latest" | "list" | "both" = "both") {
  if (!root) {
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  }
  await act(async () => {
    root!.render(<Frame target={target} kind={kind} />);
    await flush();
  });
}
async function stop() { if (root) await act(async () => { root!.unmount(); }); root = undefined; host?.remove(); }
async function reply(call: Pending<unknown>, value: unknown) {
  await act(async () => { call.resolve(value); await flush(); });
}
async function fail(call: Pending<unknown>) {
  await act(async () => { call.reject(new Error("synthetic unavailable")); await flush(); });
}
async function nextPage() {
  const buttons = [...host.querySelectorAll<HTMLButtonElement>("button")].filter(b => b.textContent === "下一页 →");
  expect(buttons).toHaveLength(1); expect(buttons[0].disabled).toBe(false);
  await act(async () => { buttons[0].click(); await flush(); });
}
function noContent(...values: string[]) { for (const value of values) expect(host.textContent).not.toContain(value); }
function changedOwner() { f.current = owner("tenant-b", "https://other-issuer.example"); f.generation++; for (const notify of f.listeners) notify(); }

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  latest.length = 0; lists.length = 0; frames.length = 0; f.listeners.clear(); f.current = owner(); f.generation = 1;
  f.latest.mockReset(); f.list.mockReset(); useUserStore.setState({ selfInfo: viewer() });
  f.latest.mockImplementation((params: API.User.GetLatestLoginRecordParams, current: () => boolean) => {
    const call = pending(params, current); latest.push(call); return call.promise;
  });
  f.list.mockImplementation((params: API.User.GetLoginRecordsParams, current: () => boolean) => {
    const call = pending(params, current); lists.push(call); return call.promise;
  });
  logs = [vi.spyOn(console, "log").mockImplementation(() => {}), vi.spyOn(console, "info").mockImplementation(() => {}),
    vi.spyOn(console, "warn").mockImplementation(() => {}), vi.spyOn(console, "error").mockImplementation(() => {})];
});
afterEach(async () => { await stop(); for (const log of logs) log.mockRestore(); });

describe("real owned login-record presentation", () => {
  it("does not request records for an unauthorized viewer or log record contents", async () => {
    useUserStore.setState({ selfInfo: { ...viewer(20), userRole: "administrator" } });
    await render("target");
    expect(f.latest).not.toHaveBeenCalled(); expect(f.list).not.toHaveBeenCalled();
    expect(host.textContent).toContain("仅管理员可查看登录记录");
    await act(async () => { useUserStore.setState({ selfInfo: { ...viewer(), userID: "unbound-viewer" } }); });
    expect(f.latest).not.toHaveBeenCalled(); expect(f.list).not.toHaveBeenCalled();
    await act(async () => { useUserStore.setState({ selfInfo: viewer() }); });
    expect(latest).toHaveLength(1); expect(lists).toHaveLength(1);
    expect(latest[0].current()).toBe(true); expect(lists[0].current()).toBe(true);
    await reply(latest[0], { data: { record: record("target", "198.51.100.7") } });
    await reply(lists[0], { data: { records: [record("target", "198.51.100.8")], total: 1 } });
    expect(host.textContent).toContain("198.51.100.7"); expect(host.textContent).toContain("198.51.100.8");
    expect(host.textContent).toContain("Synthetic region");
    for (const log of logs) expect(log).not.toHaveBeenCalled();
    const before = [f.latest.mock.calls.length, f.list.mock.calls.length];
    await act(async () => { useUserStore.setState({ selfInfo: { ...viewer(), nickname: "Renamed" } }); });
    expect([f.latest.mock.calls.length, f.list.mock.calls.length]).toEqual(before);
  });

  it("keeps only the latest target record and clears failed or mismatched replacements", async () => {
    await render("alpha", "latest");
    expect(latest[0].params).toEqual({ userID: "alpha" });
    await reply(latest[0], { data: { record: record("alpha", "198.51.100.11") } });
    expect(host.textContent).toContain("198.51.100.11");
    await render("beta", "latest"); noContent("198.51.100.11");
    expect(frames.filter(frame => frame.target === "beta")).toHaveLength(1);
    expect(frames.find(frame => frame.target === "beta")?.text).not.toContain("198.51.100.11");
    await render("gamma", "latest"); expect(latest).toHaveLength(3);
    expect(latest[1].current()).toBe(false); expect(latest[2].current()).toBe(true);
    await reply(latest[2], { data: { record: record("gamma", "198.51.100.13") } });
    await reply(latest[1], { data: { record: record("beta", "198.51.100.12") } });
    expect(host.textContent).toContain("198.51.100.13"); noContent("198.51.100.11", "198.51.100.12");
    await render("delta", "latest"); noContent("198.51.100.13");
    await fail(latest[3]); expect(host.textContent).toContain("登录信息暂时无法加载。");
    await render("epsilon", "latest");
    await reply(latest[4], { data: { record: record("substitute", "198.51.100.99") } });
    noContent("198.51.100.99"); expect(host.textContent).toContain("登录信息暂时无法加载。");
  });

  it("resets record pages per target and hides old pages while loading or after failure", async () => {
    await render("alpha", "list");
    expect(lists[0].params).toEqual({ userID: "alpha", pagination: { pageNumber: 1, showNumber: 10 } });
    await reply(lists[0], { data: { records: [record("alpha", "198.51.100.21")], total: 25 } });
    await nextPage(); expect(lists).toHaveLength(2); noContent("198.51.100.21");
    expect(host.textContent).toContain("加载中...");
    expect(lists[1].params).toEqual({ userID: "alpha", pagination: { pageNumber: 2, showNumber: 10 } });
    await reply(lists[1], { data: { records: [record("alpha", "198.51.100.22")], total: 25 } });
    expect(host.textContent).toContain("198.51.100.22"); expect(host.textContent).toContain("2 / 3");
    await render("beta", "list"); noContent("198.51.100.21", "198.51.100.22");
    expect(frames.filter(frame => frame.target === "beta")).toHaveLength(1);
    expect(frames.find(frame => frame.target === "beta")?.text).not.toContain("198.51.100.22");
    expect(lists[2].params).toEqual({ userID: "beta", pagination: { pageNumber: 1, showNumber: 10 } });
    await fail(lists[2]); expect(host.textContent).toContain("登录记录暂时无法加载。");
    await render("gamma", "list");
    await reply(lists[3], { data: { records: [record("substitute", "198.51.100.98")], total: 1 } });
    noContent("198.51.100.98"); expect(host.textContent).toContain("登录记录暂时无法加载。");
  });

  it("retires same-ID pending records after owner generation level or mounted lifetime changes", async () => {
    for (const reason of ["owner", "generation", "level", "unmount"] as const) {
      await stop(); latest.length = 0; lists.length = 0; f.current = owner(); f.generation++;
      useUserStore.setState({ selfInfo: viewer() }); await render("same-target");
      const oldLatest = latest[0], oldList = lists[0];
      expect(oldLatest.current()).toBe(true); expect(oldList.current()).toBe(true);
      if (reason === "unmount") await stop();
      else await act(async () => {
        if (reason === "owner") changedOwner();
        else if (reason === "generation") { f.generation++; for (const notify of f.listeners) notify(); }
        else {
          useUserStore.setState({ selfInfo: viewer(20) });
          expect(oldLatest.current()).toBe(false); expect(oldList.current()).toBe(false);
          useUserStore.setState({ selfInfo: viewer() });
        }
        await flush();
      });
      expect(oldLatest.current()).toBe(false); expect(oldList.current()).toBe(false);
      if (reason !== "unmount") {
        expect(latest).toHaveLength(2); expect(lists).toHaveLength(2);
        expect(latest[1].current()).toBe(true); expect(lists[1].current()).toBe(true);
        await reply(latest[1], { data: { record: record("same-target", "198.51.100.31") } });
        await reply(lists[1], { data: { records: [record("same-target", "198.51.100.32")], total: 1 } });
      }
      await reply(oldLatest, { data: { record: record("same-target", "198.51.100.33") } });
      await reply(oldList, { data: { records: [record("same-target", "198.51.100.34")], total: 1 } });
      if (reason === "unmount") expect(document.body.textContent).not.toContain("198.51.100.33");
      else {
        noContent("198.51.100.33", "198.51.100.34");
        expect(host.textContent).toContain("198.51.100.31"); expect(host.textContent).toContain("198.51.100.32");
      }
    }
  });
});
