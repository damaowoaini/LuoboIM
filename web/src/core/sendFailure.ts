// Presentation-only failure metadata. Never retains the SDK error, URL or body.
export type SendFailureDiagnostic = Readonly<{
  code?: number;
  phase: "unknown" | "media-claim" | "media-reservation" | "session" | "upload-put";
  reason: "unknown" | "sender-differs" | "not-admitted" | "bridge-failed" | "not-acknowledged" | "retired" | "invalid-arguments" | "http-rejected";
  httpStatus?: number;
  s3Code: "unknown" | "AccessDenied" | "SignatureDoesNotMatch" | "NoSuchUpload" | "InvalidPart" | "RequestTimeTooSkewed";
}>;
const known = new Map<string, Pick<SendFailureDiagnostic, "phase" | "reason">>([
  ["ordinary media sender differs", { phase: "media-claim", reason: "sender-differs" }],
  ["media already active or session retired", { phase: "media-claim", reason: "not-admitted" }],
  ["ordinary media reservation failed", { phase: "media-reservation", reason: "bridge-failed" }],
  ["ordinary media reservation not acknowledged", { phase: "media-reservation", reason: "not-acknowledged" }],
  ["original media session retired", { phase: "session", reason: "retired" }],
  ["buffer session expired", { phase: "session", reason: "retired" }],
  ["buffer session expired before dispatch", { phase: "session", reason: "retired" }],
  ...["ordinary send arguments incomplete", "ordinary send string arguments required", "ordinary online flag must be boolean"]
    .map(text => [text, { phase: "media-claim", reason: "invalid-arguments" }] as const),
]);
const s3Codes = new Set(["AccessDenied", "SignatureDoesNotMatch", "NoSuchUpload", "InvalidPart", "RequestTimeTooSkewed"]);
function own(value: unknown, key: string): unknown {
  try { return value && typeof value === "object" ? Object.getOwnPropertyDescriptor(value, key)?.value : undefined; }
  catch { return undefined; }
}
export function decodeSendFailure(error: unknown): SendFailureDiagnostic {
  const rawCode = own(error, "errCode");
  const code = typeof rawCode === "number" && Number.isInteger(rawCode) && rawCode >= 0 && rawCode <= 2147483647 ? rawCode : undefined;
  const unknown: SendFailureDiagnostic = { ...(code === undefined ? {} : { code }), phase: "unknown", reason: "unknown", s3Code: "unknown" };
  const raw = own(error, "errMsg");
  if (typeof raw !== "string" || raw.length > 65536) return Object.freeze(unknown);
  const fixed = known.get(raw);
  if (fixed) return Object.freeze({ ...unknown, ...fixed });
  // doPut's fmt.Errorf has no %w. The pinned stdlib creates an errorString;
  // UploadFile/SendMessage return it directly and messageCall adds this prefix.
  const prefix = "error *errors.errorString not implement CodeError: ";
  if (!raw.startsWith(prefix)) return Object.freeze(unknown);
  const match = /^PUT (https:\/\/[^\s]{1,16384}) failed, status code ([1-5][0-9]{2}), body ([\s\S]*)$/.exec(raw.slice(prefix.length));
  if (!match) return Object.freeze(unknown);
  try {
    const url = new URL(match[1]);
    // Diagnostic parsing performs no request and retains no URL components.
    // Production storage hosts/ports/paths are not a fixture authorization list.
    const authority = match[1].slice("https://".length).split(/[/?#]/)[0];
    if (url.protocol !== "https:" || !authority || !url.hostname || url.username || url.password || authority.includes("@"))
      return Object.freeze(unknown);
    const httpStatus = Number(match[2]);
    if (httpStatus >= 200 && httpStatus < 400) return Object.freeze(unknown);
    const result: SendFailureDiagnostic = { ...unknown, phase: "upload-put", reason: "http-rejected", httpStatus };
    if (match[3].length <= 16384 && typeof DOMParser === "function") {
      const xml = new DOMParser().parseFromString(match[3], "application/xml"), root = xml.documentElement;
      const children = Array.from(root.children).filter(child => child.tagName === "Code");
      if (root.tagName === "Error" && !root.namespaceURI && !xml.getElementsByTagName("parsererror").length && children.length === 1 &&
          children[0].children.length === 0 && s3Codes.has(children[0].textContent ?? ""))
        return Object.freeze({ ...result, s3Code: children[0].textContent as SendFailureDiagnostic["s3Code"] });
    }
    return Object.freeze(result);
  } catch { return Object.freeze(unknown); }
}
export function visibleSendFailure(value: unknown): SendFailureDiagnostic {
  const code = own(value, "code"), phase = own(value, "phase"), reason = own(value, "reason");
  const http = own(value, "httpStatus"), s3 = own(value, "s3Code");
  const phases = new Set(["unknown", "media-claim", "media-reservation", "session", "upload-put"]);
  const reasons = new Set(["unknown", "sender-differs", "not-admitted", "bridge-failed", "not-acknowledged", "retired", "invalid-arguments", "http-rejected"]);
  return Object.freeze({
    ...(typeof code === "number" && Number.isInteger(code) && code >= 0 && code <= 2147483647 ? { code } : {}),
    phase: typeof phase === "string" && phases.has(phase) ? phase as SendFailureDiagnostic["phase"] : "unknown",
    reason: typeof reason === "string" && reasons.has(reason) ? reason as SendFailureDiagnostic["reason"] : "unknown",
    ...(typeof http === "number" && Number.isInteger(http) && http >= 100 && http <= 599 ? { httpStatus: http } : {}),
    s3Code: typeof s3 === "string" && s3Codes.has(s3) ? s3 as SendFailureDiagnostic["s3Code"] : "unknown",
  });
}
export function sendFailureText(value: unknown, media: boolean): string {
  const failure = visibleSendFailure(value);
  const parts = [media ? "附件发送结果未确认。" : "发送结果未确认。"];
  if (failure?.phase === "media-claim" || failure?.phase === "media-reservation") parts.push("附件准备未完成。");
  if (failure?.phase === "session") parts.push("会话已变化。");
  parts.push(failure?.code === undefined ? "错误码未知。" : `错误码：${failure.code}。`);
  return parts.join(" ");
}
