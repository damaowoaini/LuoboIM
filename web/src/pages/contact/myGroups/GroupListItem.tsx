import { GroupItem } from "open-im-sdk-wasm/lib/types/entity";

import OIMAvatar from "@/components/OIMAvatar";

const GroupListItem = ({
  source,
  showGroupCard,
}: {
  source: GroupItem;
  showGroupCard: (group: GroupItem) => void;
}) => {
  return (
    <div
      className="app-contact-row flex flex-row px-3.5 py-3 transition-colors hover:bg-[var(--primary-active)]"
      onClick={() => showGroupCard(source)}
    >
      <OIMAvatar src={source?.faceURL} isgroup />
      <div className="ml-3">
        <p className="text-base">{source.groupName}</p>
        <p className="text-xs text-[var(--sub-text)]">{source.memberCount}</p>
      </div>
    </div>
  );
};

export default GroupListItem;
