// OpenIM custom envelope (110); avoid initializing the SDK Worker in a view helper.
export type TagAudioProjection = Readonly<{ sourceUrl: string; durationSeconds?: number }>;
type LegacyTagContent = { kind: "text"; text: string } |
  { kind: "audio"; durationSeconds?: number; playableAudio?: TagAudioProjection };
const record = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === "object" && !Array.isArray(value);

function tagData(message: unknown): Record<string, unknown> | undefined {
  if (!record(message) || message.contentType !== 110 || !record(message.customElem)) return;
  const raw = message.customElem.data;
  if (typeof raw !== "string" || raw.length > 65536) return;
  try {
    const decoded: unknown = JSON.parse(raw);
    if (record(decoded) && decoded.customType === 903 && record(decoded.data)) return decoded.data;
  } catch { /* Invalid custom data keeps the existing unsupported-message fallback. */ }
}

/** A view of the original custom envelope; never a native sound element. */
export function legacyTagContent(message: unknown): LegacyTagContent | undefined {
  const data = tagData(message);
  if (!data) return;
  if (data.textElem != null) {
    if (record(data.textElem) && typeof data.textElem.content === "string") return { kind: "text", text: data.textElem.content };
    return;
  }
  if (!record(data.soundElem)) return;
  const { sourceUrl, duration } = data.soundElem;
  const durationValid = duration == null || typeof duration === "number" && Number.isSafeInteger(duration) && duration >= 0;
  const durationSeconds = durationValid && typeof duration === "number" ? duration : undefined;
  let playableAudio: TagAudioProjection | undefined;
  if (durationValid && typeof sourceUrl === "string" && /^https:\/\//i.test(sourceUrl)) {
    try {
      const url = new URL(sourceUrl);
      const authority = sourceUrl.slice(sourceUrl.indexOf("://") + 3).split(/[/?#]/, 1)[0];
      const userInfo = authority.includes("@") ? authority.slice(0, authority.lastIndexOf("@")) : "";
      if (url.protocol === "https:" && url.hostname && !url.username && !url.password && !userInfo) {
        playableAudio = { sourceUrl, durationSeconds };
      }
    } catch { /* A received path or malformed URL cannot become playable media. */ }
  }
  return { kind: "audio", durationSeconds, playableAudio };
}

export function legacyTagSummary(message: unknown): string | undefined {
  const content = legacyTagContent(message);
  if (content?.kind === "text") return content.text;
  if (content?.kind === "audio") return `[标签语音]${content.durationSeconds === undefined ? "" : ` ${content.durationSeconds} 秒`}${content.playableAudio ? "" : "（当前版本暂不支持播放）"}`;
}

// Forwarding is denied by the old voice identity, even when its media is unsafe.
// Text wins only for rendering; a retained raw sound element still forbids sharing.
export function isLegacyTagVoice(message: unknown): boolean {
  const data = tagData(message);
  return !!data && data.soundElem != null;
}

/** Covers one source, a new merge request and already-merged history bodies. */
export function canForwardLegacyTagMessages(source: unknown): boolean {
  const pending = Array.isArray(source) ? [...source] : [source];
  let visited = 0;
  while (pending.length) {
    if (++visited > 4096 || pending.length > 4096) return false;
    const message: unknown = pending.pop();
    if (isLegacyTagVoice(message)) return false;
    if (!record(message)) continue;
    for (const children of [message.messageList, record(message.mergeElem) ? message.mergeElem.multiMessage : undefined]) {
      if (!Array.isArray(children)) continue;
      if (pending.length + children.length > 4096) return false;
      pending.push(...children);
    }
  }
  return true;
}
