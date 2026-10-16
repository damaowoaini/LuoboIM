// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MessageItem, SearchMessageResult } from "open-im-sdk-wasm/lib/types/entity";
import type { SendIntent } from "@/core/messageSendIntent";
const session = vi.hoisted(() => ({ generation: 1, current: { userID: "owner", issuer: "https://synthetic.invalid", tenant: { serverID: "test" } }, onChange: vi.fn() }));
vi.mock("@/core/session/browser", () => ({ sessionCoordinator: session }));
import { captureIndividualForward, runIndividualForward, type IndividualForwardDependencies } from "@/core/individualForward";
import { captureSendOrigin } from "@/core/messageSendIntent";
import { messageOwner, messageHistoryFence, messagePrivacyLedger } from "@/core/messagePrivacyRuntime";

function source(id: string, extra: object = {}): MessageItem {
  return { clientMsgID: id, status: 2, contentType: 101, textElem: { content: "ordinary" }, ...extra } as MessageItem;
}
function found(cid: string, id: string, extra: object = {}): SearchMessageResult {
  return { totalCount: 1, findResultItems: [{ conversationID: cid, messageCount: 1, messageList: [source(id, extra)] }] } as SearchMessageResult;
}
function dependencies() {
  return {
    isCurrent: vi.fn(() => true), resolve: vi.fn(async (intent: SendIntent) => intent),
    canDeliver: vi.fn(async (_intent: SendIntent) => true),
    find: vi.fn(async (cid: string, id: string) => found(cid, id)),
    create: vi.fn(async (value: MessageItem) => ({ ...value, clientMsgID: `forward-${value.clientMsgID}`, status: 1 } as MessageItem)),
    createAdditional: vi.fn(async (_text: string) => source("additional-created")),
    send: vi.fn<IndividualForwardDependencies["send"]>(async () => ({ state: "sent" })),
    onProgress: vi.fn<NonNullable<IndividualForwardDependencies["onProgress"]>>(),
  };
}
function setup(ids = ["one", "two"]) {
  const selection = captureIndividualForward("original-cid", messageOwner(), ids)!;
  const origin = captureSendOrigin(undefined, selection.sourceCID);
  const intents = [origin.selectedIntent({ kind: "user", id: "U", conversationID: "target-U" })!,
    origin.selectedIntent({ kind: "group", id: "G", conversationID: "target-G" })!];
  return { selection, intents, deps: dependencies() };
}
const flush = async () => { for (let i = 0; i < 40; ++i) await Promise.resolve(); };
beforeEach(() => { session.generation++; messageHistoryFence.invalidate(); messagePrivacyLedger.clear(); });
afterEach(() => vi.useRealTimers());

