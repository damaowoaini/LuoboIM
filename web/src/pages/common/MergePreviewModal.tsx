import { MessageCardSourceContext } from "@/components/ReceivedMessageCard";
import type { MessageCardSource } from "@/core/messageCard";
import { useMessagePrivacy } from "@/hooks/useMessagePrivacy";
import { CloseOutlined } from "@ant-design/icons";
import { Modal } from "antd";
import { MergeElem } from "open-im-sdk-wasm/lib/types/entity";
import { forwardRef, ForwardRefRenderFunction, memo, useEffect, useImperativeHandle, useMemo, useRef } from "react";

import { OverlayVisibleHandle, useOverlayVisible } from "@/hooks/useOverlayVisible";

import MessageItem from "../chat/queryChat/MessageItem";

interface IMergePreviewModalProps {
  mergeData?: MergeElem;
  source?: MessageCardSource;
}

const MergePreviewModal: ForwardRefRenderFunction<
  OverlayVisibleHandle,
  IMergePreviewModalProps
> = ({ mergeData, source }, ref) => {
  const privacy = useMessagePrivacy({ mergeElem: mergeData }, source?.conversationID);
  const visibilityRef = useRef<OverlayVisibleHandle>(null);
  const { isOverlayOpen, closeOverlay: hide, openOverlay: show } = useOverlayVisible(visibilityRef);
  const turn = useRef(0);
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; ++turn.current; }; }, []);
  const live = useRef({ mergeData, source, open: isOverlayOpen });
  live.current = { mergeData, source, open: isOverlayOpen };
  const closeOverlay = () => { ++turn.current; live.current.open = false; hide(); };
  useImperativeHandle(ref, () => ({ isOverlayOpen,
    openOverlay: () => { ++turn.current; show(); }, closeOverlay }));
  const sourceView = useMemo<MessageCardSource>(() => {
    const epoch = turn.current;
    const current = () => Boolean(mounted.current && live.current.open && live.current.mergeData === mergeData &&
      live.current.source === source && turn.current === epoch && source?.current());
    return { conversationID: source?.conversationID ?? "", groupID: source?.groupID, current,
      canShare: () => current() && Boolean(source?.canShare()) };
  }, [mergeData, source, isOverlayOpen]);
  return (
    <Modal
      title={null}
      footer={null}
      centered
      open={isOverlayOpen}
      closable={false}
      width={680}
      onCancel={closeOverlay}
      styles={{
        mask: {
          opacity: 0,
          transition: "none",
        },
      }}
      className="no-padding-modal"
      maskTransitionName=""
    >
      <div className="flex h-16 items-center justify-between bg-[var(--gap-text)] px-7">
        <div className="font-medium">{privacy.allowsRetention ? mergeData?.title : "合并记录不可展示"}</div>
        <CloseOutlined
          className="cursor-pointer text-[var(--sub-text)]"
          rev={undefined}
          onClick={closeOverlay}
        />
      </div>
      <MessageCardSourceContext.Provider value={sourceView}>
      <div className="h-[65vh] overflow-auto">
        {privacy.allowsRetention && sourceView.current() && mergeData?.multiMessage.map((message) => (
          <MessageItem
            message={message}
            isSender={false}
            conversationID={sourceView.conversationID}
            disabled
            key={message.clientMsgID}
          />
        ))}
      </div>
      </MessageCardSourceContext.Provider>
    </Modal>
  );
};

export default memo(forwardRef(MergePreviewModal));
