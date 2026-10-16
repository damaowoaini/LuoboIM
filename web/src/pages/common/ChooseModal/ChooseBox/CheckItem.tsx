import { CloseOutlined, RightOutlined } from "@ant-design/icons";
import { Checkbox } from "antd";
import clsx from "clsx";
import { t } from "i18next";
import { SessionType } from "open-im-sdk-wasm";
import {
  ConversationItem,
  FriendUserItem,
  GroupItem,
} from "open-im-sdk-wasm/lib/types/entity";
import { FC, memo } from "react";

import OIMAvatar from "@/components/OIMAvatar";

interface ICheckItemProps {
  data: CheckListItem;
  isChecked?: boolean;
  showCheck?: boolean;
  disabled?: boolean;
  itemClick?: (data: CheckListItem) => void;
  cancelClick?: (data: CheckListItem) => void;
}

export type CheckListItem = Partial<
  FriendUserItem & ConversationItem & GroupItem & { disabled?: boolean }
>;

const CheckItem: FC<ICheckItemProps> = (props) => {
  const { data, isChecked, showCheck, disabled, itemClick, cancelClick } = props;
  const showName = data.remark || data.nickname || data.groupName || data.showName;
  // Group members carry both IDs; the selectable identity is the user.
  const recipientID = data.userID || data.groupID;
  const isDisabled = disabled ?? data.disabled;
  return (
    <div
      className={clsx(
        "mx-2 flex min-w-0 items-center justify-between gap-2 rounded-md px-3.5 py-2.5 hover:bg-[var(--primary-active)]",
        { "cursor-pointer": showCheck },
      )}
      onClick={() => !isDisabled && itemClick?.(data)}
    >
      <div className="flex min-w-0 flex-1 items-center">
        {showCheck && (
          <Checkbox
            className="mr-3 shrink-0"
            checked={isChecked}
            disabled={isDisabled}
          />
        )}
        <OIMAvatar
          src={data.faceURL}
          text={showName || recipientID}
          isgroup={
            Boolean(data.groupName) ||
            data.conversationType === SessionType.WorkingGroup
          }
        />
        <div className="ml-3 min-w-0 flex-1">
          <div className="truncate" title={showName}>
            {showName}
          </div>
          {recipientID && (
            <div className="break-all text-xs leading-5 text-[var(--sub-text)]">
              {t(
                data.userID
                  ? "placeholder.recipientAccountID"
                  : "placeholder.recipientGroupID",
              )}
              : {recipientID}
            </div>
          )}
        </div>
      </div>
      {showCheck ? (
        <RightOutlined
          className="shrink-0 cursor-pointer text-[var(--sub-text)]"
          rev={undefined}
        />
      ) : (
        <CloseOutlined
          className="shrink-0 cursor-pointer text-[var(--sub-text)]"
          rev={undefined}
          onClick={(e) => {
            e.stopPropagation();
            cancelClick?.(data);
          }}
        />
      )}
    </div>
  );
};

export default memo(CheckItem);
