import { GroupMemberRole } from "open-im-sdk-wasm";
import { useEffect, useState } from "react";

import { useConversationStore } from "@/store";

export function useCurrentMemberRole() {
  const currentMemberInGroup = useConversationStore(
    (state) => state.currentMemberInGroup,
  );
  const [, refreshAtExpiry] = useState(0);
  const muteEndTime = currentMemberInGroup?.muteEndTime ?? 0;
  useEffect(() => {
    // The current server and SDK return Unix milliseconds, not seconds.
    // Recheck after long/clamped waits or clock rollback; this never sends an
    // unmute command and cannot change authoritative membership or policy.
    if (!Number.isFinite(muteEndTime) || muteEndTime <= Date.now()) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let disposed = false;
    const check = () => {
      if (disposed) return;
      const remaining = muteEndTime - Date.now();
      if (remaining <= 0) {
        refreshAtExpiry((value) => value + 1);
        return;
      }
      timer = setTimeout(check, Math.min(remaining, 2147483647));
    };
    check();
    return () => { disposed = true; clearTimeout(timer); };
  }, [currentMemberInGroup?.groupID, currentMemberInGroup?.userID, muteEndTime]);

  const isOwner = currentMemberInGroup?.roleLevel === GroupMemberRole.Owner;
  const isAdmin = currentMemberInGroup?.roleLevel === GroupMemberRole.Admin;
  const isNomal = currentMemberInGroup?.roleLevel === GroupMemberRole.Nomal;
  const isJoinGroup = Boolean(currentMemberInGroup?.groupID);
  const currentRolevel = currentMemberInGroup?.roleLevel ?? 0;
  const currentIsMuted = muteEndTime > Date.now();

  return {
    isOwner,
    isAdmin,
    isNomal,
    isJoinGroup,
    currentRolevel,
    currentIsMuted,
    currentMemberInGroup,
  };
}
