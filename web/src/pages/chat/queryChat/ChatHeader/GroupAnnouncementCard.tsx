import { t } from "i18next";
import type { GroupItem, GroupMemberItem } from "open-im-sdk-wasm/lib/types/entity";
import { useEffect, useRef, useState } from "react";

import speaker from "@/assets/images/chatHeader/speaker.png";
import OIMAvatar from "@/components/OIMAvatar";
import { announcementDeadline, announcementSnapshot, groupAnnouncementAcknowledgements,
  type AnnouncementSnapshot } from "@/core/groupAnnouncementAck";
import { sessionCoordinator } from "@/core/session/browser";
import { IMSDK } from "@/layout/MainContentWrap";
import { useConversationStore } from "@/store";
import styles from "./chat-header.module.scss";

const signature = (group?: GroupItem) => JSON.stringify([group?.groupID, group?.status,
  group?.notificationUpdateTime, group?.notificationUserID, group?.notification]);
const memberSignature = (member?: GroupMemberItem) => JSON.stringify([member?.groupID, member?.userID, member?.roleLevel]);
const covered = () => [...document.querySelectorAll<HTMLElement>('[role="dialog"], [aria-modal="true"], .ant-drawer-open')].some(element => {
  for (let node: HTMLElement | null = element; node; node = node.parentElement) {
    const style = getComputedStyle(node);
    if (node.hidden || style.display === "none" || style.visibility === "hidden") return false;
  }
  return true;
});
type Selection = { snapshot: AnnouncementSnapshot; current: () => boolean; viewCurrent: () => boolean;
  confirm: () => Promise<void>; open: () => void };

