import { Button } from "antd";
import { ApplicationHandleResult } from "open-im-sdk-wasm";
import { FriendApplicationItem } from "open-im-sdk-wasm/lib/types/entity";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { Virtuoso } from "react-virtuoso";

import ApplicationItem, { friendApplicationIdentity } from "@/components/ApplicationItem";
import { sessionCoordinator } from "@/core/session/browser";
import { IMSDK } from "@/layout/MainContentWrap";
import { useUserStore } from "@/store";
import { useContactStore } from "@/store/contact";
import { feedbackToast } from "@/utils/common";
import { calcApplicationBadge } from "@/utils/imCommon";
import { setAccessedFriendApplication } from "@/utils/storage";

export const NewFriends = () => {
  const { t } = useTranslation();
  const currentUserID = useUserStore((state) => state.selfInfo.userID);
  const owner = sessionCoordinator.current;
  const generation = sessionCoordinator.generation;
  const mounted = useRef(false);
  const inFlight = useRef(new WeakMap<object, Set<string>>());
  const reading = useRef(new WeakMap<object, Set<string>>());
  const renderIssuer = owner?.issuer;
  const renderTenant = owner?.tenant.serverID;
  const renderLease = { owner, generation, actor: currentUserID };
  const latest = useRef(renderLease);
  latest.current = renderLease;
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const current = () => Boolean(mounted.current && owner && owner.userID === currentUserID &&
    latest.current === renderLease && sessionCoordinator.current === owner &&
    sessionCoordinator.generation === generation &&
    useUserStore.getState().selfInfo.userID === currentUserID);

  const recvFriendApplicationList = useContactStore(
    (state) => state.recvFriendApplicationList,
  );
  const sendFriendApplicationList = useContactStore(
    (state) => state.sendFriendApplicationList,
  );

  // Subscribe to the non-sensitive tickets for rendering, but resolve their
  // owner binding inside the store's private WeakMap.
  useContactStore((state) => state.recvFriendApplicationReadState);
  useContactStore((state) => state.sendFriendApplicationReadState);
  const phase = (direction: "recv" | "send") => owner && owner.userID === currentUserID
    ? useContactStore.getState().getFriendApplicationReadPhase(direction, { owner, generation }) : "idle";
  const recvPhase = phase("recv"), sendPhase = phase("send");
  const loading = recvPhase === "loading" || sendPhase === "loading";
  const failed = recvPhase === "error" || sendPhase === "error";
  const complete = recvPhase === "ready" && sendPhase === "ready";
  const friendApplicationList = sortArray(
    (recvPhase === "idle" ? [] : recvFriendApplicationList)
      .concat(sendPhase === "idle" ? [] : sendFriendApplicationList),
  );
  const readApplications = async () => {
    if (!owner || !current() || owner.issuer !== renderIssuer ||
        owner.tenant.serverID !== renderTenant) return;
    const readKey = JSON.stringify([generation, currentUserID, renderIssuer, renderTenant]);
    const pending = reading.current.get(owner) ?? new Set<string>();
    if (pending.has(readKey)) return;
    const store = useContactStore.getState();
    const expected = { owner, generation };
    const requests: Array<() => Promise<void>> = [];
    for (const direction of ["recv", "send"] as const) {
      const state = store.getFriendApplicationReadPhase(direction, expected);
      if (state === "idle" || state === "error") requests.push(() => direction === "recv"
        ? store.getRecvFriendApplicationListByReq(expected) : store.getSendFriendApplicationListByReq(expected));
    }
    if (!requests.length) return;
    reading.current.set(owner, pending);
    pending.add(readKey);
    try { await Promise.all(requests.map((request) => request())); }
    finally {
      pending.delete(readKey);
      if (pending.size === 0) reading.current.delete(owner);
    }
  };
  useEffect(() => {
    if (recvPhase === "idle" || sendPhase === "idle") void readApplications();
  }, [owner, generation, currentUserID]);

  useEffect(() => {
    if (!owner || !current()) return;
    const accessedFriendApplications = [...recvFriendApplicationList]
      .filter(
        (application) =>
          application.handleResult === ApplicationHandleResult.Unprocessed,
      )
      .map((application) => `${application.fromUserID}_${application.createTime}`);
    void setAccessedFriendApplication(accessedFriendApplications, { owner, generation })
      .then(() => { if (current()) return calcApplicationBadge({ owner, generation }); })
      .catch(() => undefined);
  }, [recvFriendApplicationList, owner, generation, currentUserID]);

  const exactIncoming = (application: FriendApplicationItem) => {
    if (!friendApplicationIdentity(application, currentUserID).canHandle) return false;
    const store = useContactStore.getState();
    // The wire operation accepts a peer ID, not a request timestamp. Multiple
    // cached rows for that pair cannot be safely disambiguated for the server.
    const matches = store.recvFriendApplicationList.concat(store.sendFriendApplicationList)
      .filter((row) => row.fromUserID === application.fromUserID && row.toUserID === application.toUserID);
    const row = matches[0];
    return matches.length === 1 && store.recvFriendApplicationList.includes(row) &&
      friendApplicationIdentity(row, currentUserID).canHandle &&
      row.createTime === application.createTime && row.reqMsg === application.reqMsg;
  };

  const handleApplication = async (application: FriendApplicationItem, agree: boolean) => {
    if (!owner || !current() || !exactIncoming(application)) return;
    // The SDK operates on this pair, not createTime/reqMsg. A refreshed row for
    // the same peer must share the still-pending operation's lock. Owner object
    // identity separates sessions without serializing credentials into keys.
    const key = JSON.stringify([generation, currentUserID,
      application.fromUserID, application.toUserID]);
    const pending = inFlight.current.get(owner) ?? new Set<string>();
    if (pending.has(key)) return;
    inFlight.current.set(owner, pending);
    pending.add(key);
    try {
      // No await between the exact current owner/row checks and SDK dispatch.
      if (!current() || !exactIncoming(application)) return;
      const params = { toUserID: application.fromUserID, handleMsg: "" };
      await (agree ? IMSDK.acceptFriendApplication(params) : IMSDK.refuseFriendApplication(params));
    } catch (error) {
      if (current()) feedbackToast({ error });
    } finally {
      pending.delete(key);
      if (pending.size === 0) inFlight.current.delete(owner);
    }
  };

  return (
    <div className="flex h-full w-full flex-col bg-white">
      <p className="m-5.5 text-base font-extrabold">{t("placeholder.newFriends")}</p>
      {(loading || (!failed && !complete)) && (
        <p role="status" aria-live="polite" className="mx-5.5 mb-3 text-sm text-[var(--sub-text)]">
          {t(loading ? "application.friendRequestsLoading" : "application.friendRequestsNotLoaded")}
        </p>
      )}
      {failed && (
        <div role="alert" className="mx-5.5 mb-3 rounded-md bg-red-50 p-3 text-sm">
          {recvPhase === "error" && <p>{t("application.receivedFriendRequestsFailed")}</p>}
          {sendPhase === "error" && <p>{t("application.sentFriendRequestsFailed")}</p>}
          <Button className="mt-2" disabled={loading} onClick={readApplications}>
            {t("application.retryFriendRequests")}
          </Button>
        </div>
      )}
      {!failed && !loading && !complete && (
        <Button className="mx-5.5 mb-3 self-start" onClick={readApplications}>
          {t("application.loadFriendRequests")}
        </Button>
      )}
      <div className="min-h-0 flex-1 pb-3">
        {complete && friendApplicationList.length === 0 ? (
          <div role="status" className="flex h-full flex-col items-center justify-center gap-2 p-6 text-center text-sm text-[var(--sub-text)]">
            <p>{t("application.noFriendRequests")}</p>
            <p>{t("application.noFriendRequestsHint")}</p>
          </div>
        ) : <Virtuoso
          className="h-full overflow-x-hidden"
          data={friendApplicationList}
          itemContent={(index, item) => {
            // Capture the rendered immutable row, not a later-mutated store
            // object or an arbitrary callback argument from an old portal.
            const snapshot = Object.freeze({ ...item });
            return <ApplicationItem
              key={JSON.stringify([currentUserID, generation, item.fromUserID,
                item.toUserID, item.createTime, index])}
              source={snapshot}
              currentUserID={currentUserID}
              canHandle={Boolean(owner && owner.userID === currentUserID && exactIncoming(snapshot))}
              onAccept={() => handleApplication(snapshot, true)}
              onReject={() => handleApplication(snapshot, false)}
            />;
          }}
        />}
      </div>
    </div>
  );
};

const sortArray = (list: FriendApplicationItem[]) => {
  list.sort((a, b) => {
    if (a.handleResult === 0 && b.handleResult === 0) {
      return b.createTime - a.createTime;
    } else if (a.handleResult === 0) {
      return -1;
    } else if (b.handleResult === 0) {
      return 1;
    }
    return 0;
  });
  return list;
};
