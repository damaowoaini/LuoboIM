const routeKey = "luobo.route.url";
export function validateRouteUrl(raw: string): string {
  const url = new URL(raw);
  const local = ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
  if (
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    (url.protocol !== "https:" && !(local && url.protocol === "http:"))
  )
    throw new Error("请配置有效的 HTTPS 登录服务地址");
  return url.toString().replace(/\/$/, "");
}
export const getRouteUrl = (): string => {
  const raw =
    localStorage.getItem(routeKey) ||
    (import.meta.env.VITE_ROUTE_URL as string | undefined) ||
    window.location.origin;
  // file:// has no login-service origin. An unconfigured desktop must still
  // render settings so the user can supply a real service address.
  try {
    return validateRouteUrl(raw);
  } catch {
    return "";
  }
};
export const saveRouteUrl = (value: string): void =>
  localStorage.setItem(routeKey, validateRouteUrl(value));