describe("individual forwarding one-shot operation", () => {
  it("accepts exactly one to twenty unique IDs and stores no source body", () => {
    expect(captureIndividualForward("original-cid", messageOwner(), [])).toBeUndefined();
    expect(captureIndividualForward("original-cid", messageOwner(), Array.from({ length: 21 }, (_, i) => String(i)))).toBeUndefined();
    expect(captureIndividualForward("original-cid", messageOwner(), ["same", "same"])).toBeUndefined();
    const value = setup(Array.from({ length: 20 }, (_, i) => String(i))).selection;
    expect(value.sourceIDs).toHaveLength(20); expect(Object.isFrozen(value.sourceIDs)).toBe(true);
    expect(Object.keys(value).sort()).toEqual(["current", "owner", "receipts", "sourceCID", "sourceIDs", "started"]);
  });
  it("serializes separate sources and one additional tuple for each explicit target", async () => {
    const { selection, intents, deps } = setup();
    const rows = await runIndividualForward(selection, intents, "note", deps);
    expect(rows.map(row => [row.target.kind, row.target.id, row.sourceID, row.state])).toEqual([
      ["user", "U", "one", "acknowledged"], ["user", "U", "two", "acknowledged"], ["user", "U", "", "acknowledged"],
      ["group", "G", "one", "acknowledged"], ["group", "G", "two", "acknowledged"], ["group", "G", "", "acknowledged"],
    ]);
    expect(deps.create.mock.calls.map(([value]) => value.clientMsgID)).toEqual(["one", "two", "one", "two"]);
    expect(deps.createAdditional.mock.calls).toEqual([["note"], ["note"]]);
    expect(deps.find.mock.calls.every(([cid]) => cid === "original-cid")).toBe(true);
  });
  it("records ACK then unknown then not-attempted without replaying the frozen operation", async () => {
    const { selection, intents, deps } = setup();
    deps.send.mockResolvedValueOnce({ state: "sent" }).mockResolvedValueOnce({ state: "unconfirmed" });
    const rows = await runIndividualForward(selection, intents, "", deps);
    expect(rows.map(row => row.state)).toEqual(["acknowledged", "unconfirmed", "not-attempted", "not-attempted"]);
    expect(deps.send).toHaveBeenCalledTimes(2);
    expect(await runIndividualForward(selection, intents, "", deps)).toEqual(rows);
    expect(deps.send).toHaveBeenCalledTimes(2);
    // A later deliberate reselection is a new intent, not globally prohibited.
    const next = captureIndividualForward("original-cid", messageOwner(), ["one"])!;
    expect((await runIndividualForward(next, [intents[0]], "", deps))[0].state).toBe("acknowledged");
    expect(deps.send).toHaveBeenCalledTimes(3);
  });
  it("counts an unconfirmed additional tuple and stops the next recipient", async () => {
    const { selection, intents, deps } = setup();
    deps.send.mockResolvedValueOnce({ state: "sent" }).mockResolvedValueOnce({ state: "sent" }).mockResolvedValueOnce({ state: "unconfirmed" });
    const rows = await runIndividualForward(selection, intents, "note", deps);
    expect(rows.map(row => [row.kind, row.state])).toEqual([["message", "acknowledged"], ["message", "acknowledged"], ["additional", "unconfirmed"],
      ["message", "not-attempted"], ["message", "not-attempted"], ["additional", "not-attempted"]]);
    expect(deps.send).toHaveBeenCalledTimes(3);
  });
  it("rejects final target permission before reading or preparing any source", async () => {
    const { selection, intents, deps } = setup(); deps.canDeliver.mockResolvedValue(false);
    const rows = await runIndividualForward(selection, intents, "", deps);
    expect(rows[0]).toEqual(expect.objectContaining({ state: "rejected", reason: "permission" }));
    expect(deps.find).not.toHaveBeenCalled(); expect(deps.create).not.toHaveBeenCalled(); expect(deps.send).not.toHaveBeenCalled();
  });
  it.each(["empty", "foreign-cid", "foreign-id", "duplicate-source", "extra-result", "revoked"])("refuses exact lookup mismatch: %s", async variant => {
    const { selection, intents, deps } = setup(); const result = found("original-cid", "one");
    if (variant === "empty") result.findResultItems = [];
    if (variant === "foreign-cid") result.findResultItems![0].conversationID = "other-cid";
    if (variant === "foreign-id") result.findResultItems![0].messageList[0].clientMsgID = "foreign";
    if (variant === "duplicate-source") result.findResultItems![0].messageList.push(source("one"));
    if (variant === "extra-result") result.findResultItems!.push(result.findResultItems![0]);
    if (variant === "revoked") result.findResultItems![0].messageList[0].contentType = 2101;
    deps.find.mockResolvedValue(result);
    const rows = await runIndividualForward(selection, intents, "", deps);
    expect(rows[0].state).toBe("rejected"); expect(deps.send).not.toHaveBeenCalled();
  });
  it("refuses ordinary-looking sources containing a private quoted child", async () => {
    const { selection, intents, deps } = setup();
    deps.find.mockResolvedValue(found("original-cid", "one", { quoteElem: { quoteMessage: source("private-child", { attachedInfoElem: { isPrivateChat: true } }) } }));
    expect((await runIndividualForward(selection, intents, "", deps))[0].reason).toBe("invalid-source");
    expect(deps.create).not.toHaveBeenCalled(); expect(deps.send).not.toHaveBeenCalled();
  });
  it("cannot resume after source history clears while forward creation is pending", async () => {
    const { selection, intents, deps } = setup(); let finish!: (value: MessageItem) => void;
    deps.create.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
    const pending = runIndividualForward(selection, intents, "", deps); await flush(); expect(deps.create).toHaveBeenCalledOnce();
    const endClear = messageHistoryFence.beginClear(messageOwner(), selection.sourceCID); finish(source("late-created"));
    const rows = await pending; endClear();
    expect(rows.every(row => row.state === "not-attempted")).toBe(true); expect(deps.send).not.toHaveBeenCalled();
  });
  it("retains a verified ACK as fact when the view becomes invalid during dispatch", async () => {
    const { selection, intents, deps } = setup(); let finish!: (value: { state: "sent" }) => void;
    deps.send.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
    const pending = runIndividualForward(selection, intents, "", deps); await flush(); expect(deps.send).toHaveBeenCalledOnce();
    expect(selection.receipts()[0].state).toBe("processing");
    deps.isCurrent.mockReturnValue(false); finish({ state: "sent" });
    expect((await pending).map(row => row.state)).toEqual(["acknowledged", "not-attempted", "not-attempted", "not-attempted"]);
  });
  it("latches send timeout as unknown and late completion never continues later tuples", async () => {
    vi.useFakeTimers(); const { selection, intents, deps } = setup(); let finish!: (value: { state: "sent" }) => void;
    deps.send.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
    const pending = runIndividualForward(selection, intents, "note", deps); await flush(); expect(deps.send).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(10000);
    expect((await pending)[0].state).toBe("unconfirmed");
    finish({ state: "sent" }); await flush();
    expect(deps.send).toHaveBeenCalledOnce(); expect(deps.createAdditional).not.toHaveBeenCalled();
    expect(selection.receipts()[0].state).toBe("unconfirmed");
  });
});
