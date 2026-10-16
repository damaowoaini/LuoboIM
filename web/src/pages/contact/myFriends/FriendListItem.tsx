import { FriendUserItem } from "open-im-sdk-wasm/lib/types/entity";

import OIMAvatar from "@/components/OIMAvatar";

const FriendListItem = ({
  friend,
  showUserCard,
}: {
  friend: FriendUserItem;
  showUserCard: (userID: string) => void;
}) => {
  const displayName = friend.remark || friend.nickname;
  return (
    <div
      className="flex items-center rounded-md px-3.5 pb-3 pt-2.5 transition-colors hover:bg-[var(--primary-active)]"
      onClick={() => showUserCard(friend.userID)}
    >
      <OIMAvatar src={friend.faceURL} text={displayName} />
      <div className="ml-3 truncate text-sm">{displayName}</div>
    </div>
  );
};

export default FriendListItem;