const GroupAnnouncementCard = ({ currentGroupInfo, conversationID, openGroupAnnouncementDrawer }: {
  currentGroupInfo?: GroupItem; conversationID?: string; openGroupAnnouncementDrawer: () => void;
}) => {
  const conversation = useConversationStore(state => state.currentConversation);
  const member = useConversationStore(state => state.currentMemberInGroup);
  const [reload, setReload] = useState(0), [snapshot, setSnapshot] = useState<AnnouncementSnapshot>();
  const [author, setAuthor] = useState<GroupMemberItem>(), [failure, setFailure] = useState("");
  const [loading, setLoading] = useState(true), [busy, setBusy] = useState(false), [dismissed, setDismissed] = useState(false);
  const selection = useRef<Selection | undefined>(undefined), turn = useRef(0), inFlight = useRef(false);
  const groupVersion = signature(currentGroupInfo), currentMember = memberSignature(member);

  useEffect(() => {
    let alive = true, active = false, request = 0, expiryTimer: ReturnType<typeof setTimeout> | undefined;
    const retire = () => { clearTimeout(expiryTimer); ++turn.current; ++request; selection.current = undefined; inFlight.current = false;
      if (alive) { setSnapshot(undefined); setAuthor(undefined); setBusy(false); } };
    const scope = useConversationStore.getState().captureConversationViewScope();
    const owner = sessionCoordinator.current, generation = sessionCoordinator.generation;
    const actor = owner?.userID, issuer = owner?.issuer, tenantID = owner?.tenant.serverID;
    const groupID = scope?.groupID;
    const account = Object.freeze({ issuer: issuer ?? "", tenantID: tenantID ?? "", userID: actor ?? "" });
    const base = () => alive && !!owner && sessionCoordinator.current === owner &&
      sessionCoordinator.generation === generation && owner.userID === actor && owner.issuer === issuer &&
      owner.tenant.serverID === tenantID && !!scope?.current() && scope.conversationID === conversationID &&
      !!groupID && currentGroupInfo?.groupID === groupID &&
      signature(useConversationStore.getState().currentGroupInfo) === groupVersion &&
      memberSignature(useConversationStore.getState().currentMemberInGroup) === currentMember;
    const available = () => document.visibilityState !== "hidden" && document.hasFocus() && !covered();
    const fresh = async (current: () => boolean) => {
      if (!current() || !groupID || !actor) throw new Error("公告视图已变化");
      const groups = await announcementDeadline(IMSDK.getSpecifiedGroupsInfo([groupID]));
      if (!current()) throw new Error("公告视图已变化");
      const members = await announcementDeadline(IMSDK.getSpecifiedGroupMembersInfo({ groupID, userIDList: [actor] }));
      if (!current() || !Array.isArray(groups.data) || groups.data.length !== 1 ||
          !Array.isArray(members.data) || members.data.length !== 1) throw new Error("公告或本人群身份无法核验");
      const value = await announcementSnapshot(groups.data[0], members.data[0], groupID, actor);
      if (!current()) throw new Error("公告视图已变化");
      return value;
    };
    const load = async () => {
      retire(); const epoch = turn.current, read = ++request;
      const current = () => base() && active && available() && epoch === turn.current && read === request;
      if (!current()) { setLoading(false); return; }
      setLoading(true); setFailure(""); setDismissed(false);
      try {
        const value = await fresh(current);
        const acknowledged = await groupAnnouncementAcknowledgements.read(account, value, current);
        if (!current()) return;
        const expires = Date.now() + 30000;
        const selected = (): boolean => current() && Date.now() < expires && selection.current === entry;
        const entry: Selection = { snapshot: value, current: selected, viewCurrent: current,
          open: () => { if (selected() && !inFlight.current) { retire(); openGroupAnnouncementDrawer(); } },
          confirm: async () => {
            if (!selected() || inFlight.current) return;
            inFlight.current = true; setBusy(true); setFailure("");
            try {
              const latest = await fresh(selected);
              if (!selected()) return;
              if (latest.version !== value.version) { setFailure("群公告已更新，请重新读取当前版本"); setReload(n => n + 1); return; }
              await groupAnnouncementAcknowledgements.confirm(account, value, selected);
              if (selected()) { clearTimeout(expiryTimer); setDismissed(true); }
            } catch {
              if (selected()) setFailure("本机已读未确认，请重试；不会清除消息中的 @ 提醒");
            } finally { if (current()) { inFlight.current = false; setBusy(false); } }
          } };
        selection.current = entry; setSnapshot(value); setDismissed(acknowledged || !value.text.trim()); setLoading(false);
        if (!acknowledged && value.text.trim()) expiryTimer = setTimeout(() => { if (current()) setReload(n => n + 1); }, 30000);
        if (!acknowledged && value.publisherID) {
          void (async () => {
            try {
              const { data } = await announcementDeadline(IMSDK.getSpecifiedGroupMembersInfo({ groupID: value.groupID, userIDList: [value.publisherID] }));
              if (selected() && Array.isArray(data) && data.length === 1 && data[0].groupID === value.groupID &&
                  data[0].userID === value.publisherID && typeof data[0].nickname === "string" && data[0].nickname.length <= 128) setAuthor(data[0]);
            } catch { /* A missing/late publisher keeps the generic label, without a global toast. */ }
          })();
        }
      } catch { if (current()) { setLoading(false); setFailure("群公告读取失败，请重试"); } }
    };
    const reconcile = () => {
      const next = base() && available();
      if (!next) { if (active) { active = false; retire(); } return; }
      if (!active) { active = true; void load(); }
    };
    const blur = () => { active = false; retire(); };
    window.addEventListener("blur", blur); window.addEventListener("focus", reconcile);
    document.addEventListener("visibilitychange", reconcile);
    const overlayNode = (node: Node) => node instanceof Element && (node.matches('[role="dialog"], [aria-modal="true"], .ant-drawer-open') ||
      !!node.querySelector('[role="dialog"], [aria-modal="true"], .ant-drawer-open'));
    const observer = new MutationObserver(records => {
      // Added-and-removed dialogs in one browser turn still retire old callbacks.
      if (records.some(record => [...record.addedNodes].some(overlayNode) ||
          record.type === "attributes" && record.target instanceof Element &&
          (record.target.matches('[role="dialog"], [aria-modal="true"], .ant-drawer-open') ||
            record.attributeName === "class" && record.oldValue?.includes("ant-drawer-open")))) { active = false; retire(); }
      reconcile();
    });
    observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeOldValue: true,
      attributeFilter: ["class", "style", "hidden", "aria-modal", "role"] });
    const detach = sessionCoordinator.onChange(() => { active = false; retire(); });
    reconcile();
    return () => { alive = false; retire(); detach(); observer.disconnect();
      window.removeEventListener("blur", blur); window.removeEventListener("focus", reconcile); document.removeEventListener("visibilitychange", reconcile); };
  }, [conversation, conversationID, groupVersion, currentMember, reload, openGroupAnnouncementDrawer]);

  const entry = selection.current, valid = entry?.current() && entry.snapshot === snapshot;
  if (dismissed && entry?.viewCurrent()) return null;
  return <section className={styles["notice-card"]} aria-label="群公告本机提醒"
    onDoubleClick={() => { if (valid) entry?.open(); }}>
    <div className="mb-2 flex items-center gap-2 text-[var(--primary)]"><img width={20} src={speaker} alt="" />{t("placeholder.groupAnnouncement")}</div>
    {valid && snapshot && <><div className="mb-3 line-clamp-2 whitespace-pre-wrap leading-relaxed">{snapshot.text}</div>
      <button type="button" aria-label="查看公告发布者" style={{ minHeight: 44, minWidth: 44 }}
        className="mb-2 flex items-center gap-2 text-xs text-[var(--sub-text)]" disabled={!author || busy}
        onClick={() => { if (entry?.current() && author?.groupID === snapshot.groupID && author.userID === snapshot.publisherID)
          window.userClick(author.userID, author.groupID); }}><OIMAvatar size={20}
          text={author?.nickname || t("placeholder.administrator")} />{author?.nickname || t("placeholder.administrator")}</button></>}
    {loading && <p role="status">正在核验当前群公告…</p>}
    {failure && <p role="alert" className="my-2 leading-relaxed">{failure}</p>}
    <div className="flex flex-wrap gap-2">
      <button type="button" className="rounded px-3 text-[var(--primary)]" style={{ minHeight: 44 }} disabled={!valid || busy} onClick={() => entry?.open()}>查看公告</button>
      <button type="button" className="rounded bg-[var(--primary)] px-3 text-white" style={{ minHeight: 44 }} disabled={!valid || busy} onClick={() => { void entry?.confirm(); }}>在本机标为已读</button>
      <button type="button" className="rounded px-3" style={{ minHeight: 44 }} disabled={busy} onClick={() => { if (document.hasFocus() && document.visibilityState !== "hidden" && !covered()) setReload(n => n + 1); }}>重新读取</button>
    </div>
    <p className="mt-2 text-xs leading-relaxed text-[var(--sub-text)]">仅关闭此版本的本机提醒，不代表其他设备已读，也不清除消息中的 @ 提醒。</p>
  </section>;
};
export default GroupAnnouncementCard;
