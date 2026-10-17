import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useUserStore } from "@/store";
import { sessionCoordinator } from "@/core/session/browser";
import type { Session } from "@/core/session/session";

type Authority = { owner: Session | undefined; generation: number; viewer: string; level: number; revision: number };
type Scope = { owner: Session | undefined; generation: number; revision: number; target: string; page: number };
const same = (a: Scope, b: Scope) => a.owner === b.owner && a.generation === b.generation &&
  a.revision === b.revision && a.target === b.target && a.page === b.page;

// This is a presentation/request gate. Server authorization remains authoritative.
export function useOwnedLoginRecords<T>(target: string,
  load: (target: string, page: number, current: () => boolean) => Promise<T>) {
  const authority = useRef<Authority | undefined>(undefined);
  const snapshot = useCallback(() => {
    const owner = sessionCoordinator.current, generation = sessionCoordinator.generation;
    const viewer = useUserStore.getState().selfInfo;
    const old = authority.current;
    if (!old || old.owner !== owner || old.generation !== generation ||
        old.viewer !== viewer.userID || old.level !== viewer.level) {
      authority.current = { owner, generation, viewer: viewer.userID, level: viewer.level,
        revision: (old?.revision ?? 0) + 1 };
    }
    return authority.current!.revision;
  }, []);
  const subscribe = useCallback((listener: () => void) => {
    const changed = () => { snapshot(); listener(); };
    const stopOwner = sessionCoordinator.onChange(changed);
    const stopViewer = useUserStore.subscribe(changed);
    return () => { stopOwner(); stopViewer(); };
  }, [snapshot]);
  const revision = useSyncExternalStore(subscribe, snapshot, snapshot);
  const self = useUserStore.getState().selfInfo;
  const owner = sessionCoordinator.current, generation = sessionCoordinator.generation;
  const allowed = Boolean(owner && self.userID === owner.userID && self.level === 100 && target);
  const [pagination, setPagination] = useState<Scope>({ owner, generation, revision, target, page: 1 });
  const page = pagination.owner === owner && pagination.generation === generation && pagination.revision === revision && pagination.target === target
    ? pagination.page : 1;
  const scope: Scope = { owner, generation, revision, target, page };
  const live = useRef(scope); live.current = scope;
  const [result, setResult] = useState<{ scope: Scope; data?: T; failed: boolean }>();

  useEffect(() => {
    let active = true;
    const current = () => {
      const viewer = useUserStore.getState().selfInfo;
      return active && allowed && same(scope, live.current) && sessionCoordinator.current === owner &&
        sessionCoordinator.generation === generation && snapshot() === revision && viewer.userID === owner?.userID && viewer.level === 100;
    };
    setResult(undefined);
    if (current()) {
      void load(target, page, current).then(data => {
        if (current()) setResult({ scope, data, failed: false });
      }).catch(() => {
        if (current()) setResult({ scope, failed: true });
      });
    }
    return () => { active = false; };
  }, [owner, generation, revision, target, page, allowed, load, snapshot]);

  // Hide a previous owner, target, or page synchronously, before effects run.
  const visible = allowed && result && same(result.scope, scope) ? result : undefined;
  return {
    allowed, page, data: visible?.data, failed: visible?.failed ?? false,
    loading: allowed && !visible,
    setPage: (next: number) => {
      const viewer = useUserStore.getState().selfInfo;
      if (allowed && same(scope, live.current) && owner === sessionCoordinator.current &&
          generation === sessionCoordinator.generation && snapshot() === revision && viewer.userID === owner?.userID && viewer.level === 100 &&
          Number.isSafeInteger(next) && next >= 1) setPagination({ ...scope, page: next });
    },
  };
}
