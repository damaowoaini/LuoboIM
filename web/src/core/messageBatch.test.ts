import { describe, expect, it, vi } from "vitest";
import { deliverMessageBatch, type SendReceipt } from "./messageBatch";

const recipients = [{ userID: "one" }, { userID: "two" }];
const options = () => ({ recipients, additional: "", isCurrent: () => true, canDeliver: vi.fn().mockResolvedValue(true), createMessage: vi.fn().mockResolvedValue("message"), createAdditional: vi.fn().mockResolvedValue("additional"), send: vi.fn().mockResolvedValue({ state: "sent" }) });
describe("selected-target message delivery", () => {
  it("waits for both acknowledgements and never retries an unknown send", async () => {
    let finish!: (value: SendReceipt) => void; const deps = options();
    deps.send.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; })).mockResolvedValueOnce({ state: "unconfirmed" });
    let settled = false; const pending = deliverMessageBatch(deps).then(value => { settled = true; return value; });
    await vi.waitFor(() => expect(deps.send).toHaveBeenCalledTimes(1)); expect(settled).toBe(false);
    finish({ state: "sent" }); const result = await pending;
    expect(result.map(item => item.state)).toEqual(["sent", "unconfirmed"]); expect(deps.send).toHaveBeenCalledTimes(2);
  });
  it("does not send to a group with authoritative membership false", async () => {
    const deps = options(); deps.canDeliver.mockResolvedValue(false);
    expect((await deliverMessageBatch(deps)).map(item => item.state)).toEqual(["skipped", "skipped"]);
    expect(deps.createMessage).not.toHaveBeenCalled(); expect(deps.send).not.toHaveBeenCalled();
  });
  it("does not send a prepared message after the modal/session owner changed", async () => {
    let current = true; const deps = { ...options(), isCurrent: () => current };
    deps.createMessage.mockImplementation(async () => { current = false; return "message"; });
    expect((await deliverMessageBatch(deps)).map(item => item.state)).toEqual(["cancelled", "cancelled"]); expect(deps.send).not.toHaveBeenCalled();
  });
  it("retains a primary acknowledgement when an additional message fails", async () => {
    const deps = { ...options(), recipients: [recipients[0]], additional: "remark" };
    deps.send.mockResolvedValueOnce({ state: "sent" }).mockRejectedValueOnce(new Error("unknown additional ACK"));
    expect((await deliverMessageBatch(deps))[0].state).toBe("partial"); expect(deps.send).toHaveBeenCalledTimes(2);
  });
  it("isolates preparation failure to that target and reports complete success only for acknowledgements", async () => {
    const deps = options(); deps.createMessage.mockRejectedValueOnce(new Error("malformed source"));
    expect((await deliverMessageBatch(deps)).map(item => item.state)).toEqual(["not-sent", "sent"]); expect(deps.send).toHaveBeenCalledOnce();
  });
});
