import { describe, expect, it, vi } from "vitest";
import { bindMessageHistory } from "./messageHistory";
import { MessageHistoryFence } from "./messagePrivacy";

describe("actual SDK proxy clear ordering", () => {
  it("waits already-dispatched writes, refuses new message operations, drops old history, then runs one native clear", async () => {
    let finish!:(data:unknown)=>void;
    const order:string[]=[], native={sendMessage:vi.fn(()=>{order.push("send");return new Promise(resolve=>finish=resolve);}),getAdvancedHistoryMessageList:vi.fn(),deleteAllMsgFromLocalAndSvr:vi.fn(async()=>{order.push("clear");})};
    const sdk=bindMessageHistory(native,new MessageHistoryFence(),()=>"A");
    const old=sdk.sendMessage().catch(error=>error);
    const clearing=sdk.deleteAllMsgFromLocalAndSvr();
    await expect(sdk.getAdvancedHistoryMessageList()).rejects.toThrow();
    expect(native.getAdvancedHistoryMessageList).not.toHaveBeenCalled(); expect(order).toEqual(["send"]);
    finish({data:"ack"}); await clearing; expect(await old).toBeInstanceOf(Error);
    expect(order).toEqual(["send","clear"]);
  });
  it("never dispatches native clear after owner changed while settling an old operation", async () => {
    let owner="A",finish!:()=>void;
    const native={searchLocalMessages:()=>new Promise<void>(resolve=>finish=resolve),deleteAllMsgFromLocalAndSvr:vi.fn()};
    const sdk=bindMessageHistory(native,new MessageHistoryFence(),()=>owner);
    const old=sdk.searchLocalMessages().catch(()=>undefined), clearing=sdk.deleteAllMsgFromLocalAndSvr();
    owner="B";finish(); await old; await expect(clearing).rejects.toThrow("会话已切换"); expect(native.deleteAllMsgFromLocalAndSvr).not.toHaveBeenCalled();
  });
  it("does not label a settle timeout as a successful clear, and releases the barrier for an explicit retry", async () => {
    vi.useFakeTimers();
    try {
      const native={sendMessage:()=>new Promise(()=>{}),deleteAllMsgFromLocalAndSvr:vi.fn()},fence=new MessageHistoryFence();
      const sdk=bindMessageHistory(native,fence,()=>"A",100);
      void sdk.sendMessage().catch(()=>undefined); const clearing=sdk.deleteAllMsgFromLocalAndSvr();
      const rejection=expect(clearing).rejects.toThrow("未执行清空"); await vi.advanceTimersByTimeAsync(101); await rejection;
      expect(native.deleteAllMsgFromLocalAndSvr).not.toHaveBeenCalled(); expect(fence.current(fence.capture("A"),"A")).toBe(true);
    } finally {vi.useRealTimers();}
  });
  it("denies global/local and different-conversation clears while an earlier same-owner native clear is still running", async () => {
    let finish!:()=>void;
    const native={clearConversationAndDeleteAllMsg:vi.fn((_id:string)=>new Promise<void>(resolve=>finish=resolve)),deleteAllMsgFromLocalAndSvr:vi.fn()};
    const sdk=bindMessageHistory(native,new MessageHistoryFence(),()=>"A");
    const pending=sdk.clearConversationAndDeleteAllMsg("c-one");
    await Promise.resolve();
    expect(()=>sdk.clearConversationAndDeleteAllMsg("c-two")).toThrow();
    expect(()=>sdk.deleteAllMsgFromLocalAndSvr()).toThrow();
    await vi.waitFor(()=>expect(native.clearConversationAndDeleteAllMsg).toHaveBeenCalledOnce());finish();await pending;
  });
  it("keeps login/listeners synchronous and preserves ordinary successful operations", async () => {
    const native={on:vi.fn(()=>"subscription"),login:vi.fn(async()=>"logged-in"),getAdvancedHistoryMessageList:vi.fn(async()=>({data:{messageList:["normal"]}}))};
    const sdk=bindMessageHistory(native,new MessageHistoryFence(),()=>"A");
    expect(sdk.on()).toBe("subscription"); expect(await sdk.login()).toBe("logged-in");
    expect((await sdk.getAdvancedHistoryMessageList()).data.messageList).toEqual(["normal"]);
  });
});
