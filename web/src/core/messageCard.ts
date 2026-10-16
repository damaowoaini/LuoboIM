/** Received OpenIM 108 cards are identity hints, never cached profile authority.
 * faceURL/ex are bounded for wire compatibility and are deliberately discarded. */
export type ReceivedCard = Readonly<{ userID: string; label: string }>;
export type MessageCardSource = Readonly<{ conversationID: string; groupID?: string; current: () => boolean; canShare: () => boolean }>;
const unsafeControls = /[\x00-\x1f\x7f-\x9f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]/;
const lossless = (value: string) => {
  for (let i = 0; i < value.length; i++) {
    const code = value.charCodeAt(i);
    if (code >= 0xd800 && code <= 0xdbff) {
      const next = value.charCodeAt(++i);
      if (!(next >= 0xdc00 && next <= 0xdfff)) return false;
    } else if (code >= 0xdc00 && code <= 0xdfff) return false;
  }
  return true;
};
export function parseReceivedCard(value: unknown): ReceivedCard | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return;
  const row = value as Record<string, unknown>;
  const { userID, nickname, faceURL, ex } = row;
  if (typeof userID !== "string" || !userID || userID.length > 128 ||
      userID !== userID.trim() || /\s/u.test(userID) || unsafeControls.test(userID) || !lossless(userID)) return;
  if (nickname != null && (typeof nickname !== "string" || nickname.length > 256 ||
      Array.from(nickname).length > 128 || unsafeControls.test(nickname) || !lossless(nickname))) return;
  if (faceURL != null && (typeof faceURL !== "string" || faceURL.length > 2048 || !lossless(faceURL))) return;
  if (ex != null && (typeof ex !== "string" || ex.length > 4096 || !lossless(ex))) return;
  const strings = [userID, nickname ?? "", faceURL ?? "", ex ?? ""] as string[];
  if (strings.reduce((sum, text) => sum + new TextEncoder().encode(text).length, 0) > 8192) return;
  return Object.freeze({ userID, label: (nickname as string | undefined)?.trim() || userID });
}
