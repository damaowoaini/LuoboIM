import { getRouteUrl } from "./route";
import { endpoints, type Session, validateDomain } from "./session";

export const DEV_ENDPOINT_BOOTSTRAP_PATH = "/__luobo_dev__/endpoints";
const maxBootstrapBytes = 8192;
type Context = Readonly<{ origin: string; issuer: string }>;
export type DevEndpointMapping = Readonly<{
  version: 1;
  issuer: string;
  serverID: string;
  domain: string;
}>;
let mapping: DevEndpointMapping | undefined;

function loopbackOrigin(origin: string): boolean {
  try {
    const url = new URL(origin);
    return url.origin === origin && !url.username && !url.password &&
      ["http:", "https:"].includes(url.protocol) &&
      ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
  } catch {
    return false;
  }
}

function context(): Context | undefined {
  try {
    if (typeof window === "undefined") return undefined;
    return { origin: window.location.origin, issuer: getRouteUrl() };
  } catch {
    return undefined;
  }
}

export function decodeDevEndpointMapping(value: unknown, current: Context): DevEndpointMapping | undefined {
  if (!loopbackOrigin(current.origin) || current.issuer !== current.origin ||
      !value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const data = value as Record<string, unknown>;
  if (Object.keys(data).sort().join(",") !== "domain,issuer,serverID,version" ||
      data.version !== 1 || data.issuer !== current.origin ||
      typeof data.serverID !== "string" || !data.serverID || data.serverID.length > 128 ||
      data.serverID.trim() !== data.serverID || /[\u0000-\u0020\u007f]/.test(data.serverID) ||
      typeof data.domain !== "string") return undefined;
  try {
    if (validateDomain(data.domain) !== data.domain) return undefined;
  } catch {
    return undefined;
  }
  return Object.freeze({ version: 1, issuer: current.origin, serverID: data.serverID, domain: data.domain });
}

function matchesDevEndpointMapping(session: Session, current: Context, selected?: DevEndpointMapping): boolean {
  const domain = session.tenant.domains[session.tenant.activeLine];
  return !!selected && loopbackOrigin(current.origin) && current.origin === selected.issuer &&
    current.issuer === selected.issuer && session.issuer === selected.issuer &&
    session.tenant.serverID === selected.serverID && domain === selected.domain;
}

export function resolveDevEndpoints(session: Session, current: Context, selected?: DevEndpointMapping) {
  const original = endpoints(session);
  if (!matchesDevEndpointMapping(session, current, selected)) return original;
  const origin = new URL(current.origin);
  const socketOrigin = `${origin.protocol === "https:" ? "wss:" : "ws:"}//${origin.host}`;
  return { chat: `${origin.origin}/chat`, im: `${origin.origin}/im`, ws: `${socketOrigin}/msg_gateway` };
}

// This is an opt-in for the same validated deployment mapping, not a URL
// prefix exception. The Go core still independently restricts insecure hosts.
export function resolveDevSdkLoopbackOptIn(session: Session, current: Context, selected?: DevEndpointMapping): boolean {
  if (!matchesDevEndpointMapping(session, current, selected)) return false;
  const origin = new URL(current.origin);
  return origin.protocol === "http:" && ["127.0.0.1", "localhost"].includes(origin.hostname);
}

export function browserSdkLoopbackOptIn(session: Session): boolean {
  const current = context();
  return !!current && resolveDevSdkLoopbackOptIn(session, current, mapping);
}

export function browserEndpoints(session: Session) {
  const current = context();
  return current ? resolveDevEndpoints(session, current, mapping) : endpoints(session);
}

async function readBounded(response: Response): Promise<unknown> {
  const declared = response.headers.get("Content-Length");
  if (declared !== null && (!/^\d+$/.test(declared) || Number(declared) > maxBootstrapBytes)) return undefined;
  const reader = response.body?.getReader();
  if (!reader) return undefined;
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    for (;;) {
      const next = await reader.read();
      if (next.done) break;
      size += next.value.byteLength;
      if (size > maxBootstrapBytes) return undefined;
      chunks.push(next.value);
    }
    const bytes = new Uint8Array(size);
    let offset = 0;
    for (const chunk of chunks) {
      bytes.set(chunk, offset);
      offset += chunk.byteLength;
    }
    return JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes));
  } finally {
    await reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
}

// Optional, task-owned deployment data, never persisted with the Session. A
// missing or malformed bootstrap cannot grant a local transport mapping.
export async function initializeDevEndpoints(send: typeof fetch = fetch): Promise<void> {
  mapping = undefined;
  const before = context();
  if (!before || !loopbackOrigin(before.origin) || before.issuer !== before.origin) return;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 2500);
  try {
    const response = await send(`${before.origin}${DEV_ENDPOINT_BOOTSTRAP_PATH}`, {
      method: "GET", credentials: "omit", redirect: "error", cache: "no-store", signal: controller.signal,
    });
    if (!response.ok) return;
    const selected = decodeDevEndpointMapping(await readBounded(response), before);
    const after = context();
    if (after?.origin === before.origin && after.issuer === before.issuer) mapping = selected;
  } catch {
    // The normal HTTPS/443 endpoint contract remains in effect.
  } finally {
    clearTimeout(timer);
  }
}
