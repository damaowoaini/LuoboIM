import { useRef, useSyncExternalStore } from "react";
import { captureMessageAccess, messageOwner, messagePrivacyState, privacyVersion, subscribeMessagePrivacy } from "@/core/messagePrivacyRuntime";

/** Timers only refresh presentation. They never call destructive native APIs. */
export function useMessagePrivacy(message: unknown, conversationID = "") {
  const access = useMessageAccess(conversationID);
  const owner = access.owner;
  useSyncExternalStore(subscribeMessagePrivacy, privacyVersion, privacyVersion);
  const state = messagePrivacyState(message, conversationID, owner);
  return access.current() ? state : {...state,expired:true,allowsRetention:false};
}
export function useMessagePrivacyRevision() {
  return useSyncExternalStore(subscribeMessagePrivacy, privacyVersion, privacyVersion);
}

/** Freeze both account and history revision at the actual component/portal lifetime. */
export function useMessageAccess(conversationID = "") {
  return useRef(captureMessageAccess(conversationID, messageOwner())).current;
}
