// @vitest-environment jsdom
// Actual editor; only AntD input transport, click-away and session/feedback are adapted.
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
const f = vi.hoisted(() => ({
  owner: { userID: "actor", issuer: "issuer-A", tenant: { serverID: "A" } }, generation: 1,
  listeners: new Set<() => void>(), feedback: vi.fn(), away: undefined as (() => void) | undefined,
  enter: undefined as ((event: { target: { value: string } }) => Promise<void>) | undefined,
  change: undefined as ((event: { target: { value: string } }) => void) | undefined,
}));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: {
  get current() { return f.owner; }, get generation() { return f.generation; },
  onChange(callback: () => void) { f.listeners.add(callback); return () => f.listeners.delete(callback); },
} }));
vi.mock("@/utils/common", () => ({ feedbackToast: f.feedback }));
vi.mock("ahooks", () => ({ useClickAway: (callback: () => void) => { f.away = callback; } }));
vi.mock("@ant-design/icons", () => ({ EnterOutlined: () => null }));
vi.mock("antd", () => ({ Input: React.forwardRef<HTMLInputElement, {
  value?: string; disabled?: boolean; "aria-busy"?: boolean;
  onChange: typeof f.change; onPressEnter: typeof f.enter;
}>((props, ref) => {
  f.enter = props.onPressEnter; f.change = props.onChange;
  return React.createElement("input", { ref, value: props.value ?? "", disabled: props.disabled,
    "aria-busy": props["aria-busy"], onChange: () => {} });
}) }));
import EditableContent from "@/components/EditableContent";
let host: HTMLDivElement, root: ReturnType<typeof createRoot>;
const flush = async () => { for (let n = 0; n < 8; n++) await Promise.resolve(); };
function deferred<T>() { let resolve!: (value: T) => void, reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
const input = () => host.querySelector<HTMLInputElement>("input");
async function render(props: React.ComponentProps<typeof EditableContent>) {
  await act(async () => { root.render(React.createElement(EditableContent, { editable: true, value: "old", editIdentity: "A", ...props })); });
}
async function edit(value = "draft") {
  const icon = host.querySelector<HTMLImageElement>('img[alt="edit name"]'); expect(icon).not.toBeNull();
  await act(async () => { icon!.click(); });
  await act(async () => { f.change!({ target: { value } }); });
}
async function submit(value = "draft") { await act(async () => { await f.enter!({ target: { value } }); }); }
beforeEach(() => { vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  f.owner = { userID: "actor", issuer: "issuer-A", tenant: { serverID: "A" } }; f.generation++; f.listeners.clear();
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

describe("actual EditableContent save lifecycle", () => {
  it("keeps a historical void callback successful", async () => {
    const save = vi.fn(async () => {}); await render({ onChange: save }); await edit(); await submit();
    expect(save).toHaveBeenCalledExactlyOnceWith("draft"); expect(input()).toBeNull();
  });
  it("keeps explicit success successful", async () => {
    await render({ onChange: async () => true }); await edit(); await submit(); expect(input()).toBeNull();
  });
  it("keeps a false/busy result draft and allows an explicit later retry", async () => {
    const save = vi.fn().mockResolvedValueOnce(false).mockResolvedValueOnce(true);
    await render({ onChange: save }); await edit(); await expect(submit()).resolves.toBeUndefined();
    expect(input()?.value).toBe("draft"); expect(input()?.disabled).toBe(false);
    await submit(); expect(save).toHaveBeenCalledTimes(2); expect(input()).toBeNull();
  });
  it("catches rejection, releases busy and retains the exact draft", async () => {
    const error = new Error("write rejected"), save = vi.fn().mockRejectedValueOnce(error).mockResolvedValueOnce(true);
    await render({ onChange: save }); await edit(); await expect(submit()).resolves.toBeUndefined();
    expect(input()?.value).toBe("draft"); expect(input()?.disabled).toBe(false);
    expect(f.feedback).toHaveBeenCalledExactlyOnceWith({ error });
    await submit(); expect(save).toHaveBeenCalledTimes(2); expect(input()).toBeNull();
  });
  it("admits only one Enter synchronously while its save is pending", async () => {
    const slow = deferred<boolean>(), save = vi.fn(() => slow.promise); let first!: Promise<void>, second!: Promise<void>;
    await render({ onChange: save }); await edit(); const enter = f.enter!;
    try {
      await act(async () => { first = enter({ target: { value: "first" } }); second = enter({ target: { value: "second" } }); await flush(); });
      expect(save).toHaveBeenCalledExactlyOnceWith("first"); expect(input()?.disabled).toBe(true);
    } finally { await act(async () => { slow.resolve(true); await Promise.all([first, second]); }); }
    expect(input()).toBeNull();
  });
  it("does not replace the submitted value by a later mutable event value", async () => {
    const slow = deferred<boolean>(), save = vi.fn(() => slow.promise), event = { target: { value: "submitted" } }; let pending!: Promise<void>;
    await render({ onChange: save }); await edit();
    try { await act(async () => { pending = f.enter!(event); await flush(); }); event.target.value = "later";
      expect(save).toHaveBeenCalledExactlyOnceWith("submitted");
    } finally { await act(async () => { slow.resolve(false); await pending; }); }
    expect(input()?.value).toBe("submitted");
  });
  it("does not abandon a normal save when the same target's authoritative value arrives before ACK", async () => {
    const slow = deferred<boolean>(), save = vi.fn(() => slow.promise); let pending!: Promise<void>;
    await render({ onChange: save }); await edit();
    try { await act(async () => { pending = f.enter!({ target: { value: "draft" } }); await flush(); });
      await render({ onChange: save, value: "draft" }); expect(input()?.value).toBe("draft");
    } finally { await act(async () => { slow.resolve(true); await pending; }); }
    expect(input()).toBeNull(); expect(host.textContent).toBe("draft");
  });
  it("keeps the unsaved draft through an unrelated same-target metadata render", async () => {
    await render({ onChange: async () => true }); await edit();
    await render({ value: "server event", onChange: async () => true }); expect(input()?.value).toBe("draft");
  });
  it("does not close a cancelled and reopened editor when an older save finishes", async () => {
    const slow = deferred<boolean>(); let pending!: Promise<void>;
    await render({ onChange: () => slow.promise }); await edit("first");
    try { await act(async () => { pending = f.enter!({ target: { value: "first" } }); await flush(); });
      await act(async () => { f.away!(); }); await edit("second");
    } finally { await act(async () => { slow.resolve(true); await pending; }); }
    expect(input()?.value).toBe("second"); expect(input()?.disabled).toBe(false);
  });
  it("distinguishes different targets even when their displayed value is equal", async () => {
    const slow = deferred<boolean>(); let pending!: Promise<void>;
    await render({ onChange: () => slow.promise }); await edit("old draft");
    try { await act(async () => { pending = f.enter!({ target: { value: "old draft" } }); await flush(); });
      await render({ editIdentity: "B", onChange: async () => true }); await edit("new draft");
    } finally { await act(async () => { slow.resolve(true); await pending; }); }
    expect(input()?.value).toBe("new draft");
  });
  it("does not dispatch a retained old editor callback into the new target", async () => {
    const saveA = vi.fn(async () => true), saveB = vi.fn(async () => true);
    await render({ onChange: saveA }); await edit(); const old = f.enter!;
    await render({ editIdentity: "B", onChange: saveB }); await edit("B draft");
    await act(async () => { await old({ target: { value: "A draft" } }); });
    expect(saveA).not.toHaveBeenCalled(); expect(saveB).not.toHaveBeenCalled(); expect(input()?.value).toBe("B draft");
  });
  it.each(["owner", "generation", "issuer", "tenant"] as const)("does not dispatch an old editor after %s changes", async (kind) => {
    const save = vi.fn(async () => true); await render({ onChange: save }); await edit(); const old = f.enter!;
    if (kind === "owner") f.owner = { ...f.owner }; else if (kind === "generation") ++f.generation;
    else if (kind === "issuer") f.owner.issuer = "issuer-B"; else f.owner.tenant.serverID = "B";
    await act(async () => { await old({ target: { value: "draft" } }); }); expect(save).not.toHaveBeenCalled();
  });
  it("does not project a late rejection over a new target editor", async () => {
    const slow = deferred<boolean>(); let pending!: Promise<void>, rejected: unknown;
    await render({ onChange: () => slow.promise }); await edit();
    try { await act(async () => { pending = f.enter!({ target: { value: "draft" } }); await flush(); });
      await render({ editIdentity: "B", onChange: async () => true }); await edit("new");
    } finally { await act(async () => { slow.reject(new Error("old failure")); await pending.catch((error: unknown) => { rejected = error; }); }); }
    expect(rejected).toBeUndefined(); expect(f.feedback).not.toHaveBeenCalled(); expect(input()?.value).toBe("new");
  });
  it("keeps an active draft but rejects submit after editing permission is withdrawn", async () => {
    const save = vi.fn(async () => true); await render({ onChange: save }); await edit(); await render({ editable: false, onChange: save });
    await submit(); expect(save).not.toHaveBeenCalled(); expect(input()?.value).toBe("draft");
  });
});
