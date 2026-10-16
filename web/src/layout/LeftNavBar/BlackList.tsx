import { CloseOutlined } from "@ant-design/icons";
import { Button, Empty, Modal, Spin } from "antd";
import { t } from "i18next";
import { BlackUserItem } from "open-im-sdk-wasm/lib/types/entity";
import { forwardRef, ForwardRefRenderFunction, memo, useEffect, useMemo, useRef, useState } from "react";

import OIMAvatar from "@/components/OIMAvatar";
import { sessionCoordinator } from "@/core/session/browser";
import { useUserStore } from "@/store/user";
import { useContactStore } from "@/store/contact";
import { feedbackToast } from "@/utils/common";

import { OverlayVisibleHandle, useOverlayVisible } from "../../hooks/useOverlayVisible";
import { IMSDK } from "../MainContentWrap";

const BlackList: ForwardRefRenderFunction<OverlayVisibleHandle, unknown> = (_, ref) => {
  const { isOverlayOpen, closeOverlay } = useOverlayVisible(ref);

  return (
    <Modal
      title={null}
      footer={null}
      closable={false}
      open={isOverlayOpen}
      onCancel={closeOverlay}
      centered
      destroyOnClose
      styles={{
        mask: {
          opacity: 0,
          transition: "none",
        },
      }}
      width={420}
      className="no-padding-modal"
      maskTransitionName=""
    >
      <BlackListContent closeOverlay={closeOverlay} />
    </Modal>
  );
};

export default memo(forwardRef(BlackList));

const BlackItem = ({
  black,
  removeBlack,
}: {
  black: BlackUserItem;
  removeBlack: (black: BlackUserItem) => Promise<void>;
}) => {
  const [loading, setLoading] = useState(false);
  const busy = useRef(false);
  const mounted = useRef(false);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);

  const tryRemove = async () => {
    if (busy.current) return;
    busy.current = true;
    setLoading(true);
    try { await removeBlack(black); }
    finally {
      busy.current = false;
      if (mounted.current) setLoading(false);
    }
  };

  return (
    <div className="flex items-center justify-between px-5 py-2.5">
      <div className="flex items-center">
        <OIMAvatar src={black.faceURL} text={black.nickname} />
        <div className="ml-3">{black.nickname}</div>
      </div>
      <Button type="primary" ghost loading={loading} onClick={tryRemove}>
        {t("placeholder.remove")}
      </Button>
    </div>
  );
};

export const BlackListContent = ({ closeOverlay }: { closeOverlay?: () => void }) => {
  const blackList = useContactStore((state) => state.blackList);
  useContactStore((state) => state.blackListReadState);
  const actor = useUserStore((state) => state.selfInfo.userID);
  const owner = sessionCoordinator.current;
  const generation = sessionCoordinator.generation;
  const issuer = owner?.issuer, tenant = owner?.tenant.serverID;
  const mounted = useRef(false);
  const reading = useRef(new WeakMap<object, Map<number, symbol>>());
  const removing = useRef(new WeakMap<object, Set<string>>());
  // A normal list/status render must not invalidate an admitted same-owner
  // operation. Only the actual owner scope creates a different render lease.
  const lease = useMemo(() => ({ owner, generation, actor }),
    [owner, generation, actor, issuer, tenant]);
  const latest = useRef(lease);
  latest.current = lease;
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const current = () => Boolean(mounted.current && owner && owner.userID === actor &&
    latest.current === lease && sessionCoordinator.current === owner &&
    sessionCoordinator.generation === generation && owner.issuer === issuer &&
    owner.tenant.serverID === tenant && useUserStore.getState().selfInfo.userID === actor);
  const phase = owner && owner.userID === actor
    ? useContactStore.getState().getBlackListReadPhase({ owner, generation }) : "idle";
  const rows = phase === "idle" ? [] : blackList;

  const read = async () => {
    if (!owner || !current()) return;
    const store = useContactStore.getState();
    const state = store.getBlackListReadPhase({ owner, generation });
    if (state !== "idle" && state !== "error") return;
    const pending = reading.current.get(owner) ?? new Map<number, symbol>();
    // Clearing the public ticket invalidates an old read without waiting for
    // its native response. A still-current retry remains mutually exclusive.
    if (pending.has(generation) && store.blackListReadState) return;
    const ticket = Symbol();
    reading.current.set(owner, pending); pending.set(generation, ticket);
    try { await store.getBlackListByReq({ owner, generation }); }
    finally {
      if (pending.get(generation) === ticket) pending.delete(generation);
      if (!pending.size) reading.current.delete(owner);
    }
  };
  useEffect(() => { if (phase === "idle") void read(); }, [owner, generation, actor, phase]);

  const removeBlack = async (black: BlackUserItem) => {
    if (!owner || !current()) return;
    const store = useContactStore.getState();
    if (store.getBlackListReadPhase({ owner, generation }) === "idle") return;
    const matches = store.blackList.filter((row) => row.userID === black.userID);
    if (matches.length !== 1 || matches[0] !== black) return;
    const key = JSON.stringify([generation, actor, black.userID]);
    const pending = removing.current.get(owner) ?? new Set<string>();
    if (pending.has(key)) return;
    removing.current.set(owner, pending); pending.add(key);
    try {
      // No await may retarget a captured old row to the current account.
      if (!current()) return;
      await IMSDK.removeBlack(black.userID);
    } catch (error) {
      if (current()) feedbackToast({ error });
    } finally {
      pending.delete(key);
      if (!pending.size) removing.current.delete(owner);
    }
  };

  return (
    <div className="app-settings-panel flex h-[468px] flex-col bg-[var(--chat-bubble)]">
      <div className="app-settings-heading flex items-center justify-between p-5">
        <span className="text-base font-medium">{t("placeholder.blackList")}</span>
        <CloseOutlined
          className="app-no-drag cursor-pointer text-[#8e9aaf]"
          rev={undefined}
          onClick={closeOverlay}
        />
      </div>
      {(phase === "idle" || phase === "loading") && (
        <div role="status" aria-label={t("placeholder.blackList")} aria-busy="true"
          className="flex justify-center p-4"><Spin /></div>
      )}
      {phase === "error" && (
        <div role="alert" className="mx-5 mb-3 rounded-md bg-red-50 p-3 text-sm">
          <p>{t("toast.getBlackListFailed")}</p>
          <Button onClick={read}>{t("retry")}</Button>
        </div>
      )}
      <div className="flex-1 overflow-y-auto">
        {rows.length > 0 ? (
          rows.map((black) => (
            <BlackItem black={black} key={black.userID} removeBlack={removeBlack} />
          ))
        ) : phase === "ready" ? (
          <Empty className="flex h-full flex-col items-center justify-center" />
        ) : null}
      </div>
    </div>
  );
};
