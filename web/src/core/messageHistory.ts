import { MessageHistoryFence } from "./messagePrivacy";

const clears = new Set(["clearConversationAndDeleteAllMsg", "deleteConversationAndDeleteAllMsg", "deleteAllMsgFromLocalAndSvr", "deleteAllMsgFromLocal", "deleteAllMsgFromSvr"]);
const exempt = new Set(["on", "off", "initSDK", "login", "logout", "getLoginStatus", "getLoginUserID", "setListener"]);

/** Order SDK operations already dispatched before a clear. This cannot cancel
 * vendor Go/WASM work; timeout means clear was NOT dispatched, not success. */
export function bindMessageHistory<T extends object>(sdk: T, fence: MessageHistoryFence, owner: () => string, timeout = 10000): T {
  const inFlight = new Set<Promise<unknown>>();
  return new Proxy(sdk, {
    get(target, property, receiver) {
      const value = Reflect.get(target, property, receiver);
      if (typeof value !== "function" || typeof property !== "string" || exempt.has(property)) return typeof value === "function" ? value.bind(target) : value;
      return (...args: unknown[]) => {
        const identity = owner();
        const conversationID = args[0] && typeof args[0] === "object" && "conversationID" in args[0] ? String(args[0].conversationID) : typeof args[0] === "string" && clears.has(property) ? args[0] : "";
        if (clears.has(property)) {
          const finish = fence.beginClear(identity, property.startsWith("deleteAll") ? undefined : conversationID);
          const pending = [...inFlight];
          return (async () => {
            let timer: ReturnType<typeof setTimeout> | undefined;
            try {
              await Promise.race([Promise.allSettled(pending), new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("已有消息操作尚未完成，未执行清空，请稍后明确重试")), timeout); })]);
              if (owner() !== identity) throw new Error("会话已切换，未执行清空");
              return await value.apply(target, args);
            } finally { if (timer) clearTimeout(timer); finish(); }
          })();
        }
        const ticket = fence.capture(identity, conversationID);
        if (!fence.current(ticket, owner())) return Promise.reject(new Error("消息记录正在更新，请稍后操作"));
        let result: unknown;
        try { result = value.apply(target, args); } catch (error) { throw error; }
        if (!result || typeof (result as Promise<unknown>).then !== "function") return result;
        const native = Promise.resolve(result);
        inFlight.add(native);
        // Track settlement even when a caller leaves or its visible promise is stale.
        native.then(() => inFlight.delete(native), () => inFlight.delete(native));
        return native.then(data => {
          if (!fence.current(ticket, owner())) throw new Error("会话或消息记录已更新，忽略旧结果");
          return data;
        });
      };
    },
  });
}
