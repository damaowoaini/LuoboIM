import { Badge, Popover } from "antd";
import { t } from "i18next";
import {
  forwardRef,
  ForwardRefRenderFunction,
  memo,
  useCallback,
  useImperativeHandle,
  useRef,
  useState,
} from "react";

import { MomentsClearType, useClearUnreadMoments } from "@/api/moments";
import top_back from "@/assets/images/moments/top_back.png";
import top_close from "@/assets/images/moments/top_close.png";
import top_message from "@/assets/images/moments/top_message.png";
import top_publish from "@/assets/images/moments/top_publish.png";
import top_refresh from "@/assets/images/moments/top_refresh.png";
import { OverlayVisibleHandle } from "@/hooks/useOverlayVisible";
import { useUserStore } from "@/store";
import { sessionCoordinator } from "@/core/session/browser";
import { message as antdMessage } from "@/AntdGlobalComp";

import PublishModal from "../PublishModal";
import MomentsMessageList from "./MomentsMessageList";
import PublishMenu from "./PublishMenu";

interface IMomentsHeaderProps {
  canBack: boolean;
  activeUserID?: string;
  refetch: () => void;
  closeOverlay: () => void;
  backPreviousState: () => void;
  jumpToMoments: (workMomentID: string) => void;
}

const MomentsHeader: ForwardRefRenderFunction<
  { showMessages: () => void },
  IMomentsHeaderProps
> = (
  { canBack, activeUserID, refetch, closeOverlay, backPreviousState, jumpToMoments },
  ref,
) => {
  const messageListRef = useRef<{ refetch: () => void }>(null);
  const publishModalRef = useRef<OverlayVisibleHandle>(null);
  const [popoverState, setPopoverState] = useState({
    messageList: false,
    actions: false,
    withVideo: false,
  });
  const workMomentsUnreadCount = useUserStore((state) => state.workMomentsUnreadCount);
  const refreshWorkMomentsUnreadCount = useUserStore(
    (state) => state.getWorkMomentsUnreadCount,
  );
  const { isPending: clearLoading, mutateAsync: clearUnreadMoments } =
    useClearUnreadMoments();

  const preparePublish = useCallback((withVideo = false) => {
    publishModalRef.current?.openOverlay();
    setPopoverState({
      messageList: false,
      actions: false,
      withVideo,
    });
  }, []);

  const jumpToMomentsAndClose = useCallback((workMomentID: string) => {
    jumpToMoments(workMomentID);
    setPopoverState({
      messageList: false,
      actions: false,
      withVideo: false,
    });
  }, []);

  const messageListOpenChange = (visible: boolean) => {
    if (visible && workMomentsUnreadCount) {
      const generation = sessionCoordinator.generation;
      messageListRef.current?.refetch();
      void clearUnreadMoments(MomentsClearType.Count).then(() => {
        if (generation === sessionCoordinator.generation) void refreshWorkMomentsUnreadCount();
      }).catch(() => {
        if (generation === sessionCoordinator.generation) antdMessage.warning("暂时无法更新通知状态，请稍后重试。");
      });
    }
    setPopoverState((state) => ({ ...state, messageList: visible }));
  };

  const clearMessageList = useCallback(() => {
    const generation = sessionCoordinator.generation;
    void clearUnreadMoments(MomentsClearType.List).then(() => {
      if (generation === sessionCoordinator.generation) messageListRef.current?.refetch();
    }).catch(() => {
      if (generation === sessionCoordinator.generation) antdMessage.warning("通知清空失败，请稍后重试。");
    });
  }, []);

  useImperativeHandle(
    ref,
    () => ({ showMessages: () => messageListOpenChange(true) }),
    [workMomentsUnreadCount],
  );

  return (
    <div className="moments-header absolute top-0 z-10 flex w-full items-center bg-[var(--primary)] px-3 py-2">
      <div className="flex items-center">
        <button type="button" aria-label="关闭工作圈" title="关闭工作圈"
          className="ignore-drag flex h-7 w-7 cursor-pointer items-center justify-center rounded hover:bg-white/15"
          onClick={closeOverlay}
        >
          <img width={12} src={top_close} alt="" />
        </button>
        {canBack && (
          <button type="button" aria-label="返回上一级动态" title="返回上一级动态"
            className="ignore-drag flex h-7 w-7 cursor-pointer items-center justify-center rounded hover:bg-white/15"
            onClick={backPreviousState}
          >
            <img width={9} src={top_back} alt="" />
          </button>
        )}
      </div>
      <div className="ml-3 flex-1 text-center text-white">
        {t("placeholder.moments")}
      </div>
      <div className="flex items-center">
        <button type="button" aria-label="刷新动态" title="刷新动态" onClick={refetch}
          className="ignore-drag cursor-pointer rounded p-1 hover:bg-white/15">
          <img width={24} src={top_refresh} alt="" />
        </button>
        <Popover
          getPopupContainer={(trigger) => (trigger.closest(".ant-modal-content") as HTMLElement) || document.body}
          content={
            <MomentsMessageList
              ref={messageListRef}
              clearLoading={clearLoading}
              clearMessageList={clearMessageList}
              jumpToMoments={jumpToMomentsAndClose}
            />
          }
          trigger="click"
          placement="bottomRight"
          overlayClassName="ignore-drag"
          title={null}
          arrow={false}
          open={popoverState.messageList}
          onOpenChange={messageListOpenChange}
        >
          <Badge size="small" offset={[-6, 2]} dot={Boolean(workMomentsUnreadCount)}>
            <button type="button" aria-label="动态通知" title="动态通知" aria-expanded={popoverState.messageList}
              className="ignore-drag mx-1 cursor-pointer rounded p-1 hover:bg-white/15">
              <img width={24} src={top_message} alt="" />
            </button>
          </Badge>
        </Popover>
        <Popover
          getPopupContainer={(trigger) => (trigger.closest(".ant-modal-content") as HTMLElement) || document.body}
          content={<PublishMenu preparePublish={preparePublish} />}
          trigger="click"
          placement="bottom"
          overlayClassName="ignore-drag"
          title={null}
          arrow={false}
          open={popoverState.actions}
          onOpenChange={(vis) =>
            setPopoverState((state) => ({ ...state, actions: vis }))
          }
        >
          <button type="button" aria-label="发布动态" title="发布动态" aria-expanded={popoverState.actions}
            className="ignore-drag cursor-pointer rounded p-1 hover:bg-white/15">
            <img width={24} src={top_publish} alt="" />
          </button>
        </Popover>
      </div>
      <PublishModal
        ref={publishModalRef}
        withVideo={popoverState.withVideo}
        activeUserID={activeUserID}
      />
    </div>
  );
};

export default memo(forwardRef(MomentsHeader));
