import type { TagAudioProjection } from "@/core/legacyTagMessage";
import { useMessageAccess } from "@/hooks/useMessagePrivacy";
import { messageOwner } from "@/core/messagePrivacyRuntime";
import { captureMessageAccess, messagePrivacyState } from "@/core/messagePrivacyRuntime";
import { useMessagePrivacy } from "@/hooks/useMessagePrivacy";
import clsx from "clsx";
import { FC, useRef, useState, useEffect } from "react";

import VoiceIcon from "@/svg/VoiceIcon";
import { feedbackToast } from "@/utils/common";

import { IMessageItemProps } from ".";
import styles from "./message-item.module.scss";

const VoiceMessageRender: FC<IMessageItemProps & { playableAudio?: TagAudioProjection }> = ({ message, isSender, disabled, conversationID, playableAudio }) => {
  const sourceOwner = useRef(messageOwner()).current;
  const sourceAccess = useMessageAccess(conversationID);
  const privacy = useMessagePrivacy(message, conversationID);
  const [audioEl] = useState<HTMLAudioElement>(() => new Audio());
  const [isPlaying, setIsPlaying] = useState(false);
  const sourceUrl = playableAudio?.sourceUrl ?? message.soundElem.sourceUrl;
  const duration = playableAudio ? playableAudio.durationSeconds : message.soundElem.duration;

  useEffect(() => {
    const audio = audioEl;
    if (privacy.expired) { audio.pause(); audio.removeAttribute("src"); setIsPlaying(false); return; }
    if (playableAudio) { audio.preload = "none"; audio.removeAttribute("src"); }
    else audio.src = sourceUrl;
    audio.onended = audio.onpause = () => setIsPlaying(false);
    audio.onplay = () => setIsPlaying(true);
    return () => { audio.onended = audio.onpause = audio.onplay = null; audio.pause(); audio.removeAttribute("src"); };
  }, [message.clientMsgID, sourceUrl, !!playableAudio, privacy.expired]);

  const playAudio = () => {
    if (!sourceAccess.current()) return;
    const access = captureMessageAccess(conversationID, sourceOwner);
    if (!access.current() || messagePrivacyState(message, conversationID).expired) return;
    if (isPlaying) {
      audioEl?.pause();
      setIsPlaying(false);
    } else {
      // A tag audio URL enters the media element only after the original access gates.
      if (playableAudio) audioEl.src = sourceUrl;
      audioEl
        ?.play()
        .then(() => { if (!access.current() || messagePrivacyState(message, conversationID).expired) { audioEl.pause(); audioEl.removeAttribute("src"); } })
        .catch((error) => { if (access.current()) feedbackToast({ error, msg: "play audio failed" }); });
    }
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
