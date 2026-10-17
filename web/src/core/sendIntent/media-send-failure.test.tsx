// @vitest-environment jsdom
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { fixture, message, messages, reset } from "./harness";
import { decodeSendFailure, sendFailureText, visibleSendFailure } from "../sendFailure";
import { useSendMessage } from "@/pages/chat/queryChat/ChatFooter/useSendMessage";
import MessageSuffix from "@/pages/chat/queryChat/MessageItem/MessageSuffix";
import type { ExMessageItem } from "@/store";

let host: HTMLDivElement, root: Root, hook: ReturnType<typeof useSendMessage>;
function Probe() { hook = useSendMessage(); return null; }
beforeEach(() => { reset(); host = document.createElement("div"); document.body.append(host); root = createRoot(host); act(() => root.render(<Probe />)); });
afterEach(() => { act(() => root.unmount()); host.remove(); });
const sensitive = "synthetic-never-retain-query-body-value";
const putError = (status = 403, code = "AccessDenied") => ({ errCode: 10005,
  errMsg: `error *errors.errorString not implement CodeError: PUT https://objects.luobo.fixture.test/luobo-stack/synthetic?signature=${sensitive} failed, status code ${status}, body <Error><Code>${code}</Code><Message>${sensitive}</Message></Error>` });

describe("bounded media send failure presentation", () => {
  it("classifies only fixed source errors and never retains error text or infers a phase from 10005", () => {
    expect(decodeSendFailure(putError())).toEqual({ code: 10005, phase: "upload-put", reason: "http-rejected", httpStatus: 403, s3Code: "AccessDenied" });
    expect(decodeSendFailure({ errCode: 10005, errMsg: sensitive })).toEqual({ code: 10005, phase: "unknown", reason: "unknown", s3Code: "unknown" });
    expect(decodeSendFailure({ errCode: 10003, errMsg: "ordinary media reservation not acknowledged" }))
      .toEqual({ code: 10003, phase: "media-reservation", reason: "not-acknowledged", s3Code: "unknown" });
    for (const error of [null, "10005", { errCode: "10005" }, { errCode: Infinity }, { errCode: 2147483648 },
      Object.create({ errCode: 10005 }), Object.defineProperty({}, "errCode", { get() { throw new Error("getter must not run"); } })])
      expect(decodeSendFailure(error).code).toBeUndefined();
    const productionError = { ...putError(), errMsg: putError().errMsg.replace("objects.luobo.fixture.test/luobo-stack/", "storage.production.example:9443/arbitrary-production-bucket/") };
    expect(decodeSendFailure(productionError)).toEqual(decodeSendFailure(putError()));
    expect(JSON.stringify(decodeSendFailure(productionError))).not.toContain("storage.production.example");
    expect(JSON.stringify(decodeSendFailure(productionError))).not.toContain(sensitive);
    for (const text of [putError().errMsg.replace("https://", "http://"),
      putError().errMsg.replace("https://", "https://user:synthetic-password@"),
      putError().errMsg.replace("https://objects.luobo.fixture.test", "https://"),
      putError().errMsg.replace("error *errors.errorString not implement CodeError: ", "error *errors.withStack not implement CodeError: "),
      putError().errMsg.replace("error *errors.errorString not implement CodeError: ", ""),
      "generic status code 403 AccessDenied", "ordinary media reservation not acknowledged " + sensitive])
      expect(decodeSendFailure({ errCode: 10005, errMsg: text }).phase).toBe("unknown");
    expect(decodeSendFailure(putError(403, sensitive)).s3Code).toBe("unknown");
    expect(visibleSendFailure({ code: sensitive, phase: sensitive, reason: sensitive, httpStatus: sensitive, s3Code: sensitive }))
      .toEqual({ phase: "unknown", reason: "unknown", s3Code: "unknown" });
    expect(JSON.stringify(decodeSendFailure(putError()))).not.toContain(sensitive);
    expect(sendFailureText(decodeSendFailure(putError()), true)).not.toContain(sensitive);
  });

  it("keeps one rejected send unconfirmed and renders a safe visible attachment failure beside the unchanged retry control", async () => {
    fixture.sdk.sendMessage.mockRejectedValueOnce(putError());
    const input = { ...message("synthetic-media-failure"), contentType: 102 } as ExMessageItem;
    const intent = hook.beginCurrentSend()!;
    let state = "";
    await act(async () => { state = (await hook.sendMessage({ message: input, intent })).state; });
    expect(state).toBe("unconfirmed"); expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
    const recorded = messages.getState().updateOneMessage as unknown as { mock: { calls: [ExMessageItem][] } };
    expect(recorded.mock.calls).toHaveLength(1);
    const projected = recorded.mock.calls[0][0];
    expect(projected.status).toBe(3);
    expect(projected.sendFailure).toEqual(decodeSendFailure(putError()));
    expect(JSON.stringify(projected.sendFailure)).not.toContain(sensitive);
    act(() => root.render(<MessageSuffix message={projected} conversationID="cid-user-A" isSender />));
    const status = host.querySelector('[role="status"]');
    expect(status?.textContent).toContain("附件发送结果未确认。");
    expect(status?.textContent).toContain("错误码：10005");
    expect(status?.textContent).not.toContain("HTTP");
    expect(status?.textContent).not.toContain("AccessDenied");
    expect(status?.getAttribute("data-send-failure-code")).toBe("10005");
    expect(status?.getAttribute("data-send-failure-http-status")).toBe("403");
    expect(status?.getAttribute("data-send-failure-s3-code")).toBe("AccessDenied");
    expect(status?.getAttribute("data-send-failure-phase")).toBe("upload-put");
    expect(host.innerHTML).not.toContain(sensitive);
    expect(host.querySelectorAll('button[aria-label="发送失败，重试这条消息"]')).toHaveLength(1);
    expect(fixture.sdk.sendMessage).toHaveBeenCalledOnce();
  });
});
