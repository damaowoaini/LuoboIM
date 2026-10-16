import { Drawer } from "antd";
import { t } from "i18next";
import { forwardRef, ForwardRefRenderFunction, memo, useCallback, useImperativeHandle, useRef, useState } from "react";

import { OverlayVisibleHandle, useOverlayVisible } from "@/hooks/useOverlayVisible";

import GroupMemberList, { GroupMemberListHandle } from "./GroupMemberList";
import GroupMemberListHeader from "./GroupMemberListHeader";
import GroupSettings from "./GroupSettings";

// export interface GroupSettingProps {}

const GroupSetting: ForwardRefRenderFunction<OverlayVisibleHandle, unknown> = (
  _,
  ref,
) => {
  const memberListRef = useRef<GroupMemberListHandle>(null);
  const [isSearching, setIsSearching] = useState(false);
  const [isPreviewMembers, setIsPreviewMembers] = useState(false);

  const overlayRef = useRef<OverlayVisibleHandle>(null);
  const { isOverlayOpen, closeOverlay } = useOverlayVisible(overlayRef);
  const activity = useRef({ epoch: 0, open: false, preview: false });
  const [activityEpoch, setActivityEpoch] = useState(0);
  const closeSettings = useCallback(() => {
    setActivityEpoch(++activity.current.epoch); activity.current.open = false;
    closeOverlay();
  }, [closeOverlay]);
  useImperativeHandle(ref, () => ({ isOverlayOpen,
    openOverlay: () => { setActivityEpoch(++activity.current.epoch); activity.current.open = true; overlayRef.current?.openOverlay(); },
    closeOverlay: closeSettings,
  }), [isOverlayOpen, closeSettings]);
  activity.current.open = isOverlayOpen; activity.current.preview = isPreviewMembers;
  const membersActive = useCallback(() => activity.current.epoch === activityEpoch &&
    activity.current.open && activity.current.preview, [activityEpoch]);
  const settingsActive = useCallback(() => activity.current.epoch === activityEpoch &&
    activity.current.open && !activity.current.preview, [activityEpoch]);
  const preview = (value: boolean) => {
    setActivityEpoch(++activity.current.epoch); activity.current.preview = value; setIsPreviewMembers(value);
  };

  const searchMember = (keyword: string) => {
    memberListRef.current?.searchMember(keyword);
  };

  const updateSearching = (val: boolean) => {
    setIsSearching(val);
  };

  return (
    <Drawer
      title={
        !isPreviewMembers ? (
          t("placeholder.setting")
        ) : (
          <GroupMemberListHeader
            back2Settings={() => preview(false)}
            searchMemebers={searchMember}
            updateSearching={updateSearching}
          />
        )
      }
      destroyOnClose
      placement="right"
      rootClassName="chat-drawer"
      onClose={closeSettings}
      afterOpenChange={(visible) => {
        if (!visible) {
          setIsPreviewMembers(false);
        }
      }}
      open={isOverlayOpen}
      maskClassName="opacity-0"
      maskMotion={{
        visible: false,
      }}
      width={460}
      getContainer={"#chat-container"}
    >
      {!isPreviewMembers ? (
        <GroupSettings
          isActive={settingsActive}
          closeOverlay={closeSettings}
          updateTravel={() => preview(true)}
        />
      ) : (
        <GroupMemberList ref={memberListRef} isSearching={isSearching} isActive={membersActive} />
      )}
    </Drawer>
  );
};

export default memo(forwardRef(GroupSetting));
