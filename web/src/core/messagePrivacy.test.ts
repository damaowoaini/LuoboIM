import { describe, expect, it } from "vitest";
import { inspectMessagePrivacy, MessageHistoryFence, PrivateMessageLedger, privateExpiry } from "./messagePrivacy";
const now = 1800000000000;
const privateMessage = (info = {}, fields = {}) => ({ clientMsgID: "synthetic-message", attachedInfoElem: { isPrivateChat: true, burnDuration: 30, hasReadTime: now, ...info }, ...fields });

describe("actual OpenIM metadata privacy", () => {
  it("does not infer private or read time from encryption/isRead/sendTime, and does not inspect opaque custom JSON", () => {
    const message = { isRead: true, sendTime: now, sessionType: 1, encryption: true, customElem: { data: JSON.stringify(privateMessage()) } };
    expect(inspectMessagePrivacy(message)).toEqual({ containsPrivate: false, rootPrivate: false, inspectionComplete: true, expiresAt: undefined });
    expect(inspectMessagePrivacy(privateMessage({ hasReadTime: 0 }, { isRead: true, sendTime: now })).expiresAt).toBeUndefined();
  });
  it("accepts boolean true in either real metadata source, never overwritten by the other's false", () => {
    const result = inspectMessagePrivacy({ attachedInfo: JSON.stringify({ isPrivateChat: true }), attachedInfoElem: { isPrivateChat: false } });
    expect(result.containsPrivate).toBe(true);
    expect(inspectMessagePrivacy({ attachedInfoElem: { isPrivateChat: "true" } }).inspectionComplete).toBe(false);
    expect(inspectMessagePrivacy({ attachedInfo: "[invalid]" }).inspectionComplete).toBe(false);
  });
  it("finds real quote/at-quote/merged private descendants and their earliest known deadline", () => {
    const child = privateMessage({}, { hasReadTime: now + 10000 });
    expect(inspectMessagePrivacy({ quoteElem: { quoteMessage: child } }).expiresAt).toBe(now + 30000);
    expect(inspectMessagePrivacy({ atTextElem: { quoteMessage: child } }).containsPrivate).toBe(true);
    const result = inspectMessagePrivacy({ mergeElem: { multiMessage: [{ textElem: {content: "normal"} }, child] } });
    expect(result.rootPrivate).toBe(false); expect(result.containsPrivate).toBe(true);
  });
  it("fails retention inspection closed on malformed, oversized, cyclic and excessively nested metadata", () => {
    expect(inspectMessagePrivacy({ attachedInfo: " ".repeat(65537) }).inspectionComplete).toBe(false);
    expect(inspectMessagePrivacy({ mergeElem: {} }).inspectionComplete).toBe(false);
    const cycle: any = {}; cycle.quoteElem = { quoteMessage: cycle };
    expect(inspectMessagePrivacy(cycle).inspectionComplete).toBe(false);
    let deep: any = {}; for (let i=0;i<34;i++) deep = { quoteElem: {quoteMessage: deep} };
    expect(inspectMessagePrivacy(deep).inspectionComplete).toBe(false);
    expect(inspectMessagePrivacy({mergeElem:{multiMessage:Array(4100).fill({})}}).inspectionComplete).toBe(false);
  });
});

describe("validated read-time deadlines", () => {
  it("accepts only bounded 2000–2100 seconds and milliseconds, including both sides of 2038", () => {
    for (const seconds of [946684800, 2147483647, 2147483648, 4102444800]) {
      expect(privateExpiry(seconds, 30)).toBe(seconds * 1000 + 30000);
      expect(privateExpiry(seconds * 1000, 30)).toBe(seconds * 1000 + 30000);
    }
    for (const invalid of [1,123,5000000000,946684799,4102444801,946684799999,4102444800001, now+.5,NaN,Infinity,Number.MAX_SAFE_INTEGER+1,"1800000000000"]) expect(privateExpiry(invalid,30)).toBeUndefined();
  });
  it("does not fabricate negative/invalid durations, and keeps the earliest root or attached authoritative read", () => {
    expect(privateExpiry(now,0)).toBe(now+30000); expect(privateExpiry(now,undefined)).toBe(now+30000);
    for (const duration of [-1, .5, Infinity, Number.MAX_SAFE_INTEGER]) expect(privateExpiry(now,duration)).toBeUndefined();
    expect(inspectMessagePrivacy(privateMessage({}, {hasReadTime:now+10000})).expiresAt).toBe(now+30000);
    expect(inspectMessagePrivacy(privateMessage({hasReadTime:now+10000}, {hasReadTime:now})).expiresAt).toBe(now+30000);
  });
  it("does not restart expiry on remount, omitted metadata, a longer duration, or wall-clock rollback", () => {
    let wall=now, mono=0;
    const ledger = new PrivateMessageLedger({wall:()=>wall,monotonic:()=>mono});
    expect(ledger.observe("issuer/a/user/session1","c",privateMessage()).expired).toBe(false);
    wall=now-60000; mono=31000;
    expect(ledger.observe("issuer/a/user/session1","c",privateMessage({burnDuration:300})).expired).toBe(true);
    expect(ledger.observe("issuer/a/user/session1","different-render-surface",{clientMsgID:"synthetic-message"}).expired).toBe(true);
    expect(ledger.observe("issuer/b/user/session1","c",{clientMsgID:"synthetic-message"}).expired).toBe(false);
  });
  it("keeps explicit deletion tombstones across a late history reply, without poisoning another session", () => {
    const ledger=new PrivateMessageLedger({wall:()=>now,monotonic:()=>0});
    ledger.forget("A/session1","c","deleted");
    expect(ledger.observe("A/session1","c",{clientMsgID:"deleted",textElem:{content:"late"}}).expired).toBe(true);
    expect(ledger.observe("A/session2","c",{clientMsgID:"deleted"}).allowsRetention).toBe(true);
  });
});

describe("history ownership fence", () => {
  it("invalidates prior tickets at clear start and end and denies overlapping clears", () => {
    const fence=new MessageHistoryFence(), before=fence.capture("owner","c"), finish=fence.beginClear("owner","c");
    expect(fence.current(before,"owner")).toBe(false);
    expect(fence.current(fence.capture("owner","c"),"owner")).toBe(false);
    expect(()=>fence.beginClear("owner")).toThrow(); finish();
    expect(fence.current(before,"owner")).toBe(false);
    expect(fence.current(fence.capture("owner","c"),"owner")).toBe(true);
    expect(fence.current(fence.capture("owner","c"),"same-user-different-issuer")).toBe(false);
  });
});
