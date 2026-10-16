export async function sdkDataDirectory(namespace: string): Promise<string> {
  if (!namespace) throw new Error("SDK数据库缺少账号归属");
  const digest = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(namespace),
  );
  const key = Array.from(new Uint8Array(digest), (byte) =>
    byte.toString(16).padStart(2, "0"),
  ).join("");
  // The bundled SQL worker mounts /sql but does not create nested directories.
  // dataDir is concatenated with userID, so use a flat, delimited file prefix.
  return `./luobo_${key}_`;
}

export async function closeSdkWorker(close: () => Promise<unknown>): Promise<void> {
  const response = await close();
  const result = typeof response === "string" ? JSON.parse(response) : response;
  if (
    !result ||
    typeof result !== "object" ||
    (result as { errCode?: number }).errCode !== 0
  )
    throw new Error("SDK数据库关闭失败，请重启后重试");
}
