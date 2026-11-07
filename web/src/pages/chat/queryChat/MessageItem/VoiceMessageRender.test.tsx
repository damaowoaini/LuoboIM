// @vitest-environment jsdom
// Actual VoiceMessageRender, privacy hooks and shared lease. Only session/media
// and toast boundaries are synthetic; no browser playback acceptance is claimed.
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const f = vi.hoisted(() => ({
  toast: vi.fn(),
  session: {
    generation: 1,
    current: { issuer: "https://route.synthetic.test", userID: "voice-user", tenant: { serverID: "voice-tenant" } },
    onChange: () => () => {},
  },
}));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: f.session }));
vi.mock("@/utils/common", () => ({ feedbackToast: f.toast }));

import VoiceMessageRender from "./VoiceMessageRender";
import type { ExMessageItem } from "@/store";

function deferred() {
  let resolve!: () => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<void>((yes, no) => { resolve = () => yes(undefined); reject = no; });
  return { promise, resolve, reject };
}
const media: FakeAudio[] = [];
class FakeAudio {
  src = "";
  preload = "";
  currentTime = 0;
  abortOnLoad = false;
  onloadedmetadata: ((event: Event) => void) | null = null;
  onplay: ((event: Event) => void) | null = null;
  onpause: ((event: Event) => void) | null = null;
  onended: ((event: Event) => void) | null = null;
  pending = deferred();
  play = vi.fn(() => this.pending.promise);
  pause = vi.fn(() => { this.onpause?.(new Event("pause")); });
  removeAttribute = vi.fn((name: string) => { if (name === "src") this.src = ""; });
  load = vi.fn(() => {
    this.currentTime = 0;
    if (this.abortOnLoad) this.pending.reject(new DOMException("owned media reset", "AbortError"));
  });
  constructor() { media.push(this); }
}
function voice(id: string): ExMessageItem {
  return { clientMsgID: id, sendID: "peer", recvID: "voice-user", sessionType: 1,
    seq: 1, status: 2, contentType: 103, sendTime: 1800000000,
    soundElem: { sourceUrl: `https://objects.synthetic.test/${id}.ogg`, duration: 2 },
  } as unknown as ExMessageItem;
}
let root: Root | undefined;
let host: HTMLDivElement;
async function render(value: React.ReactNode) {
  await act(async () => { root!.render(value); });
}
function rows(showA = true) {
  return <>
    {showA && <div key="a" data-row="a"><VoiceMessageRender message={voice("a")} isSender={false} conversationID="conversation" /></div>}
    <div key="b" data-row="b"><VoiceMessageRender message={voice("b")} isSender={false} conversationID="conversation" /></div>
  </>;
}
function button(id: string) {
  const value = host.querySelector<HTMLElement>(`[data-row="${id}"] [role="button"]`);
  expect(value).not.toBeNull();
  return value!;
}
async function click(id: string) { await act(async () => { button(id).click(); }); }
async function playing(audio: FakeAudio) { await act(async () => { audio.onloadedmetadata?.(new Event("loadedmetadata")); audio.onplay?.(new Event("play")); }); }
async function settle(audio: FakeAudio, error?: unknown) {
  await act(async () => {
    if (error === undefined) audio.pending.resolve(); else audio.pending.reject(error);
    await Promise.resolve();
  });
}
beforeEach(() => {
  vi.clearAllMocks(); media.length = 0;
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("Audio", FakeAudio);
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => {
  if (root) await act(async () => { root!.unmount(); });
  root = undefined; host.remove(); vi.unstubAllGlobals();
});

describe("VoiceMessageRender playback", () => {
  it("retires pending voice ownership without stale rows stopping the next message or hiding current failures", async () => {
    await render(<div data-row="pending"><VoiceMessageRender message={voice("pending")} isSender={false} conversationID="conversation" /></div>);
    expect(media).toHaveLength(1);
    expect(media[0].preload).toBe("none"); expect(media[0].src).toBe(""); expect(media[0].play).not.toHaveBeenCalled();
    await click("pending"); const removed = media[0];
    expect(removed.play).toHaveBeenCalledOnce();
    removed.abortOnLoad = true;
    await render(null);
    expect(removed.pause).toHaveBeenCalledOnce();
    expect(removed.removeAttribute).toHaveBeenCalledWith("src");
    expect(removed.load).toHaveBeenCalledOnce(); expect(removed.currentTime).toBe(0);
    expect(f.toast).not.toHaveBeenCalled();

    await render(rows());
    await click("a"); const a1 = media[1]; const a1Ended = a1.onended!; await playing(a1);
    await click("b"); const b1 = media[2]; await playing(b1);
    expect(a1.pause).toHaveBeenCalledOnce(); expect(a1.src).toBe(""); expect(a1.load).toHaveBeenCalledOnce();
    expect(b1.play).toHaveBeenCalledOnce(); expect(b1.src).toBe("https://objects.synthetic.test/b.ogg");
    expect(button("a").getAttribute("aria-pressed")).toBe("false");
    expect(button("b").getAttribute("aria-pressed")).toBe("true");
    await settle(a1);
    await act(async () => { a1Ended(new Event("ended")); });
    expect(b1.pause).not.toHaveBeenCalled(); expect(b1.load).not.toHaveBeenCalled(); expect(b1.src).toBe("https://objects.synthetic.test/b.ogg");
    expect(f.toast).not.toHaveBeenCalled();

    // A second handoff exercises a late rejection as well as the late resolve.
    await click("a"); const a2 = media[3]; const a2Ended = a2.onended!;
    expect(b1.pause).toHaveBeenCalledOnce(); expect(b1.load).toHaveBeenCalledOnce();
    await click("b"); const b2 = media[4]; await playing(b2);
    expect(a2.pause).toHaveBeenCalledOnce(); expect(a2.load).toHaveBeenCalledOnce();
    await settle(a2, new Error("retired A transport failure"));
    await settle(b1, new DOMException("owned handoff", "AbortError"));
    await act(async () => { a2Ended(new Event("ended")); });
    await render(rows(false)); // A's effect cleanup must release only A.
    expect(b2.pause).not.toHaveBeenCalled(); expect(b2.load).not.toHaveBeenCalled();
    expect(b2.src).toBe("https://objects.synthetic.test/b.ogg");
    expect(button("b").getAttribute("aria-pressed")).toBe("true");
    expect(f.toast).not.toHaveBeenCalled();

    const realFailure = new Error("current stream unavailable");
    await act(async () => { b2.onpause?.(new Event("pause")); });
    await settle(b2, realFailure);
    expect(f.toast).toHaveBeenCalledTimes(1);
    expect(f.toast).toHaveBeenCalledWith({ error: realFailure, msg: "play audio failed" });
    expect(b2.pause).toHaveBeenCalledOnce(); expect(b2.src).toBe(""); expect(b2.load).toHaveBeenCalledOnce();
    expect(button("b").getAttribute("aria-pressed")).toBe("false");

    // Error names alone do not define cancellation: current AbortError reports.
    await click("b"); const b3 = media[5]; await playing(b3);
    const currentAbort = new DOMException("unexpected current abort", "AbortError");
    await act(async () => { b3.onpause?.(new Event("pause")); });
    await settle(b3, currentAbort);
    expect(f.toast).toHaveBeenCalledTimes(2);
    expect(f.toast).toHaveBeenLastCalledWith({ error: currentAbort, msg: "play audio failed" });

    // Repeated play/pause on one row uses different receiver and intent identity.
    await click("b"); const b4 = media[6]; const b4Ended = b4.onended!; await playing(b4); b4.currentTime = 0.75;
    await click("b"); expect(b4.pause).toHaveBeenCalledOnce();
    expect(b4.load).toHaveBeenCalledOnce(); expect(b4.currentTime).toBe(0);
    await click("b"); const b5 = media[7]; await playing(b5);
    expect(b5).not.toBe(b4); expect(b5.play).toHaveBeenCalledOnce(); expect(b5.currentTime).toBe(0.75);
    await settle(b4, new DOMException("owned pause", "AbortError"));
    await act(async () => { b4Ended(new Event("ended")); });
    expect(b5.pause).not.toHaveBeenCalled(); expect(b5.load).not.toHaveBeenCalled();
    expect(b5.src).toBe("https://objects.synthetic.test/b.ogg");
    expect(button("b").getAttribute("aria-pressed")).toBe("true");
    expect(f.toast).toHaveBeenCalledTimes(2);
    await settle(b5);
    await render(null);
    expect(b5.pause).toHaveBeenCalledOnce(); expect(b5.src).toBe(""); expect(b5.load).toHaveBeenCalledOnce(); expect(b5.currentTime).toBe(0);
    await render(<div data-row="idle"><VoiceMessageRender message={voice("idle")} isSender={false} conversationID="conversation" /></div>);
    const unused = media[8]; expect(media).toHaveLength(9);
    expect(unused.preload).toBe("none"); expect(unused.src).toBe(""); expect(unused.play).not.toHaveBeenCalled();
    await render(null);
    expect(unused.pause).toHaveBeenCalledOnce(); expect(unused.load).toHaveBeenCalledOnce(); expect(unused.src).toBe("");
    expect(f.toast).toHaveBeenCalledTimes(2);
  });
});
