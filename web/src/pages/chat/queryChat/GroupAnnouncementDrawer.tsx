import type { ConversationSendScope } from "@/core/messageSendIntent";
import { sessionCoordinator } from "@/core/session/browser";
import { Button, Divider, Drawer, Input } from "antd";
import { t } from "i18next";
import { GroupItem, GroupMemberItem } from "open-im-sdk-wasm/lib/types/entity";
import {
  forwardRef,
  ForwardRefRenderFunction,
  memo,
  useEffect,
  useReducer,
  useRef,
  useState,
} from "react";

import empty_announcement from "@/assets/images/chatSetting/empty_announcement.png";
import OIMAvatar from "@/components/OIMAvatar";
import { OverlayVisibleHandle, useOverlayVisible } from "@/hooks/useOverlayVisible";
import { IMSDK } from "@/layout/MainContentWrap";
import { useConversationStore } from "@/store";
import { escapeHtml, feedbackToast, formatBr, sanitizeHtml } from "@/utils/common";
import { formatMessageTime } from "@/utils/imCommon";

const initialState = {
  editing: false,
  loading: false,
};

type ActionType = "editing" | "loading" | "done";

const reducer = (state: typeof initialState, action: { type: ActionType }) => {
  switch (action.type) {
    case "editing":
      return { ...state, editing: true, loading: false };
    case "loading":
      return { ...state, editing: true, loading: true };
    case "done":
      return { ...state, editing: false, loading: false };
    default:
      throw new Error(`Unhandled action type: ${action.type}`);
  }
};

// Local current-view policy only. Server group administration ACL remains final.
// A personal/group send mute does not remove an administrator's edit ability.
const canManageAnnouncement = (groupID: string, actor: string) => {
  const { currentGroupInfo: group, currentMemberInGroup: member } = useConversationStore.getState();
  return sessionCoordinator.current?.userID === actor && group?.groupID === groupID &&
    [0, 1, 3].includes(group.status) && member?.groupID === groupID && member.userID === actor &&
    (member.roleLevel === 60 || member.roleLevel === 100);
};
type EditLease = { scope: ConversationSendScope; groupID: string; actor: string };
const GroupAnnouncementDrawer: ForwardRefRenderFunction<OverlayVisibleHandle, unknown> = (_, ref) => {
  const [notification, setNotification] = useState<string>();
  const [editState, dispatch] = useReducer(reducer, initialState);
  const currentGroup = useConversationStore((state) => state.currentGroupInfo);
  const currentConversation = useConversationStore((state) => state.currentConversation);
  const currentMember = useConversationStore((state) => state.currentMemberInGroup);
  const { isOverlayOpen, closeOverlay } = useOverlayVisible(ref);
  const renderScope = useConversationStore.getState().captureConversationSendScope();
  const renderOwner = sessionCoordinator.current, renderGeneration = sessionCoordinator.generation;
  const renderActor = renderOwner?.userID, renderIssuer = renderOwner?.issuer, renderTenant = renderOwner?.tenant.serverID;
  const canEdit = Boolean(currentGroup && renderActor && renderScope?.current() && renderScope.target.kind === "group" &&
    renderScope.target.id === currentGroup.groupID && canManageAnnouncement(currentGroup.groupID, renderActor));
  const editor = useRef<EditLease | undefined>(undefined);
  const turn = useRef(0), busy = useRef(false), mounted = useRef(true), open = useRef(isOverlayOpen);
  open.current = isOverlayOpen;
  const observedOpen = useRef(isOverlayOpen);
  if (observedOpen.current !== isOverlayOpen) { observedOpen.current = isOverlayOpen; ++turn.current; }
  const renderTurn = turn.current, renderedLease = editor.current;
  const current = (lease: EditLease) => mounted.current && open.current && editor.current === lease &&
    lease.scope.current() && canManageAnnouncement(lease.groupID, lease.actor);
  const discardEditor = () => {
    ++turn.current; editor.current = undefined; busy.current = false;
    setNotification(undefined); dispatch({ type: "done" });
  };
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; ++turn.current; editor.current = undefined; busy.current = false; };
  }, []);
  useEffect(() => {
    const lease = editor.current;
    if (lease && !current(lease)) discardEditor();
  }, [isOverlayOpen, currentConversation, currentGroup, currentMember, renderOwner, renderGeneration, renderActor, renderIssuer, renderTenant]);
  useEffect(() => {
    // A same-view SDK/event refresh must not overwrite an in-progress draft.
    if (!editor.current) setNotification(currentGroup?.notification);
  }, [currentGroup?.groupID, currentGroup?.notification, isOverlayOpen]);

  const editOrPublish = async () => {
    if (!mounted.current || !open.current || busy.current || renderTurn !== turn.current) return;
    if (!editState.editing) {
      // Use this render's scope/actor, never acquire a new owner from an old
      // retained UI callback. Ordinary same-CID object refresh remains valid.
      if (!currentGroup || !renderActor || !renderScope?.current() || renderScope.target.kind !== "group" ||
          renderScope.target.id !== currentGroup.groupID || !canManageAnnouncement(currentGroup.groupID, renderActor)) return;
      editor.current = { scope: renderScope, groupID: renderScope.target.id, actor: renderActor };
      setNotification(currentGroup.notification); dispatch({ type: "editing" }); return;
    }
    const lease = renderedLease;
    if (!lease || !current(lease)) return;
    const draft = notification ?? "", operation = ++turn.current;
    busy.current = true; dispatch({ type: "loading" });
    const maySettle = () => turn.current === operation && current(lease);
    try {
      if (!maySettle()) return;
      await IMSDK.setGroupInfo({ groupID: lease.groupID, notification: draft });
      // A fulfilled SDK ACK may concern the old target; it does not authorize
      // closing/replacing a newer editor. No native cancellation is claimed.
      if (maySettle()) { busy.current = false; editor.current = undefined; dispatch({ type: "done" }); }
    } catch {
      if (maySettle()) {
        busy.current = false; dispatch({ type: "editing" });
        feedbackToast({ error: new Error("发布结果未确认，请核实当前群公告，不会自动重发；编辑文字已保留。") });
      }
    }
  };

  return (
    <Drawer
      title={t("placeholder.groupAnnouncement")}
      placement="right"
      rootClassName="chat-drawer"
      onClose={closeOverlay}
      open={isOverlayOpen}
      afterOpenChange={(vis) => {
        if (!vis && !open.current) discardEditor();
      }}
      maskClassName="opacity-0"
      maskMotion={{
        visible: false,
      }}
      width={450}
      getContainer={"#chat-container"}
    >
      <div className="relative m-5.5 flex h-full flex-col justify-center">
        <div className="flex flex-1 flex-col overflow-hidden">
          {!editState.editing ? (
            <NotificationContent currentGroup={currentGroup} visible={isOverlayOpen} />
          ) : (
            <Input.TextArea
              showCount
              value={notification}
              maxLength={250}
              spellCheck={false}
              bordered={false}
              placeholder={t("placeholder.pleaseEnter")}
              style={{ height: "72%", resize: "none" }}
              styles={{
                textarea: {
                  padding: 0,
                },
              }}
              disabled={editState.loading}
              onChange={(e) => {
                if (renderedLease && renderTurn === turn.current && current(renderedLease) && !busy.current)
                  setNotification(e.target.value);
              }}
            />
          )}

          <div className="h-20" />
        </div>
        {!canEdit ? (
          <Divider className="border-1 absolute bottom-6 border-[var(--gap-text)] px-20 !text-xs !text-[var(--sub-text)]">
            {t("placeholder.needManageEdit")}
          </Divider>
        ) : (
          <div className="absolute bottom-6 right-0">
            <Button
              className="px-5"
              type="primary"
              onClick={editOrPublish}
              loading={editState.loading}
            >
              {editState.editing ? t("placeholder.publish") : t("placeholder.edit")}
            </Button>
          </div>
        )}
      </div>
    </Drawer>
  );
};

