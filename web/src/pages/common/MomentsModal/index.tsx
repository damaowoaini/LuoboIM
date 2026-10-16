import { sessionCoordinator } from "@/core/session/browser";
import { updateCachedMoment } from "@/core/momentsCache";
import { useHistoryTravel } from "ahooks";
import { Alert, Button, Empty, Spin } from "antd";
import { t } from "i18next";
import { CbEvents } from "open-im-sdk-wasm";
import { WSEvent } from "open-im-sdk-wasm/lib/types/entity";
import {
  forwardRef,
  ForwardRefRenderFunction,
  memo,
  useCallback,
  useEffect,
  useRef,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Virtuoso, VirtuosoHandle } from "react-virtuoso";

import { getMomentsByID, useUserMoments } from "@/api/moments";
import DraggableModalWrap from "@/components/DraggableModalWrap";
import OIMAvatar from "@/components/OIMAvatar";
import { OverlayVisibleHandle, useOverlayVisible } from "@/hooks/useOverlayVisible";
import { IMSDK } from "@/layout/MainContentWrap";
import { useUserStore } from "@/store";
import { feedbackToast } from "@/utils/common";
import { WorkMoments } from "@/types/moment";

import MomentsHeader from "./MomentsHeader";
import MomentsItemRender from "./MomentsItemRender";

export interface RouteTravel {
  nickname?: string;
  faceURL?: string;
  userID?: string;
  moments?: WorkMoments;
  updateMoments?: boolean;
}

