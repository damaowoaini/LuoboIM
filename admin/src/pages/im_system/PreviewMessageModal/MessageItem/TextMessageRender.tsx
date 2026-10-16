import { FC } from "react";

import Twemoji from "@/components/Twemoji";

import { IMessageItemProps } from ".";
import styles from "./message-item.module.less";
import { MessageType } from '@/core/protocol';

const TextMessageRender: FC<IMessageItemProps> = ({ message }) => {
  let content = message.textElem?.content;
  if (message.contentType === MessageType.AtTextMessage) content = message.atTextElem?.text;

  if (message.contentType === MessageType.QuoteMessage) {
    content = message.quoteElem.text;
  }

  return (
    <Twemoji>
      <div className={styles.bubble} style={{ whiteSpace: "pre-wrap" }}>{content}</div>
    </Twemoji>
  );
};

export default TextMessageRender;
