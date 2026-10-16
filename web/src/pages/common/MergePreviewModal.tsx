import { useMessagePrivacy } from "@/hooks/useMessagePrivacy";
import { CloseOutlined } from "@ant-design/icons";
import { Modal } from "antd";
import { MergeElem } from "open-im-sdk-wasm/lib/types/entity";
import { forwardRef, ForwardRefRenderFunction, memo } from "react";

import { OverlayVisibleHandle, useOverlayVisible } from "@/hooks/useOverlayVisible";

import MessageItem from "../chat/queryChat/MessageItem";

interface IMergePreviewModalProps {
  mergeData?: MergeElem;
}

const MergePreviewModal: ForwardRefRenderFunction<
  OverlayVisibleHandle,
  IMergePreviewModalProps
> = ({ mergeData }, ref) => {
  const privacy = useMessagePrivacy({ mergeElem: mergeData });
  const { isOverlayOpen, closeOverlay } = useOverlayVisible(ref);
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
      <div className="h-[65vh] overflow-auto">
        {privacy.allowsRetention && mergeData?.multiMessage.map((message) => (
          <MessageItem
            message={message}
            isSender={false}
            disabled
            key={message.clientMsgID}
          />
        ))}
      </div>
    </Modal>
  );
};

export default memo(forwardRef(MergePreviewModal));
