import { MessageType } from "open-im-sdk-wasm";
import { FC } from "react";

import Twemoji from "@/components/Twemoji";
import { escapeHtml, formatBr, sanitizeHtml } from "@/utils/common";
import { formatAtText, formatLink } from "@/utils/imCommon";

import { IMessageItemProps } from ".";
import styles from "./message-item.module.scss";

const TextMessageRender: FC<IMessageItemProps> = ({ message }) => {
  let content = message.textElem?.content;

  if (message.contentType === MessageType.QuoteMessage) {
    content = message.quoteElem.text;
  }
  if (message.contentType === MessageType.AtTextMessage) {
    content = formatAtText(message.atTextElem);
  } else {
    content = escapeHtml(content ?? "");
  }

  content = formatLink(content);
  content = formatBr(content);

  return (
    <Twemoji>
      <div
        className={styles.bubble}
        dangerouslySetInnerHTML={{ __html: sanitizeHtml(content) }}
      ></div>
    </Twemoji>
  );
};

export default TextMessageRender;