const MomentsModalNew: ForwardRefRenderFunction<OverlayVisibleHandle> = (_, ref) => {
  const { isOverlayOpen, closeOverlay } = useOverlayVisible(ref);
  const virtuoso = useRef<VirtuosoHandle>(null);
  const headerRef = useRef<{ showMessages: () => void }>(null);
  const workMomentsUnreadCount = useUserStore((state) => state.workMomentsUnreadCount);

  const {
    value: routeTravel,
    setValue: setRouteTravel,
    backLength,
    go,
  } = useHistoryTravel<RouteTravel>({});
  const { isFetching, isError, fetchNextPage, hasNextPage, refetch, data } =
    useUserMoments(isOverlayOpen, routeTravel?.userID);
  const backStep = useRef(-1);

  const workMoments = data?.pages?.flat().filter((item) => Boolean(item)) ?? [];

  const queryClient = useQueryClient();

  useEffect(() => {
    const customMessageHandler = ({
      data: { key, data },
    }: WSEvent<{ key: string; data: string }>) => {
      if (key.includes("wm_")) {
        let updatedMoments: WorkMoments;
        try {
          updatedMoments = JSON.parse(data).body as WorkMoments;
        } catch {
          return;
        }
        if (!updatedMoments?.workMomentID || !updatedMoments?.userID) return;
        if (sessionCoordinator.current)
          updateCachedMoment(queryClient, sessionCoordinator.namespace, updatedMoments);
      }
    };
    IMSDK.on(CbEvents.OnRecvCustomBusinessMessage, customMessageHandler);
    return () => {
      IMSDK.off(CbEvents.OnRecvCustomBusinessMessage, customMessageHandler);
    };
  }, [routeTravel?.userID]);

  const endReached = () => {
    if (!isFetching && hasNextPage) {
      fetchNextPage();
    }
  };

  const updateRouteTravel = useCallback(
    (travel: RouteTravel) => {
      if (
        travel.userID &&
        (travel.userID === routeTravel?.userID ||
          travel.userID === routeTravel?.moments?.userID)
      )
        return;
      if (
        travel.moments?.workMomentID &&
        !travel.updateMoments &&
        travel.moments?.workMomentID === routeTravel?.moments?.workMomentID
      )
        return;

      if (travel.updateMoments && !routeTravel?.moments && travel.moments) return;

      if (travel.updateMoments) {
        backStep.current -= 1;
      } else {
        backStep.current = -1;
      }

      setRouteTravel({ ...travel });
    },
    [JSON.stringify(routeTravel)],
  );

  const backPreviousState = useCallback(() => go(backStep.current), []);

  const jumpToMoments = useCallback((workMomentID: string) => {
    getMomentsByID(workMomentID)
      .then(({ data: { workMoment } }) => {
        setRouteTravel({ moments: workMoment });
      })
      .catch((error) => feedbackToast({ error, msg: t("toast.getMomentsFailed") }));
  }, []);

  return (
    <DraggableModalWrap
      aria-label="工作圈"
      open={isOverlayOpen}
      title={null}
      footer={null}
      width={550}
      onCancel={closeOverlay}
      closeIcon={null}
      centered
      className="no-padding-modal"
      mask={false}
      ignoreClasses=".ignore-drag, .cursor-pointer, .ant-modal-wrap"
    >
      <div className="relative h-[80vh]">
        <MomentsHeader
          ref={headerRef}
          canBack={Boolean(backLength)}
          activeUserID={routeTravel?.userID}
          refetch={refetch}
          closeOverlay={closeOverlay}
          jumpToMoments={jumpToMoments}
          backPreviousState={backPreviousState}
        />

        {routeTravel?.moments?.workMomentID ? (
          <div className="h-full overflow-y-auto pt-12">
            <MomentsItemRender
              expanded
              moments={routeTravel.moments}
              activeUserID={routeTravel?.userID}
              backPreviousState={backPreviousState}
              updateRouteTravel={updateRouteTravel}
            />
          </div>
        ) : (
          <Virtuoso
            data={workMoments}
            ref={virtuoso}
            className="no-scrollbar"
            endReached={endReached}
            computeItemKey={(_, moments) => moments.workMomentID}
            components={{
              Header: () => (
                <>
                  <div className="relative flex h-[148px] items-end bg-gradient-to-br from-[#edf4ff] to-white px-5 pb-5 pt-14">
                    {routeTravel?.userID ? (
                      <div className="flex items-center">
                        <OIMAvatar
                          size={48}
                          src={routeTravel?.faceURL}
                          text={routeTravel?.nickname}
                        />

                        <div className="ml-3">
                          <div className="max-w-[280px] truncate font-semibold text-slate-800">{routeTravel?.nickname}</div>
                          <div className="mt-1 text-xs text-slate-500">动态</div>
                        </div>
                      </div>
                    ) : <div><h2 className="text-xl font-semibold text-slate-800">最近动态</h2><p className="mt-1 text-sm text-slate-500">分享近况，与好友交流。</p></div>}
                  </div>
                  {Boolean(workMomentsUnreadCount) && (
                    <div className="my-3 flex items-center justify-center">
                      <button type="button"
                        className="ignore-drag cursor-pointer rounded-md bg-[var(--primary)] px-3 py-1 text-sm text-white"
                        onClick={() => headerRef.current?.showMessages()}
                      >
                        {t("placeholder.multipleNewMessages", {
                          count: workMomentsUnreadCount,
                        })}
                      </button>
                    </div>
                  )}
                </>
              ),

              Footer: () =>
                isError ? (
                  <Alert
                    className="m-3"
                    type="error"
                    showIcon
                    message={t("toast.getMomentsFailed")}
                    action={
                      <Button
                        size="small"
                        loading={isFetching}
                        onClick={() => {
                          void refetch();
                        }}
                      >
                        {t("retry")}
                      </Button>
                    }
                  />
                ) : (
                  isFetching && (
                    <div className="mt-[20%] flex justify-center">
                      <Spin />
                    </div>
                  )
                ),

              EmptyPlaceholder: () =>
                !isFetching &&
                !isError && (
                  <Empty
                    image={Empty.PRESENTED_IMAGE_SIMPLE}
                    description={t("empty.noMoments")}
                  />
                ),
            }}
            itemContent={(_, moments) => (
              <MomentsItemRender
                moments={moments}
                activeUserID={routeTravel?.userID}
                backPreviousState={backPreviousState}
                updateRouteTravel={updateRouteTravel}
              />
            )}
          />
        )}
      </div>
    </DraggableModalWrap>
  );
};

export default memo(forwardRef(MomentsModalNew));
