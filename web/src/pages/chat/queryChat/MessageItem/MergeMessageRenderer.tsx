import { captureMessageRetention } from "@/core/messagePrivacyRuntime";
import { useMessagePrivacy } from "@/hooks/useMessagePrivacy";
import { FC, useMemo } from "react";
import { useReceivedMessageView } from "@/components/ReceivedMessageCard";

import Twemoji from "@/components/Twemoji";

import { IMessageItemProps } from ".";

const MergeMessageRenderer: FC<IMessageItemProps> = ({ message, showMergeModal, conversationID }) => {
  const privacy = useMessagePrivacy(message, conversationID);
  const source = useReceivedMessageView(message, conversationID);
  const retention = useMemo(() => captureMessageRetention(message, source.conversationID), [message, source]);
  const retainedSource = useMemo(() => ({ conversationID: source.conversationID, groupID: source.groupID,
    current: () => source.current() && retention.current(),
    canShare: () => source.canShare() && retention.current() }), [source, retention]);
  if (!privacy.allowsRetention || !source.current()) return <span role="status">含阅后即焚、已过期或未能核验内容的合并记录不可展示</span>;
  return (
    <div
      className="w-60 cursor-pointer rounded-md border border-[var(--gap-text)]"
      onClick={() => { if (source.current() && retention.current()) showMergeModal?.(message.mergeElem, retainedSource); }}
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
