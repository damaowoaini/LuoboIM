// @vitest-environment jsdom
import React, { act } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { deferred, emit, enter, f, flush, member, owner, reset, store, surface } from "./fixture";
import useGroupMembers from "@/hooks/useGroupMembers";
let ui: ReturnType<typeof surface>, current: (() => boolean) | undefined;
function Probe({ groupID = "A" }: { groupID?: string }) { f.hook = useGroupMembers({ groupID, current }); return null; }
async function render(groupID = "A") { await ui.render(React.createElement(Probe, { groupID })); }
beforeEach(async () => { await reset(); ui = surface(); current = store.getState().captureConversationSendScope()!.current; });
afterEach(async () => { await ui.dispose(); });
const rows = () => f.hook.fetchState.groupMemberList;
async function load() { await render(); await act(async () => { await f.hook.getMemberData(true); }); }

describe("real member hook owner/nav/read provenance", () => {
  it("preserves normal list reads and explicit group-card reads outside the current conversation", async () => {
    current = undefined; await enter("B"); await render("A"); await act(async () => { await f.hook.getMemberData(true); });
    expect(f.list).toHaveBeenCalledExactlyOnceWith({ groupID: "A", offset: 0, count: 20, filter: 0 });
    expect(rows().map((m: any) => m.userID)).toEqual(["peer"]);
  });
  it("retains same-owner rows after an ordinary refresh failure", async () => {
    await load(); f.list.mockRejectedValueOnce(new Error("current refresh"));
    await act(async () => { await f.hook.getMemberData(true); });
    expect(rows().map((m: any) => m.userID)).toEqual(["peer"]); expect(f.toast).toHaveBeenCalledOnce();
  });
  it("preserves the joined-group refresh event whose GroupItem has no member UID", async () => {
    await load(); await act(async () => { emit("joined", { groupID: "A" }); await flush(); });
    expect(f.list).toHaveBeenCalledTimes(2); expect(rows().map((m: any) => m.userID)).toEqual(["peer"]);
  });
  it("updates both real list and search role facts on the same SDK member event", async () => {
    await load(); await act(async () => { await f.hook.searchMember("peer"); emit("changed", member("peer", 60)); });
    expect(rows()[0].roleLevel).toBe(60); expect(f.hook.fetchState.searchMemberList[0].roleLevel).toBe(60);
  });
  it("ignores info changes for an unloaded UID instead of creating array[-1]", async () => {
    await load(); await act(async () => emit("changed", member("not-loaded", 60)));
    expect(Object.keys(rows())).not.toContain("-1"); expect(rows().map((m: any) => m.userID)).toEqual(["peer"]);
  });
  it("invalidates an initial pending page when a not-yet-loaded peer is promoted, without inserting an unknown row", async () => {
    await render(); const slow = deferred(); f.list.mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    await act(async () => { pending = f.hook.getMemberData(true); }); expect(f.list).toHaveBeenCalledOnce();
    await act(async () => emit("changed", member("peer", 60)));
    const loadingAfterEvent = f.hook.fetchState.loading;
    try { expect(rows()).toEqual([]); }
    finally { await act(async () => { slow.resolve({ data: [member()] }); await pending; }); }
    expect(rows()).toEqual([]); expect(loadingAfterEvent).toBe(false);
  });
  it("lets a newer role event win over an older in-flight GET", async () => {
    await load(); const slow = deferred(); f.list.mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    await act(async () => { pending = f.hook.getMemberData(true); }); expect(f.list).toHaveBeenCalledTimes(2);
    await act(async () => emit("changed", member("peer", 60)));
    await act(async () => { slow.resolve({ data: [member()] }); await pending; }); expect(rows()[0].roleLevel).toBe(60);
  });
  it("does not refill a reset list from its previously dispatched GET", async () => {
    await render(); const slow = deferred(); f.list.mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    await act(async () => { pending = f.hook.getMemberData(true); }); expect(f.list).toHaveBeenCalledOnce();
    await act(async () => f.hook.resetState());
    await act(async () => { slow.resolve({ data: [member()] }); await pending; }); expect(rows()).toEqual([]);
  });
  it("does not project an A-B-A stale group GET even though SDK generation did not change", async () => {
    await render(); const slow = deferred(); f.list.mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    await act(async () => { pending = f.hook.getMemberData(true); }); expect(f.list).toHaveBeenCalledOnce();
    await act(async () => { await enter("B"); await enter("A"); });
    await act(async () => { slow.resolve({ data: [member()] }); await pending; }); expect(rows()).toEqual([]);
  });
  it("drops a same-generation replacement owner's old read error instead of toasting it", async () => {
    await render(); const slow = deferred(); f.list.mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    await act(async () => { pending = f.hook.getMemberData(true); }); expect(f.list).toHaveBeenCalledOnce(); f.session.current = owner();
    await act(async () => { slow.reject(new Error("old owner")); await pending; }); expect(f.toast).not.toHaveBeenCalled();
  });
  it("clears A rows before B loading/error while retaining same-owner refresh behavior", async () => {
    await load(); f.session.current = owner(); await enter(); current = store.getState().captureConversationSendScope()!.current; await render();
    const slow = deferred(); f.list.mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    await act(async () => { pending = f.hook.getMemberData(true); });
    try { expect(f.list).toHaveBeenCalledTimes(2); expect(rows()).toEqual([]); }
    finally { await act(async () => { slow.reject(new Error("new owner read")); await pending; }); }
    expect(rows()).toEqual([]); expect(f.toast).toHaveBeenCalledOnce();
  });
  it("rechecks nav before dispatching the secondary remark lookup", async () => {
    await render(); f.friends = [{ userID: "remark-peer", remark: "target" }];
    const slow = deferred(); f.search.mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    await act(async () => { pending = f.hook.searchMember("target"); }); expect(f.search).toHaveBeenCalledOnce();
    await act(async () => { await enter("B"); });
    await act(async () => { slow.resolve({ data: [] }); await pending; });
    expect(f.specified.mock.calls.filter(([args]: any) => args.userIDList.includes("remark-peer"))).toHaveLength(0);
  });
  it("drops a secondary remark result that arrives after same-generation owner replacement", async () => {
    await render(); f.friends = [{ userID: "remark-peer", remark: "target" }]; f.search.mockResolvedValueOnce({ data: [] });
    const slow = deferred(); f.specified.mockReturnValueOnce(slow.promise); let pending!: Promise<void>;
    await act(async () => { pending = f.hook.searchMember("target"); await flush(); });
    expect(f.specified.mock.calls.filter(([args]: any) => args.userIDList.includes("remark-peer"))).toHaveLength(1);
    f.session.current = owner();
    await act(async () => { slow.resolve({ data: [member("remark-peer")] }); await pending; });
    expect(f.hook.fetchState.searchMemberList).toEqual([]);
  });
  it("keeps the latest keyword result when two actual searches settle out of order", async () => {
    await render(); const slow = deferred(); f.search.mockReturnValueOnce(slow.promise).mockResolvedValueOnce({ data: [member("new")] });
    let first!: Promise<void>;
    try { await act(async () => { first = f.hook.searchMember("old"); await f.hook.searchMember("new"); }); expect(f.search).toHaveBeenCalledTimes(2); }
    finally { await act(async () => { slow.resolve({ data: [member("old")] }); await first; }); }
    expect(f.hook.fetchState.searchMemberList.map((m: any) => m.userID)).toEqual(["new"]);
  });
  it("removes a deleted target from both lists before awaiting the refresh", async () => {
    await load(); await act(async () => { await f.hook.searchMember("peer"); }); const slow = deferred(); f.list.mockReturnValueOnce(slow.promise);
    await act(async () => emit("deleted", member()));
    try { expect(f.list).toHaveBeenCalledTimes(2); expect(rows()).toEqual([]); expect(f.hook.fetchState.searchMemberList).toEqual([]); }
    finally { await act(async () => { slow.resolve({ data: [] }); await flush(); }); }
  });
  it("does not dispatch a new read from a retained getter after actual hook unmount", async () => {
    await load(); const read = f.hook.getMemberData; await ui.dispose(); await read(true); expect(f.list).toHaveBeenCalledOnce();
  });
});