export default memo(forwardRef(GroupAnnouncementDrawer));

const NotificationContent = ({ currentGroup, visible }: { currentGroup?: GroupItem; visible: boolean }) => {
  const currentConversation = useConversationStore(state => state.currentConversation);
  type Author = { scope: ConversationSendScope; owner: typeof sessionCoordinator.current;
    groupID: string; userID: string; member: GroupMemberItem };
  const [author, setAuthor] = useState<Author>();
  // Antd keeps Drawer children mounted by default. Closed requests must not
  // be allowed to settle into a later open of the same group/author.
  const view = useRef(visible); view.current = visible;
  const read = useRef<Omit<Author, "member"> | undefined>(undefined), mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; read.current = undefined; }; }, []);
  useEffect(() => {
    if (!visible) { read.current = undefined; setAuthor(undefined); return; }
    const groupID = currentGroup?.groupID, userID = currentGroup?.notificationUserID;
    const previous = read.current;
    if (previous && previous.groupID === groupID && previous.userID === userID && previous.scope.current()) return;
    read.current = undefined; setAuthor(undefined);
    const scope = useConversationStore.getState().captureConversationSendScope();
    const owner = sessionCoordinator.current;
    if (!groupID || !userID || !owner || !scope?.current() || scope.target.kind !== "group" || scope.target.id !== groupID) return;
    const request = { scope, owner, groupID, userID }; read.current = request;
    void (async () => {
      try {
        const { data } = await IMSDK.getSpecifiedGroupMembersInfo({ groupID, userIDList: [userID] });
        if (!mounted.current || !view.current || read.current !== request || !scope.current() || owner !== sessionCoordinator.current ||
            !Array.isArray(data) || data.length !== 1 || data[0].groupID !== groupID || data[0].userID !== userID) return;
        setAuthor({ ...request, member: data[0] });
      } catch { /* An obsolete/current failed author read retains the generic label, without a global stale toast. */ }
    })();
  }, [visible, currentGroup?.notificationUserID, currentGroup?.groupID, currentConversation]);
  const notificationOpUser = visible && author && author.scope.current() && author.owner === sessionCoordinator.current &&
    author.groupID === currentGroup?.groupID && author.userID === currentGroup?.notificationUserID ? author.member : undefined;

  return (
    <>
      {currentGroup?.notification ? (
        <>
          <div className="flex">
            <OIMAvatar
              src={notificationOpUser?.faceURL}
              text={notificationOpUser?.nickname || t("placeholder.administrator")}
            />
            <div className="ml-3">
              <div className="mb-1.5 max-w-[200px] truncate">
                {notificationOpUser?.nickname || t("placeholder.administrator")}
              </div>
              <div className="text-xs text-[var(--sub-text)]">
                {formatMessageTime(Number(currentGroup?.notificationUpdateTime))}
              </div>
            </div>
          </div>
          <div
            className="text-break mt-3 flex-1 overflow-y-auto"
            dangerouslySetInnerHTML={{
              __html: sanitizeHtml(formatBr(escapeHtml(currentGroup?.notification || ""))),
            }}
          ></div>
        </>
      ) : (
        <div className="flex h-full flex-col items-center justify-center">
          <img width={176} src={empty_announcement} alt="empty_announcement" />
          <div className="mt-9 text-xs text-[var(--sub-text)]">
            {t("placeholder.noGroupAnnouncement")}
          </div>
        </div>
      )}
    </>
  );
};
