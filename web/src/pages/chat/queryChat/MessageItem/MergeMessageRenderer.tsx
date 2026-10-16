import { captureMessageRetention } from "@/core/messagePrivacyRuntime";
import { useMessagePrivacy } from "@/hooks/useMessagePrivacy";
import { FC } from "react";

import Twemoji from "@/components/Twemoji";

import { IMessageItemProps } from ".";

const MergeMessageRenderer: FC<IMessageItemProps> = ({ message, showMergeModal }) => {
  const privacy = useMessagePrivacy(message);
  if (!privacy.allowsRetention) return <span role="status">含阅后即焚、已过期或未能核验内容的合并记录不可展示</span>;
  return (
    <div
      className="w-60 cursor-pointer rounded-md border border-[var(--gap-text)]"
      onClick={() => { if (captureMessageRetention(message).current()) showMergeModal?.(message.mergeElem); }}
    >
      <div className="border-b border-[var(--gap-text)] px-4 py-2.5">
        {message.mergeElem.title}
      </div>
      <ul className="px-4 py-2.5 text-xs text-[var(--sub-text)]">
        {message.mergeElem.abstractList.map((item, idx) => (
          <Twemoji key={idx}>
            <li className="mb-2 line-clamp-3 break-all last:mb-0">{item}</li>
          </Twemoji>
        ))}
      </ul>
    </div>
  );
};

export default MergeMessageRenderer;
