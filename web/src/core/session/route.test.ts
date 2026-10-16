import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { getRouteUrl, validateRouteUrl } from "./route";
let saved: string | null;
beforeEach(() => {
  saved = null;
  vi.stubGlobal("localStorage", { getItem: () => saved });
  vi.stubGlobal("window", { location: { origin: "http://127.0.0.1:5173" } });
});
afterEach(() => {
  vi.unstubAllGlobals();
});
it("keeps an unconfigured file-origin desktop recoverable through settings", () => {
  vi.stubGlobal("window", { location: { origin: "file://" } });
  expect(getRouteUrl()).toBe("");
});
it("recovers from an invalid persisted address without rendering a fatal exception", () => {
  saved = "javascript:alert(1)";
  expect(getRouteUrl()).toBe("");
  saved = "https://route.example/";
  expect(getRouteUrl()).toBe("https://route.example");
});
it("requires HTTPS outside loopback and never accepts embedded credentials, query or fragment", () => {
  for (const raw of [
    "http://route.example",
    "https://user:password@route.example",
    "file:///tmp/service",
    "https://route.example?token=private",
    "https://route.example/#fragment",
  ])
    expect(() => validateRouteUrl(raw)).toThrow();
  expect(validateRouteUrl("http://127.0.0.1:3000/")).toBe("http://127.0.0.1:3000");
});
