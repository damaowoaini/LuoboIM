import { describe, expect, it, vi } from "vitest";
import { createSessionLogout, revokeCurrentSession } from "./logout";
import { Session, SessionCoordinator } from "./session";

const response = (tenant = "a") => ({ issuer: "https://issuer.example", userID: "same-user", imToken: `im-${tenant}`, chatToken: `chat-${tenant}`, serverInfo: {serverID: tenant, serverName:tenant, domains:[`${tenant}.example`]} });
async function fixture() {
  const coordinator = new SessionCoordinator({read:async()=>undefined,write:async()=>{},clear:async()=>{}});
  await coordinator.commit(response());
  return coordinator;
}
const deferred = <T>() => { let resolve!: (value:T)=>void; let reject!: (error:Error)=>void; const promise=new Promise<T>((yes,no)=>{resolve=yes;reject=no;});return {promise,resolve,reject}; };

describe("current session logout", () => {
  it.each(["closed", "pending"] as const)("waits for remote %s then clears only captured session", async (state) => {
    const c=await fixture(), remote=deferred<"closed"|"pending">();
    const clear=vi.fn(()=>c.clear());
    const logout=createSessionLogout(c,()=>remote.promise,clear);
    const operation=logout(); expect(c.current?.tenant.serverID).toBe("a");expect(clear).not.toHaveBeenCalled();expect(logout.isPending()).toBe(true);
    remote.resolve(state);expect(await operation).toEqual({state,cleared:true});expect(c.current).toBeUndefined();expect(clear).toHaveBeenCalledWith(state);expect(logout.isPending()).toBe(false);
  });
  it("clears locally after failure and deduplicates pending user logout", async () => {
    const c=await fixture(), remote=deferred<"closed">(), send=vi.fn(()=>remote.promise), clear=vi.fn(()=>c.clear());
    const logout=createSessionLogout(c,send,clear), first=logout();expect(logout()).toBe(first);
    remote.reject(new Error("offline"));expect(await first).toEqual({state:"failed",cleared:true});expect(send).toHaveBeenCalledTimes(1);expect(clear).toHaveBeenCalledTimes(1);
  });
  it("late response cannot clear another tenant with the same userID", async () => {
    const c=await fixture(), remote=deferred<"closed">(), clear=vi.fn(()=>c.clear());
    const logout=createSessionLogout(c,()=>remote.promise,clear);const old=logout();
    await c.commit(response("b"));expect(logout.isPending()).toBe(false);remote.resolve("closed");expect(await old).toEqual({state:"closed",cleared:false});expect(clear).not.toHaveBeenCalled();expect(c.current?.tenant.serverID).toBe("b");
  });
  it("forced SDK kick clears immediately without remote platform revocation", async () => {
    const c=await fixture(), remote=deferred<"closed">(), send=vi.fn(()=>remote.promise), clear=vi.fn(()=>c.clear());
    const logout=createSessionLogout(c,send,clear);const old=logout();expect(await logout(true)).toEqual({state:"local",cleared:true});remote.resolve("closed");expect((await old).cleared).toBe(false);expect(clear).toHaveBeenCalledTimes(1);
  });
  it("sends only captured dual credentials to the exact captured tenant", async () => {
    const c=await fixture(), session=c.current as Session;
    const send=vi.fn(async()=>new Response(JSON.stringify({errCode:0,data:{revoked:true,sessionClosed:false,pending:true}})));
    expect(await revokeCurrentSession(session,new AbortController().signal,send as typeof fetch)).toBe("pending");
    expect(send.mock.calls[0]).toEqual(["https://a.example/chat/account/logout",expect.objectContaining({headers:expect.objectContaining({token:"chat-a"}),body:JSON.stringify({imToken:"im-a"}),credentials:"omit",redirect:"error"})]);
  });
  it.each([{errCode:0,data:{revoked:true,sessionClosed:true,pending:true}},{errCode:0,data:{revoked:false,sessionClosed:true,pending:false}},{errCode:1505,data:null}])("rejects unconfirmed or contradictory backend logout: %j",async(body)=>{
    const c=await fixture();await expect(revokeCurrentSession(c.current!,new AbortController().signal,async()=>new Response(JSON.stringify(body)))).rejects.toThrow();
  });
  it("bounded transport aborts without falling back to another tenant", async()=>{
    const c=await fixture();const send=vi.fn((_url:RequestInfo|URL,init?:RequestInit)=>new Promise<Response>((_yes,no)=>{init?.signal?.addEventListener("abort",()=>no(new DOMException("aborted","AbortError")));}));
    await expect(revokeCurrentSession(c.current!,new AbortController().signal,send,1)).rejects.toThrow();expect(send).toHaveBeenCalledTimes(1);
  });
});
