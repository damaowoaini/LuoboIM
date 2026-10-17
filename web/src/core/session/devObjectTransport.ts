import { sessionCoordinator } from "./browser";
import { browserSdkLoopbackOptIn } from "./devEndpoints";

function loopbackOrigin(origin: string): boolean {
  try {
    const url = new URL(origin);
    return url.origin === origin && url.protocol === "http:" &&
      ["127.0.0.1", "localhost"].includes(url.hostname) && !url.username && !url.password;
  } catch { return false; }
}
const profile = "luobo-task-objects-v1";
const prefix = "/__luobo_dev_objects__";
let enabledOrigin: string | undefined;
let installed = false;

// This changes transport only. Message/SDK metadata retains canonical URLs.
export function mapDevObjectTransport(raw: string, origin: string): string | undefined {
  if (!loopbackOrigin(origin) || raw.length > 16384 || /[\u0000-\u0020\u007f\\#]/u.test(raw)) return;
  for (const [host, path, kind] of [
    ["objects.luobo.fixture.test", "/luobo-stack/", "storage"],
    ["luobo.fixture.test", "/api/object/", "alias"],
  ]) {
    const authority = `https://${host}`;
    if (!raw.startsWith(authority + path)) continue;
    const suffix = raw.slice(authority.length);
    const pathname = suffix.split("?")[0];
    try {
      if (pathname.length <= path.length || decodeURIComponent(pathname).split("/").some(part => part === "." || part === "..")) return;
    } catch { return; }
    return `${origin}${prefix}/${kind}${suffix}`;
  }
}
function currentOrigin(): string | undefined {
  if (typeof window === "undefined" || !loopbackOrigin(window.location.origin)) return;
  return window.location.origin;
}
function permitted(): string | undefined {
  const origin = currentOrigin(), session = sessionCoordinator.current;
  if (origin !== enabledOrigin || !origin || !session || session.issuer !== origin ||
      session.tenant.serverID !== "fixture-tenant" ||
      session.tenant.domains[session.tenant.activeLine] !== "luobo.fixture.test" ||
      !browserSdkLoopbackOptIn(session)) return;
  return origin;
}
export function devObjectTransportURL(raw: string): string {
  const origin = permitted();
  return origin ? mapDevObjectTransport(raw, origin) ?? raw : raw;
}
async function boundedJSON(response: Response): Promise<unknown> {
  const reader = response.body?.getReader();
  if (!reader) return;
  const chunks: Uint8Array[] = []; let total = 0;
  try {
    for (;;) {
      const next = await reader.read();
      if (next.done) break;
      total += next.value.byteLength;
      if (total > 1024) return;
      chunks.push(next.value);
    }
    const all = new Uint8Array(total); let at = 0;
    for (const chunk of chunks) { all.set(chunk, at); at += chunk.byteLength; }
    return JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(all));
  } finally { await reader.cancel().catch(() => undefined); reader.releaseLock(); }
}
export async function initializeDevObjectTransport(): Promise<void> {
  enabledOrigin = undefined;
  const origin = currentOrigin();
  if (!origin || installed) return;
  const original = window.fetch.bind(window);
  const controller = new AbortController(), timer = setTimeout(() => controller.abort(), 2500);
  try {
    const response = await original(`${origin}/__luobo_dev__/object-transport`, {
      method: "GET", credentials: "omit", redirect: "error", cache: "no-store", signal: controller.signal,
    });
    if (!response.ok) return;
    const value = await boundedJSON(response);
    if (!value || typeof value !== "object" || Array.isArray(value)) return;
    const item = value as Record<string, unknown>;
    if (Object.keys(item).sort().join(",") !== "issuer,profile,version" || item.version !== 1 ||
        item.issuer !== origin || item.profile !== profile || currentOrigin() !== origin) return;
    enabledOrigin = origin;
    // Fixed Go1.27.2 net/http RoundTrip passes fetch a string URL. Request
    // objects and all nonallowlisted traffic retain the original arguments.
    window.fetch = (input, init) => {
      const raw = typeof input === "string" ? input : input instanceof URL ? input.href : undefined;
      const mapped = raw === undefined ? undefined : devObjectTransportURL(raw);
      if (!mapped || mapped === raw) return original(input, init);
      return original(mapped, { ...init, credentials: "omit" });
    };
    installed = true;
  } catch {
    // An absent local capability never changes production fetch behavior.
  } finally { clearTimeout(timer); }
}
