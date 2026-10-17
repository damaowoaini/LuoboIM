function record(value: unknown): value is Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const prototype: unknown = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

export function decodeAppConfigData(value: unknown): { config: Record<string, string> } {
  if (!record(value)) throw new Error("服务配置响应格式错误");
  const raw = Object.hasOwn(value, "config") ? value.config : {};
  if (!record(raw) || Object.values(raw).some((item) => typeof item !== "string"))
    throw new Error("服务配置响应格式错误");
  return { config: { ...raw } as Record<string, string> };
}
