// @vitest-environment jsdom
import "./query-chat-boundaries";
import React, { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { enter, fixture, flush, group, member, resetFixture, store } from "./harness";
import { QueryChat } from "@/pages/chat/queryChat";

let root: Root | undefined;
let host: HTMLDivElement;
beforeEach(() => { resetFixture(); (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; });
afterEach(() => { act(() => root?.unmount()); root = undefined; host?.remove(); });
async function renderChat(strict = true) {
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  await act(async () => { root!.render(strict ? <StrictMode><QueryChat /></StrictMode> : <QueryChat />); await flush(); });
}

describe("real QueryChat + ChatFooter preserve drafts", () => {
  for (const reason of ["member loading", "metadata loading", "whole group mute", "personal mute"] as const) {
    it(`${reason}: repeated render performs zero draft writes and keeps the editor blocked`, async () => {
      enter();
      store.setState({ currentGroupInfo: reason === "metadata loading" ? undefined : group("A", reason === "whole group mute" ? 3 : 0),
        currentMemberInGroup: reason === "member loading" ? undefined : member("A", "actor-a", 20, reason === "personal mute" ? Date.now() + 60000 : 0) });
      await renderChat(false);
      expect(host.querySelector("#editable-div")).toBeNull();
      expect(fixture.sdk.setConversationDraft).not.toHaveBeenCalled();
      await act(async () => { root!.render(<QueryChat />); await flush(); });
      expect(fixture.sdk.setConversationDraft).not.toHaveBeenCalled();
    });
  }
  it("StrictMode repeated blocked rendering performs zero draft writes", async () => {
    enter(); store.setState({ currentGroupInfo: group("A", 3), currentMemberInGroup: member() });
    await renderChat();
    await act(async () => { root!.render(<StrictMode><QueryChat /></StrictMode>); await flush(); });
    expect(fixture.sdk.setConversationDraft).not.toHaveBeenCalled();
  });
  it("after current metadata/member load the real footer restores the existing draft", async () => {
    enter(); await renderChat(false);
    expect(host.querySelector("#editable-div")).toBeNull();
    await act(async () => {
      fixture.groups[0].resolve({ data: [group()] }); fixture.members[0].resolve({ data: [member()] }); await flush();
    });
    expect(host.querySelector("#editable-div")?.textContent).toBe("kept user draft");
    expect(store.getState().currentConversation?.draftText).toBe("kept user draft");
    expect(fixture.sdk.setConversationDraft).not.toHaveBeenCalled();
  });
  it("known whole-group mute survives a failed refresh without enabling the real editor", async () => {
    enter(); store.setState({ currentGroupInfo: group("A", 3), currentMemberInGroup: member() });
    await renderChat(false);
    await act(async () => { fixture.groups[0].reject(new Error("synthetic current refresh")); await flush(); });
    expect(store.getState().currentGroupInfo?.status).toBe(3);
    expect(host.querySelector("#editable-div")).toBeNull();
    expect(fixture.sdk.setConversationDraft).not.toHaveBeenCalled();
  });
  it("a legitimate unmute event restores the existing draft without a clear or send", async () => {
    enter(); store.setState({ currentGroupInfo: group("A", 3), currentMemberInGroup: member() });
    await renderChat(false);
    await act(async () => { store.getState().updateCurrentGroupInfo(group()); await flush(); });
    expect(host.querySelector("#editable-div")?.textContent).toBe("kept user draft");
    expect(fixture.sdk.setConversationDraft).not.toHaveBeenCalled();
  });
});
