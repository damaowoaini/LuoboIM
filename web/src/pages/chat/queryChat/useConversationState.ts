import { useThrottleFn } from "ahooks";
import { GroupAtType, GroupStatus } from "open-im-sdk-wasm";
import { useEffect, useRef, useState, useSyncExternalStore } from "react";

import { captureMessageReadScope, markReadAndHydrate, pendingReadTimes, recoverReadTimes, type MessageReadScope, type ReadSyncResult } from "@/core/messageReadReceipt";

import { readBarrier, readIdentity } from "@/core/messageReadBarrier";
import { privacyVersion, subscribeMessagePrivacy } from "@/core/messagePrivacyRuntime";
import { sessionCoordinator } from "@/core/session/browser";
import { useCurrentMemberRole } from "@/hooks/useCurrentMemberRole";
import { IMSDK } from "@/layout/MainContentWrap";
import { useConversationStore, useMessageStore } from "@/store";

export default function useConversationState() {
  // Effect cleanup and throttled trailing calls can outlive Router teardown.
  // Never acquire a replacement account's SDK lease from an old mounted hook.
  const source = useRef({
    owner: sessionCoordinator.current,
    generation: sessionCoordinator.generation,
    actor: sessionCoordinator.current?.userID,
    issuer: sessionCoordinator.current?.issuer,
    tenant: sessionCoordinator.current?.tenant.serverID,
  }).current;
  const currentConversation = useConversationStore(
    (state) => state.currentConversation,
  );
  const currentGroupInfo = useConversationStore(
    (state) => state.currentGroupInfo,
  );
  const readScopeRef = useRef<MessageReadScope | undefined>(undefined);
  if (!readScopeRef.current?.current()) readScopeRef.current = captureMessageReadScope();
  const readScope = readScopeRef.current;
  useMessageStore(state => state.historyMessageList);
  const loadedIncomingKeys = (includeAcknowledged = false) => {
    if (!readScope?.single || !readScope.current()) return [];
    return (useMessageStore.getState().historyMessageList ?? []).flatMap(message => {
      const identity = readIdentity(readScope.owner, readScope.conversationID, message);
      return identity && identity.sendID === readScope.peer && identity.recvID === readScope.actor &&
        (includeAcknowledged || !readBarrier.acknowledged(identity)) && message.isRead !== true ?
        [JSON.stringify([identity.clientMsgID, identity.seq])] : [];
    }).sort();
  };
  const incomingReadKey = readScope?.single ? JSON.stringify(loadedIncomingKeys()) : "";
  useSyncExternalStore(subscribeMessagePrivacy, privacyVersion, privacyVersion);
  const readAttempt = useRef<{ scope: MessageReadScope; key: string; pending: boolean; seen?: Set<string> } | undefined>(undefined);
  const [readSync, setReadSync] = useState<{ scope: MessageReadScope; result: ReadSyncResult }>();
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const isMutedGroup = currentGroupInfo?.status === GroupStatus.Muted;

  const { isJoinGroup, isNomal, isAdmin, isOwner, currentIsMuted, currentMemberInGroup } =
    useCurrentMemberRole();
  const isCurrent = () => {
    const owner = sessionCoordinator.current;
    return Boolean(source.owner && source.actor && owner && owner === source.owner &&
      sessionCoordinator.generation === source.generation && owner.userID === source.actor &&
      owner.issuer === source.issuer && owner.tenant.serverID === source.tenant &&
      currentConversation?.conversationID &&
      useConversationStore.getState().currentConversation?.conversationID === currentConversation.conversationID);
  };

  useEffect(() => {
    checkConversationState();
    return () => {
      checkConversationState();
    };
  }, [
    currentConversation?.conversationID,
    currentConversation?.groupAtType,
    currentConversation?.unreadCount,
    incomingReadKey,
  ]);

  const { run: checkConversationState } = useThrottleFn(
    () => {
      if (!mounted.current || !currentConversation || !isCurrent()) return;

      // C2C admission is tied to loaded exact incoming IDs, not the unread
      // badge (the current-view conversation reducer may intentionally keep0).
      // Never issue an empty initial ACK before history supplies those IDs.
      const incoming = loadedIncomingKeys();
      if (readScope?.current() && (readScope.single ? incoming.length > 0 : currentConversation.unreadCount > 0)) {
        const batchKey = () => {
          // ACK publication precedes onAcknowledged. It must affect new
          // admission, not make the same loaded batch appear replaced.
          if (readScope.single) return JSON.stringify(loadedIncomingKeys(true));
          const latest = useConversationStore.getState().currentConversation;
          let latestID = "";
          try { const value = JSON.parse(latest?.latestMsg ?? "null");
            if (typeof value?.clientMsgID === "string") latestID = value.clientMsgID;
          } catch { /* No plaintext or replacement timestamp is retained. */ }
          return JSON.stringify([latestID, latest?.latestMsgSendTime, latest?.unreadCount,
            (useMessageStore.getState().historyMessageList ?? []).map(message => message.clientMsgID)]);
        };
        const key = batchKey(), previous = readAttempt.current;
        const seen = previous && previous.scope === readScope ? previous.seen ?? new Set<string>() : new Set<string>();
        const hasNewIncoming = incoming.some(id => !seen.has(id));
        if ((!readScope.single || hasNewIncoming) &&
            (!previous || previous.scope !== readScope || !previous.pending && (readScope.single || previous.key !== key))) {
          // Reserve before dispatch. Failed/unknown IDs remain attempted for
          // this view; badges, row flags/reorder or pagination cannot replay
          // that batch. A genuinely new incoming ID can form a new batch.
          if (readScope.single) incoming.forEach(id => seen.add(id));
          const attempt = { scope: readScope, key, pending: true, seen };
          readAttempt.current = attempt;
          const current = () => mounted.current && isCurrent() && readScope.current();
          const operationScope: MessageReadScope = { ...readScope, current };
          const latestAtDispatch = useConversationStore.getState().currentConversation?.latestMsg;
          void markReadAndHydrate(operationScope, IMSDK, () => {
            if (!current()) return;
            const latest = useConversationStore.getState().currentConversation;
            // A newly arrived message may not be covered by this read ACK.
            if (!latest || latest.latestMsg !== latestAtDispatch || batchKey() !== key) return;
            const cleared = { ...latest, unreadCount: 0 };
            useConversationStore.getState().updateCurrentConversation(cleared);
            if (current()) useConversationStore.getState().updateConversationList([cleared], "filter");
          }).then(result => {
            if (readAttempt.current === attempt) attempt.pending = false;
            if (!current()) return;
            setReadSync({ scope: readScope, result });
            // Only a genuinely different observed batch may start another read;
            // this is not an automatic retry of the failed/unknown original.
            const latest = useConversationStore.getState().currentConversation;
            if (latest && (readScope.single ? loadedIncomingKeys().some(id => !seen.has(id)) :
                latest.unreadCount > 0 && batchKey() !== key)) checkConversationState();
          });
        }
      }
      if (
        currentConversation.groupAtType !== GroupAtType.AtNormal &&
        currentConversation.groupAtType !== GroupAtType.AtGroupNotice
      ) {
        if (!isCurrent()) return;
        void IMSDK.resetConversationGroupAtType(currentConversation.conversationID).catch(() => undefined);
      }
    },
    { wait: 2000 },
  );

  const getIsCanSendMessage = () => {
    if (!isCurrent()) return false;
    if (currentConversation?.userID) {
      return true;
    }

    const groupID = currentConversation?.groupID;
    const current = useConversationStore.getState();
    if (current.currentGroupInfo !== currentGroupInfo ||
        current.currentMemberInGroup !== currentMemberInGroup) return false;
    if (!groupID || !isJoinGroup || !currentGroupInfo || !currentMemberInGroup ||
        currentGroupInfo.groupID !== groupID || currentMemberInGroup.groupID !== groupID ||
        !sessionCoordinator.current?.userID ||
        currentMemberInGroup.userID !== sessionCoordinator.current.userID ||
        !(isNomal || isAdmin || isOwner) ||
        ![GroupStatus.Nomal, GroupStatus.Baned, GroupStatus.Dismissed, GroupStatus.Muted]
          .includes(currentGroupInfo.status) ||
        currentGroupInfo.status === GroupStatus.Dismissed) {
      return false;
    }

    if (isMutedGroup && isNomal) {
      return false;
    }

    return !currentIsMuted;
  };

  const pending = pendingReadTimes(readScope);
  const recoverReadTime = async () => {
    if (!readScope?.current() || !mounted.current || !isCurrent()) return;
    const current = () => mounted.current && isCurrent() && readScope.current();
    const result = await recoverReadTimes({ ...readScope, current }, IMSDK);
    if (current()) setReadSync({ scope: readScope, result });
  };
  return { getIsCanSendMessage, currentIsMuted, isMutedGroup, currentConversation,
    readTimePending: pending.count, canRecoverReadTime: pending.canRecover, readRetryBusy: pending.busy, recoverReadTime,
    readSync: readSync && readSync.scope === readScope && readScope?.current() ? readSync.result : undefined };
}
