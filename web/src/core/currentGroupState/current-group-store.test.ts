// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from "vitest";
import { conversation, enter, fixture, flush, group, member, resetFixture, store } from "./harness";

beforeEach(resetFixture);

describe("real current-group Zustand store", () => {
  it("publishes the new conversation before SDK dispatch and hydrates both identities", async () => {
    let dispatchedCID: string | undefined;
    fixture.hooks.onGroupDispatch = () => { dispatchedCID = store.getState().currentConversation?.conversationID; };
    enter();
    expect(dispatchedCID).toBe("cid-A");
    expect(fixture.members[0].args).toEqual({ groupID: "A", userIDList: ["actor-a"] });
    fixture.groups[0].resolve({ data: [group()] });
    fixture.members[0].resolve({ data: [member()] });
    await flush();
    expect(store.getState().currentGroupInfo?.groupID).toBe("A");
    expect(store.getState().currentMemberInGroup?.userID).toBe("actor-a");
  });

  it("clears both old authority objects synchronously on switch and leaves single chat empty", () => {
    enter();
    store.setState({ currentGroupInfo: group(), currentMemberInGroup: member() });
    enter("B");
    expect(store.getState().currentGroupInfo).toBeUndefined();
    expect(store.getState().currentMemberInGroup).toBeUndefined();
    store.setState({ currentGroupInfo: group("B"), currentMemberInGroup: member("B") });
    store.getState().updateCurrentConversation({ ...conversation(), conversationID: "single", conversationType: 1, groupID: "", userID: "peer" });
    expect(store.getState().currentGroupInfo).toBeUndefined();
    expect(store.getState().currentMemberInGroup).toBeUndefined();
  });

  it("keeps valid same-group state on conversation metadata refresh without redundant GETs", () => {
    enter();
    const known = group("A", 3);
    store.setState({ currentGroupInfo: known, currentMemberInGroup: member() });
    store.getState().updateCurrentConversation({ ...conversation(), showName: "renamed" });
    expect(store.getState().currentGroupInfo).toBe(known);
    expect(fixture.groups).toHaveLength(1);
    expect(fixture.members).toHaveLength(1);
  });

  for (const kind of ["group", "member"] as const) {
    const reads = () => kind === "group" ? fixture.groups : fixture.members;
    const value = (id: string) => kind === "group" ? group(id) : member(id);
    const result = () => kind === "group" ? store.getState().currentGroupInfo : store.getState().currentMemberInGroup;
    const refresh = () => kind === "group" ? store.getState().getCurrentGroupInfoByReq("A") : store.getState().getCurrentMemberInGroupByReq("A");

    it(`${kind}: A to B to A ignores the first A response`, async () => {
      enter(); enter("B"); enter();
      reads()[2].resolve({ data: [value("A")] }); await flush();
      const accepted = result();
      reads()[0].resolve({ data: [{ ...value("A"), nickname: "obsolete", groupName: "obsolete" }] }); await flush();
      expect(result()).toBe(accepted);
    });
    it(`${kind}: latest same-group request wins`, async () => {
      enter(); const first = refresh(); const second = refresh();
      reads()[2].resolve({ data: [{ ...value("A"), nickname: "latest", groupName: "latest" }] }); await second;
      const accepted = result();
      reads()[1].resolve({ data: [value("A")] }); await first;
      expect(result()).toBe(accepted);
    });
    it(`${kind}: obsolete failure cannot toast or erase the new view`, async () => {
      enter(); enter("B");
      reads()[1].resolve({ data: [value("B")] }); await flush();
      const accepted = result();
      reads()[0].reject(new Error("synthetic old failure")); await flush();
      expect(result()).toBe(accepted);
      expect(fixture.toast).not.toHaveBeenCalled();
    });
    it(`${kind}: accepted current event wins over an older GET`, async () => {
      enter();
      store.setState({ currentGroupInfo: group(), currentMemberInGroup: member() });
      if (kind === "group") store.getState().updateCurrentGroupInfo(group("A", 3));
      else store.getState().tryUpdateCurrentMemberInGroup(member("A", "actor-a", 100));
      const accepted = result();
      reads()[0].resolve({ data: [value("A")] }); await flush();
      expect(result()).toBe(accepted);
    });
    it(`${kind}: switching to a single chat rejects a late group response`, async () => {
      enter();
      store.getState().updateCurrentConversation({ ...conversation(), conversationID: "single", conversationType: 1, groupID: "", userID: "peer" });
      reads()[0].resolve({ data: [value("A")] }); await flush();
      expect(result()).toBeUndefined();
    });
    it(`${kind}: existing SDK generation protection still rejects a previous owner result`, async () => {
      enter(); fixture.session.generation += 1; fixture.session.current = fixture.owner();
      reads()[0].resolve({ data: [value("A")] }); await flush();
      expect(result()).toBeUndefined();
    });
  }

  it("public getters refuse no active group and wrong-group admission", async () => {
    void store.getState().getCurrentGroupInfoByReq("A");
    void store.getState().getCurrentMemberInGroupByReq("A");
    expect(fixture.groups).toHaveLength(0); expect(fixture.members).toHaveLength(0);
    enter();
    void store.getState().getCurrentGroupInfoByReq("B");
    void store.getState().getCurrentMemberInGroupByReq("B");
    expect(fixture.groups).toHaveLength(1); expect(fixture.members).toHaveLength(1);
  });
  it("wrong-group and wrong-actor events cannot change current authority", () => {
    enter(); store.setState({ currentGroupInfo: group(), currentMemberInGroup: member() });
    store.getState().updateCurrentGroupInfo(group("B", 3));
    store.getState().tryUpdateCurrentMemberInGroup(member("A", "other", 100));
    store.getState().tryUpdateCurrentMemberInGroup(member("B", "actor-a", 100));
    expect(store.getState().currentGroupInfo?.groupID).toBe("A");
    expect(store.getState().currentMemberInGroup?.roleLevel).toBe(20);
  });
  it("legitimate current self event can bootstrap before the first GET", () => {
    enter(); store.getState().tryUpdateCurrentMemberInGroup(member("A", "actor-a", 60));
    expect(store.getState().currentMemberInGroup?.roleLevel).toBe(60);
  });
  it("empty member result removes previous membership and role", async () => {
    enter(); store.setState({ currentMemberInGroup: member("A", "actor-a", 100) });
    fixture.members[0].resolve({ data: [] }); await flush();
    expect(store.getState().currentMemberInGroup).toBeUndefined();
  });
  it("wrong member identity or current error removes old authority", async () => {
    enter(); store.setState({ currentMemberInGroup: member("A", "actor-a", 100) });
    fixture.members[0].resolve({ data: [member("A", "other", 100)] }); await flush();
    expect(store.getState().currentMemberInGroup).toBeUndefined();
    store.setState({ currentMemberInGroup: member("A", "actor-a", 100) });
    const read = store.getState().getCurrentMemberInGroupByReq("A");
    fixture.members[1].reject(new Error("synthetic current failure")); await read;
    expect(store.getState().currentMemberInGroup).toBeUndefined();
    expect(fixture.toast).toHaveBeenCalledTimes(2);
  });
  for (const response of ["error", "malformed", "wrong group"] as const) {
    it(`same-group ${response} preserves known whole-group mute`, async () => {
      enter(); const known = group("A", 3); store.setState({ currentGroupInfo: known });
      if (response === "error") fixture.groups[0].reject(new Error("synthetic refresh failure"));
      else fixture.groups[0].resolve({ data: response === "malformed" ? [{}] : [group("B")] });
      await flush();
      expect(store.getState().currentGroupInfo).toBe(known);
      expect(fixture.toast).toHaveBeenCalledTimes(1);
    });
  }
  it("owner bootstrap accepts empty selfInfo only until it identifies a different account", async () => {
    fixture.user.selfInfo.userID = ""; enter();
    fixture.user.selfInfo.userID = "actor-a";
    fixture.groups[0].resolve({ data: [group()] }); await flush();
    expect(store.getState().currentGroupInfo?.groupID).toBe("A");
    store.getState().clearConversationStore(); fixture.user.selfInfo.userID = ""; enter();
    fixture.user.selfInfo.userID = "different";
    fixture.groups[1].resolve({ data: [group()] }); await flush();
    expect(store.getState().currentGroupInfo).toBeUndefined();
  });
  it("same-generation owner replacement or store clear invalidates pending reads", async () => {
    enter(); fixture.session.current = fixture.owner();
    fixture.groups[0].resolve({ data: [group()] }); await flush();
    expect(store.getState().currentGroupInfo).toBeUndefined();
    enter(); store.getState().clearConversationStore();
    fixture.members[1].resolve({ data: [member()] }); await flush();
    expect(store.getState().currentMemberInGroup).toBeUndefined();
  });
});
