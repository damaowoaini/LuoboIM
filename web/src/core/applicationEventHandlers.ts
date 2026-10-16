import type { FriendApplicationItem, GroupApplicationItem, WSEvent } from "open-im-sdk-wasm/lib/types/entity";
import type { SessionCoordinator } from "@/core/session/session";
import type { ContactApplicationOwner } from "@/store/type";

type Dependencies = {
  coordinator: Pick<SessionCoordinator, "current" | "generation">;
  actor(): unknown;
  receivedFriend(data: FriendApplicationItem, expected: ContactApplicationOwner): Promise<void>;
  sentFriend(data: FriendApplicationItem): void;
  receivedGroup(data: GroupApplicationItem, expected: ContactApplicationOwner): Promise<void>;
  sentGroup(data: GroupApplicationItem): void;
  notifyFriend(data: FriendApplicationItem, current: () => boolean): void;
  notifyGroup(data: GroupApplicationItem, current: () => boolean): void;
};
const exactID = (value: unknown): value is string =>
  typeof value === "string" && value.length > 0 && value.trim() === value;

// Created once for the actual hook registration. An old listener must never
// obtain a fresh account merely because its callback was invoked late.
export function createApplicationEventHandlers(deps: Dependencies) {
  const owner = deps.coordinator.current;
  const generation = deps.coordinator.generation;
  const actor = owner?.userID;
  const issuer = owner?.issuer;
  const tenant = owner?.tenant.serverID;
  const expected = owner ? { owner, generation } : undefined;
  let active = false;
  const current = () => {
    if (!active || !owner || !exactID(actor) || deps.coordinator.current !== owner ||
      deps.coordinator.generation !== generation || owner.userID !== actor ||
      owner.issuer !== issuer || owner.tenant.serverID !== tenant) return false;
    const uiActor = deps.actor();
    return uiActor === actor || uiActor === "" || uiActor === undefined;
  };
  return {
    activate: () => { active = true; },
    dispose: () => { active = false; },
    friend: ({ data }: Pick<WSEvent<FriendApplicationItem>, "data">) => {
      if (!current() || !expected || !data || !exactID(data.fromUserID) ||
        !exactID(data.toUserID) || data.fromUserID === data.toUserID) return;
      if (data.toUserID === actor) {
        void deps.receivedFriend(data, expected);
        if (data.handleResult === 0 && current()) deps.notifyFriend(data, current);
      } else if (data.fromUserID === actor) {
        deps.sentFriend(data);
      }
    },
    group: ({ data }: Pick<WSEvent<GroupApplicationItem>, "data">) => {
      if (!current() || !expected || !data || !exactID(data.userID) || !exactID(data.groupID)) return;
      if (data.userID === actor) {
        deps.sentGroup(data);
      } else {
        // Preserve the SDK's authenticated admin-recipient event contract;
        // this does not infer a new group role from the applicant payload.
        void deps.receivedGroup(data, expected);
        if (data.handleResult === 0 && current()) deps.notifyGroup(data, current);
      }
    },
  };
}
