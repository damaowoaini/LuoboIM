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

const VoiceMessageRender: FC<IMessageItemProps> = ({ message, isSender, disabled, conversationID }) => {
  const sourceOwner = useRef(messageOwner()).current;
  const sourceAccess = useMessageAccess(conversationID);
  const privacy = useMessagePrivacy(message, conversationID);
  const audioEl = useRef<HTMLAudioElement>(new Audio());
  const [isPlaying, setIsPlaying] = useState(false);

  useEffect(() => {
    const audio = audioEl.current;
    if (privacy.expired) { audio.pause(); audio.removeAttribute("src"); setIsPlaying(false); return; }
    audio.src = message.soundElem.sourceUrl;
    audio.onended = audio.onpause = () => setIsPlaying(false);
    audio.onplay = () => setIsPlaying(true);
    return () => { audio.onended = audio.onpause = audio.onplay = null; audio.pause(); audio.removeAttribute("src"); };
  }, [message.clientMsgID, message.soundElem.sourceUrl, privacy.expired]);

  const playAudio = () => {
    if (!sourceAccess.current()) return;
    const access = captureMessageAccess(conversationID, sourceOwner);
    if (!access.current() || messagePrivacyState(message, conversationID).expired) return;
    if (isPlaying) {
      audioEl.current?.pause();
      setIsPlaying(false);
    } else {
      audioEl.current
        ?.play()
        .then(() => { if (!access.current() || messagePrivacyState(message, conversationID).expired) { audioEl.current.pause(); audioEl.current.removeAttribute("src"); } })
        .catch((error) => { if (access.current()) feedbackToast({ error, msg: "play audio failed" }); });
    }
  };

  if (privacy.expired) return <span role="status">消息已过期或已移除</span>;
  return (
    <div
      className={clsx(
        styles.bubble,
        "flex cursor-pointer items-center !py-2",
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
      >{`${message.soundElem.duration} ‘’`}</span>
    </div>
  );
};

export default VoiceMessageRender;
