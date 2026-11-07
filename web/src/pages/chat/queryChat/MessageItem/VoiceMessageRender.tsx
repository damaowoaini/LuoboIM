import type { TagAudioProjection } from "@/core/legacyTagMessage";
import { useMessageAccess } from "@/hooks/useMessagePrivacy";
import { messageOwner } from "@/core/messagePrivacyRuntime";
import { captureMessageAccess, messagePrivacyState } from "@/core/messagePrivacyRuntime";
import { useMessagePrivacy } from "@/hooks/useMessagePrivacy";
import { claimVoicePlayback, type VoicePlaybackLease } from "@/core/voicePlayback";
import clsx from "clsx";
import { FC, useCallback, useRef, useState, useEffect } from "react";

import VoiceIcon from "@/svg/VoiceIcon";
import { feedbackToast } from "@/utils/common";

import type { IMessageItemProps } from ".";
import styles from "./message-item.module.scss";

type VoiceIntent = { revision: number; audio: HTMLAudioElement; state: "pending" | "playing" | "paused"; lease?: VoicePlaybackLease };

function idleVoiceReceiver() {
  const audio = new Audio();
  audio.preload = "none";
  return audio;
}

const VoiceMessageRender: FC<IMessageItemProps & { playableAudio?: TagAudioProjection }> = ({ message, isSender, disabled, conversationID, playableAudio }) => {
  const sourceOwner = useRef(messageOwner()).current;
  const sourceAccess = useMessageAccess(conversationID);
  const privacy = useMessagePrivacy(message, conversationID);
  const [firstReceiver] = useState(idleVoiceReceiver);
  const idleReceiver = useRef<HTMLAudioElement | undefined>(firstReceiver);
  const playback = useRef<{ mounted: boolean; revision: number; resumeTime: number; intent?: VoiceIntent }>({ mounted: false, revision: 0, resumeTime: 0 });
  const [isPlaying, setIsPlaying] = useState(false);
  const sourceUrl = playableAudio?.sourceUrl ?? message.soundElem.sourceUrl;
  const duration = playableAudio ? playableAudio.durationSeconds : message.soundElem.duration;

  const retire = useCallback((expected?: VoiceIntent, notify = true) => {
    const current = playback.current;
    if (expected && current.intent !== expected) return;
    // Invalidate before pause/removeAttribute can dispatch events or reject play.
    current.revision++;
    const intent = current.intent;
    current.intent = undefined;
    intent?.lease?.release();
    if (intent) {
      const audio = intent.audio;
      current.resumeTime = Number.isFinite(audio.currentTime) && audio.currentTime > 0 ? audio.currentTime : 0;
      audio.onloadedmetadata = audio.onended = audio.onpause = audio.onplay = null;
      audio.pause();
      audio.removeAttribute("src");
      audio.load();
    }
    if (notify && current.mounted) setIsPlaying(false);
  }, []);

  useEffect(() => {
    playback.current.mounted = true;
    if (!privacy.expired && !idleReceiver.current) idleReceiver.current = idleVoiceReceiver();
    const idle = idleReceiver.current;
    retire();
    playback.current.resumeTime = 0;
    return () => {
      playback.current.mounted = false;
      retire(undefined, false);
      playback.current.resumeTime = 0;
      // Only this effect's still-unclaimed receiver can be disposed here.
      if (idle && idleReceiver.current === idle) {
        idleReceiver.current = undefined;
        idle.onloadedmetadata = idle.onended = idle.onpause = idle.onplay = null;
        idle.pause(); idle.removeAttribute("src"); idle.load();
      }
    };
  }, [message.clientMsgID, sourceUrl, !!playableAudio, privacy.expired, conversationID, retire]);

  const playAudio = () => {
    if (!sourceAccess.current()) return;
    const access = captureMessageAccess(conversationID, sourceOwner);
    if (!access.current() || messagePrivacyState(message, conversationID).expired) return;
    if (!playback.current.mounted) return;
    // A pending play is also an owned intent: the next click cancels it.
    const previous = playback.current.intent;
    if (previous && previous.state !== "paused") { retire(); return; }
    if (previous) retire();

    // Consume the empty idle receiver once. Later intents use new receivers,
    // so retired native events cannot reach a newer intent's handlers.
    const resumeTime = playback.current.resumeTime;
    const audio = idleReceiver.current ?? idleVoiceReceiver();
    idleReceiver.current = undefined;
    const intent: VoiceIntent = { revision: ++playback.current.revision, audio, state: "pending" };
    playback.current.intent = intent;
    intent.lease = claimVoicePlayback(audio, () => retire(intent));
    const current = () => playback.current.mounted && playback.current.intent === intent &&
      playback.current.revision === intent.revision && intent.lease?.current() === true &&
      sourceAccess.current() && access.current() && !messagePrivacyState(message, conversationID).expired;
    audio.onplay = () => { if (current()) { intent.state = "playing"; setIsPlaying(true); } else retire(intent); };
    audio.onended = () => { if (current()) { retire(intent); playback.current.resumeTime = 0; } };
    audio.onloadedmetadata = () => { if (current() && resumeTime > 0) audio.currentTime = resumeTime; };
    // A native pause is not evidence that we cancelled this request. Keep its
    // intent until settlement, so a subsequent real play rejection still reports.
    audio.onpause = () => { if (current()) { intent.state = "paused"; setIsPlaying(false); } };
    // Ordinary and tag URLs enter this exact receiver only after the existing
    // account/history/privacy gates, never from a preload on render.
    audio.src = sourceUrl;
    audio.play()
      .then(() => { if (!current()) retire(intent); })
      .catch((error) => {
        const report = current();
        retire(intent);
        // An owned cancellation is stale by identity, not by error name.
        // A still-current AbortError is a real failure and remains visible.
        if (report) feedbackToast({ error, msg: "play audio failed" });
      });
  };

  if (privacy.expired) return <span role="status">消息已过期或已移除</span>;
  return (
    <div
      role="button"
      tabIndex={0}
      aria-label={isPlaying ? "暂停语音" : "播放语音"}
      aria-pressed={isPlaying}
      onKeyDown={event => { if (!event.repeat && (event.key === "Enter" || event.key === " ")) { event.preventDefault(); playAudio(); } }}
      className={clsx(
        styles.bubble,
        "flex cursor-pointer items-center !py-2 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-600",
        !isSender && "flex-row-reverse",
        disabled && "justify-end",
      )}
      onClick={playAudio}
    >
      <VoiceIcon
        style={{ transform: isSender ? "rotateY(180deg)" : "none" }}
        playing={isPlaying}
      />
      <span
        className={isSender ? "mr-1" : "ml-1"}
      >{playableAudio && duration === undefined ? "标签语音" : `${duration} ‘’`}</span>
    </div>
  );
};

export default VoiceMessageRender;
