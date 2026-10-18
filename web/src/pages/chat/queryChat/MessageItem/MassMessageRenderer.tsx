import { FC } from "react";
import { legacyTagContent } from "@/core/legacyTagMessage";

import { IMessageItemProps } from ".";
import styles from "./message-item.module.scss";
import CatchMessageRender from "./CatchMsgRenderer";
import VoiceMessageRender from "./VoiceMessageRender";

const MassMessageRenderer: FC<IMessageItemProps> = (props) => {
  const content = legacyTagContent(props.message);
  if (content?.kind === "text") return <div className={styles.bubble}>{content.text}</div>;
  if (content?.kind === "audio" && content.playableAudio) return <VoiceMessageRender {...props} playableAudio={content.playableAudio} />;
  return <CatchMessageRender />;
};

export default MassMessageRenderer;
