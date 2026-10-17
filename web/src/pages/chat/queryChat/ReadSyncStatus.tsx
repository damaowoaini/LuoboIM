import type { ReactNode } from "react";
import type { ExMessageItem } from "@/store/message";
import type { ReadSyncResult } from "@/core/messageReadReceipt";
import { useMessagePrivacy } from "@/hooks/useMessagePrivacy";

/** The actual message renderer uses this boundary. A pending read time is a
 * reversible view hold, not an expired/deleted ledger entry. */
export function MessageReadBoundary({ message, conversationID, children }: {
  message: ExMessageItem; conversationID?: string; children: ReactNode;
}) {
  const privacy = useMessagePrivacy(message, conversationID);
  if (privacy.readTimePending) return <span role="status" className="rounded bg-slate-100 p-2 text-xs text-slate-500">读取时间未能确认，私密内容已暂时隐藏</span>;
  if (privacy.expired) return <span role="status" className="rounded bg-slate-100 p-2 text-xs text-slate-500">消息已过期或已移除</span>;
  return <>{children}</>;
}

export default function ReadSyncStatus({ result, pending, canRecover, busy, recover }: {
  result?: ReadSyncResult; pending: number; canRecover: boolean; busy: boolean; recover(): Promise<unknown>;
}) {
  if (!pending && result?.state !== "failed") return null;
  return <section aria-label="消息读取状态" role="status" className="flex items-center gap-2 px-5 py-2 text-xs text-[var(--sub-text)]">
    <span>{pending ? "读取时间未能确认，私密内容已暂时隐藏。重新获取只查询读取时间，不会再次标已读或发送消息。" : "读取状态未能确认，尚未自动重试。"}</span>
    {pending > 0 && <button type="button" aria-label="重新获取读取时间" disabled={!canRecover || busy}
      className="min-h-11 min-w-11 shrink-0 rounded px-2 text-[var(--primary)] focus-visible:outline focus-visible:outline-2 disabled:opacity-50"
      onClick={() => { void recover(); }}>{busy ? "正在获取" : "重新获取"}</button>}
  </section>;
}
