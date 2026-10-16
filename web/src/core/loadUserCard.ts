import type { BusinessUserInfo } from "@/api/login";
import type { FriendUserItem, FullUserItemWithCache, GroupMemberItem } from "open-im-sdk-wasm/lib/types/entity";

export type HydratedUserCard = Partial<BusinessUserInfo & FriendUserItem>;
interface CardSource {
  sdk(): Promise<FullUserItemWithCache[]>;
  business(): Promise<BusinessUserInfo[]>;
  current(): boolean;
}
export class UserCardUnavailable extends Error {
  constructor() { super("User card identity or policy unavailable"); }
}
// Both authorities must answer for the exact requested user. Cached display data
// is never evidence of current relationship or business privacy permission.
export async function loadUserCard(target: string, groupID: string | undefined, source: CardSource): Promise<{card: HydratedUserCard; groupMember?: GroupMemberItem}> {
  const current = () => { if (!source.current()) throw new UserCardUnavailable(); };
  current();
  if (!target) throw new UserCardUnavailable();
  const rows = await source.sdk();
  current();
  if (!Array.isArray(rows) || rows.length !== 1) throw new UserCardUnavailable();
  const row = rows[0];
  const identity = row?.friendInfo ?? row?.publicInfo;
  if (!identity || identity.userID !== target) throw new UserCardUnavailable();
  if (row.friendInfo && (row.friendInfo.userID !== target || (row.friendInfo.remark !== undefined && typeof row.friendInfo.remark !== "string"))) throw new UserCardUnavailable();
  if (row.publicInfo && row.publicInfo.userID !== target) throw new UserCardUnavailable();
  const member = row.groupMemberInfo ?? undefined;
  if (member && (member.userID !== target || !groupID || member.groupID !== groupID)) throw new UserCardUnavailable();
  const users = await source.business();
  current();
  const business = exactUserCardPolicy(target, users);
  return {card: {...identity, ...business, userID: target, remark: row.friendInfo ? row.friendInfo.remark ?? "" : undefined}, groupMember: member};
}

// Shared with the final application dispatch: a previous card is not a current
// privacy decision, and missing/multiple/substituted policies are never allow.
export function exactUserCardPolicy(target: string, users: BusinessUserInfo[]): BusinessUserInfo {
  if (!target || !Array.isArray(users) || users.length !== 1 || users[0]?.userID !== target ||
      (users[0].allowAddFriend !== 1 && users[0].allowAddFriend !== 2)) throw new UserCardUnavailable();
  return users[0];
}
