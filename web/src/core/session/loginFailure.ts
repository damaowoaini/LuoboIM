// Keep only a bounded public error code across the SDK document restart.
// It belongs to this tab and origin; no account, message or credential is saved.
const key = "luobo.sdk.login.failure";
const validCode = (value: string) => value === "unknown" || /^-?\d{1,8}$/.test(value);

export function rememberSdkLoginFailure(code: string): void {
  try { sessionStorage.setItem(key, validCode(code) ? code : "unknown"); } catch { /* The URL remains the fallback. */ }
}

export function readSdkLoginFailure(): string | undefined {
  try {
    const code = sessionStorage.getItem(key);
    return code && validCode(code) ? code : undefined;
  } catch { return undefined; }
}

export function clearSdkLoginFailure(): void {
  try { sessionStorage.removeItem(key); } catch { /* Do not prevent another login. */ }
}
