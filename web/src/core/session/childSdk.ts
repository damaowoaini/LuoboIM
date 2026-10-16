// Child renderers use the authenticated main renderer's one SDK connection.
// They must not log in again with the same platform or create independent DBs.
export const isDesktopChild =
  !!window.electronAPI && window.location.hash.startsWith("#/third/");
export function createChildSdk(): object {
  return new Proxy(
    {},
    {
      get: (_, method) => {
        if (method === "on" || method === "off") return () => undefined;
        if (method === "logout") return async () => undefined;
        return (...args: unknown[]) =>
          window.electronAPI!.ipcInvoke("callDesktopSession", String(method), args);
      },
    },
  );
}
