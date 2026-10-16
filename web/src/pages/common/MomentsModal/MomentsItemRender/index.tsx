import { Popover } from "antd";
import { t } from "i18next";
import { memo, useRef, useState } from "react";

import { useDeleteMoments, useLikeMoments } from "@/api/moments";
import item_assign from "@/assets/images/moments/item_assign.png";
import item_comment from "@/assets/images/moments/item_comment.png";
import item_like from "@/assets/images/moments/item_like.png";
import item_liked from "@/assets/images/moments/item_liked.png";
import item_private from "@/assets/images/moments/item_private.png";
import liked from "@/assets/images/moments/liked.png";
import moment_actions from "@/assets/images/moments/moment_actions.png";
import OIMAvatar from "@/components/OIMAvatar";
import { useUserStore } from "@/store";
import { Comments, MomentPermission, WorkMoments } from "@/types/moment";
import { feedbackToast } from "@/utils/common";
import { formatMessageTime } from "@/utils/imCommon";

import { RouteTravel } from "..";
import CommentInput, { CommentInputHandle } from "./CommentInput";
import CommentRow from "./CommentRow";
import styles from "./moments-item-render.module.scss";
import MomentsMediaRow from "./MomentsMediaRow";

const MomentsItemRender = ({
  moments,
  activeUserID,
  updateRouteTravel,
  backPreviousState,
  expanded = false,
}: {
  moments: WorkMoments;
  activeUserID?: string;
  backPreviousState: () => void;
  updateRouteTravel: (data: RouteTravel) => void;
  expanded?: boolean;
}) => {
  const [actionVisible, setActionVisible] = useState(false);
  const inputRef = useRef<CommentInputHandle>(null);

  const selfUserID = useUserStore((state) => state.selfInfo.userID);

  const { isPending: deleteLoading, mutateAsync: deleteMoments } =
    useDeleteMoments(activeUserID);
  const { isPending: likeLoading, mutateAsync: updateMomentsLike } = useLikeMoments();

  const updateLikeState = () => {
    if (likeLoading) return;

    updateMomentsLike({ workMomentID: moments.workMomentID, like: !isSelfLiked })
      .then(({ data: { workMoment } }) =>
        updateRouteTravel({ moments: workMoment, updateMoments: true }),
      )
      .catch((error) => feedbackToast({ error, msg: t("toast.accessFailed") }));
    setActionVisible(false);
  };

  const startComment = (comment?: Comments) => {
    inputRef.current?.updateCommentState({
      visible: true,
      text: "",
      replyItem: comment ?? null,
    });
    setActionVisible(false);
  };

  const tryDeleteMoments = () => {
    if (deleteLoading) return;

    deleteMoments(moments.workMomentID)
      .then(backPreviousState)
      .catch((error) => feedbackToast({ error, msg: t("toast.accessFailed") }));
  };

  const atUserStr = moments.atUsers
    ?.reduce((acc, user) => `${acc + user.nickname}、`, "")
    ?.slice(0, -1);
  const likeUserStr = [...(moments.likeUsers ?? [])]
    .slice(0, 6)
    ?.reduce((acc, user) => `${acc + user.nickname}、`, "")
    ?.slice(0, -1);
  const likeCount = moments.likeUsers?.length || 0;
  const likeSuffix =
    likeCount > 6 ? t("placeholder.somePersonLike", { num: likeCount }) : "";
  const isSelfLiked = moments.likeUsers?.some((user) => user.userID === selfUserID);

  const isPrivate = moments.permission === MomentPermission.Private;
  const isAssign = moments.permission > MomentPermission.Private;

  const isSelfMoments = moments.userID === selfUserID;

  return (
    <div className="ignore-drag mx-5 mt-5 flex">
      <button type="button" aria-label={`查看${moments.nickname}的动态`}
        className="h-fit shrink-0 cursor-pointer rounded"
        onClick={() =>
          updateRouteTravel({
            userID: moments.userID,
            nickname: moments.nickname,
            faceURL: moments.faceURL,
          })
        }
      ><OIMAvatar src={moments.faceURL} text={moments.nickname} /></button>
      <div className="ml-3 min-w-0 flex-1">
        <button type="button"
          className="w-fit max-w-[200px] cursor-pointer truncate text-left font-medium text-[var(--moment-text)]"
          onClick={() => updateRouteTravel({ moments })}
        >
          {moments.nickname}
        </button>
        {expanded ? <div className="mb-2 whitespace-pre-wrap break-words">{moments.content.text}</div> : <button type="button"
          className="mb-2 line-clamp-6 cursor-pointer whitespace-pre-wrap break-words text-left"
          onClick={() => updateRouteTravel({ moments })}
        >
          {moments.content.text}
        </button>}

        <MomentsMediaRow moments={moments} />

        {Boolean(atUserStr) && (
          <div className="mb-2 text-xs text-[var(--sub-text)]">{`${t(
            "placeholder.mentioned",
          )}：${atUserStr}`}</div>
        )}
        <div className="mb-2 flex w-full items-center justify-between">
          <div className="flex items-center">
            <div className="text-xs text-[var(--sub-text)]">
              {formatMessageTime(moments.createTime)}
            </div>
            {isPrivate && <img className="ml-4" width={15} src={item_assign} alt="" />}
            {isAssign && <img className="ml-4" width={9} src={item_private} alt="" />}
            {isSelfMoments && (
              <button type="button"
                className="ml-3 cursor-pointer text-xs text-[var(--moment-text)]"
                onClick={tryDeleteMoments}
              >
                {t("placeholder.delete")}
              </button>
            )}
          </div>
          <Popover
            getPopupContainer={(trigger) => (trigger.closest(".ant-modal-content") as HTMLElement) || document.body}
            content={
              <MoreActionContent
                isSelfLiked={isSelfLiked}
                updateLikeState={updateLikeState}
                startComment={startComment}
              />
            }
            title={null}
            trigger="click"
            placement="left"
            overlayClassName={styles["action-popover"]}
            open={actionVisible}
            onOpenChange={(vis) => setActionVisible(vis)}
          >
            <button type="button" aria-label="动态操作" title="动态操作" aria-expanded={actionVisible}
              className="cursor-pointer rounded p-1 hover:bg-[var(--primary-active)]">
              <img src={moment_actions} width={22} alt="" />
            </button>
          </Popover>
        </div>
        {Boolean(likeCount) && (
          <div className="mb-2 text-xs text-[var(--moment-text)]">
            <img
              src={liked}
              width={12}
              className="mr-1.5 inline-block align-middle"
              alt=""
            />
            <span>{likeUserStr + likeSuffix}</span>
          </div>
        )}
        <div className="overflow-hidden rounded-sm">
          {moments.comments?.map((comment) => (
            <CommentRow
              key={comment.commentID}
              selfUserID={selfUserID}
              isSelfMoments={isSelfMoments}
              workMomentID={moments.workMomentID}
              comment={comment}
              startComment={startComment}
            />
          ))}
        </div>
        <CommentInput
          ref={inputRef}
          workMomentID={moments.workMomentID}
          updateRouteTravel={updateRouteTravel}
        />
      </div>
    </div>
  );
};

export default memo(MomentsItemRender);

const MoreActionContent = memo(
  ({
    isSelfLiked,
    startComment,
    updateLikeState,
  }: {
    isSelfLiked?: boolean;
    startComment: (comment?: Comments) => void;
    updateLikeState: () => void;
  }) => {
    const actionStyle = "flex cursor-pointer items-center px-2 py-1 text-xs text-white";
    return (
      <div className="flex rounded-sm bg-[#313e52]">
        <button type="button" className={actionStyle} onClick={updateLikeState} aria-pressed={isSelfLiked}>
          <img width={16} src={isSelfLiked ? item_liked : item_like} alt="" />
          <span className="ml-1">{isSelfLiked ? "取消点赞" : t("placeholder.toLike")}</span>
        </button>
        <div className="my-1 border-r border-[var(--base-black)]" />
        <button type="button" data-ignore className={actionStyle} onClick={() => startComment()}>
          <img data-ignore width={16} src={item_comment} alt="" />
          <span data-ignore className="ml-1">
            {t("placeholder.comment")}
          </span>
        </button>
      </div>
    );
  },
);
