import { t } from "i18next";
import { AllowType, GroupStatus, GroupVerificationType } from "open-im-sdk-wasm";
import { GroupItem } from "open-im-sdk-wasm/lib/types/entity";
import { useCallback, useEffect, useRef } from "react";

import { sessionCoordinator } from "@/core/session/browser";
import type { ConversationSendScope } from "@/core/messageSendIntent";

import { modal } from "@/AntdGlobalComp";
import { IMSDK } from "@/layout/MainContentWrap";
import { useConversationStore, useUserStore } from "@/store";
import { feedbackToast } from "@/utils/common";

export type PermissionField = "applyMemberFriend" | "lookMemberInfo";

export function useGroupSettings({ closeOverlay, isActive }: { closeOverlay: () => void; isActive?: () => boolean }) {
  const currentGroupInfo = useConversationStore((state) => state.currentGroupInfo);
  const selfUserID = useUserStore((state) => state.selfInfo.userID);
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const scopeRef = useRef<ConversationSendScope | undefined>(undefined);
  if (!scopeRef.current?.current()) scopeRef.current = useConversationStore.getState().captureConversationSendScope();
  const scope = scopeRef.current;
  const actor = sessionCoordinator.current?.userID;
  const busy = useRef(new Set<string>());
  const viewCurrent = () => Boolean(mounted.current && (!isActive || isActive()) && scope?.current() && actor &&
    useUserStore.getState().selfInfo.userID === actor);
  const captureAction = (kind: "mute" | "quit" | "dismiss" | "permission") => {
    const groupID = currentGroupInfo?.groupID, status = currentGroupInfo?.status;
    if (!groupID || scope?.target.kind !== "group" || scope.target.id !== groupID) return;
    const current = () => {
      const state = useConversationStore.getState(), self = state.currentMemberInGroup, group = state.currentGroupInfo;
      if (!viewCurrent() || group?.groupID !== groupID || ![0, 1, 3].includes(group.status) ||
          self?.groupID !== groupID || self.userID !== actor || ![20, 60, 100].includes(self.roleLevel)) return false;
      if (kind === "quit") return self.roleLevel !== 100;
      if (kind === "dismiss") return self.roleLevel === 100;
      if (kind === "permission") return self.roleLevel === 60 || self.roleLevel === 100;
      return (self.roleLevel === 60 || self.roleLevel === 100) && group.status === status;
    };
    if (!current()) return;
    return { groupID, status, current, viewCurrent };
  };
  const perform = async (intent: NonNullable<ReturnType<typeof captureAction>>, operation: () => Promise<unknown>) => {
    if (!intent.current() || busy.current.has(intent.groupID)) return false;
    busy.current.add(intent.groupID);
    try {
      if (!intent.current()) return false;
      await operation(); return intent.viewCurrent();
    } catch (error) {
      if (intent.viewCurrent()) feedbackToast({ error }); return false;
    } finally { busy.current.delete(intent.groupID); }
  };


  const updateGroupInfo = useCallback(
    async (value: Partial<GroupItem>) => {
      if (!currentGroupInfo) return;
      try {
        await IMSDK.setGroupInfo({
          ...value,
          groupID: currentGroupInfo.groupID,
        });
      } catch (error) {
        feedbackToast({ error, msg: t("toast.updateGroupInfoFailed") });
      }
    },
    [currentGroupInfo?.groupID],
  );

  const updateNickNameInGroup = useCallback(
    async (value: string) => {
      if (!currentGroupInfo) return;
      try {
        await IMSDK.setGroupMemberInfo({
          groupID: currentGroupInfo.groupID,
          userID: selfUserID,
          nickname: value,
        });
      } catch (error) {
        feedbackToast({ error });
      }
    },
    [selfUserID, currentGroupInfo?.groupID],
  );

  const updateGroupVerification = useCallback(
    async (verification: GroupVerificationType) => {
      if (!currentGroupInfo) return;

      try {
        await IMSDK.setGroupInfo({
          groupID: currentGroupInfo?.groupID,
          needVerification: verification,
        });
      } catch (error) {
        feedbackToast({ error, msg: t("toast.updateGroupVerificationFailed") });
      }
    },
    [currentGroupInfo?.groupID],
  );

  const updateGroupMuteAll = useCallback(async () => {
    const intent = captureAction("mute"); if (!intent) return;
    const currentIsMute = intent.status === GroupStatus.Muted;
    const execFunc = () => perform(intent, () => IMSDK.changeGroupMute({ groupID: intent.groupID, isMute: !currentIsMute }));
    if (!currentIsMute) {
      modal.confirm({
        title: t("placeholder.allMuted"),
        content: (
          <div className="flex items-baseline">
            <div>{t("toast.confirmAllMuted")}</div>
            <span className="text-xs text-[var(--sub-text)]">
              {t("placeholder.onlyManageCanSend")}
            </span>
          </div>
        ),
        onOk: execFunc,
      });
    } else {
      await execFunc();
    }
  }, [scope, actor, isActive, currentGroupInfo?.status, currentGroupInfo?.groupID]);

  const updateGroupMemberPermission = useCallback(
    async (rule: AllowType, field: PermissionField) => {
      if ((rule !== AllowType.Allowed && rule !== AllowType.NotAllowed) ||
          (field !== "applyMemberFriend" && field !== "lookMemberInfo")) return;
      const intent = captureAction("permission"); if (!intent) return;
      await perform(intent, () => IMSDK.setGroupInfo({ groupID: intent.groupID, [field]: rule }));
    },
    [scope, actor, isActive, currentGroupInfo?.groupID],
  );

  const tryDismissGroup = () => {
    const intent = captureAction("dismiss"); if (!intent) return;

    modal.confirm({
      title: t("placeholder.disbandGroup"),
      content: (
        <div className="flex items-baseline">
          <div>{t("toast.confirmDisbandGroup")}</div>
          <span className="text-xs text-[var(--sub-text)]">
            {t("placeholder.disbandGroupToast")}
          </span>
        </div>
      ),
      onOk: async () => {
        const completed = await perform(intent, () => IMSDK.dismissGroup(intent.groupID));
        if (completed && intent.viewCurrent()) closeOverlay();
      },
    });
  };

  const tryQuitGroup = () => {
    const intent = captureAction("quit"); if (!intent) return;

    modal.confirm({
      title: t("placeholder.exitGroup"),
      content: (
        <div className="flex items-baseline">
          <div>{t("toast.confirmExitGroup")}</div>
          <span className="text-xs text-[var(--sub-text)]">
            {t("placeholder.exitGroupToast")}
          </span>
        </div>
      ),
      onOk: async () => {
        const completed = await perform(intent, () => IMSDK.quitGroup(intent.groupID));
        if (completed && intent.viewCurrent()) closeOverlay();
      },
    });
  };

  return {
    currentGroupInfo,
    updateGroupInfo,
    updateGroupMuteAll,
    updateNickNameInGroup,
    updateGroupVerification,
    updateGroupMemberPermission,
    tryQuitGroup,
    tryDismissGroup,
  };
}
