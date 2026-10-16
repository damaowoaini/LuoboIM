import { Layout, Tooltip } from "antd";
import clsx from "clsx";
import i18n, { t } from "i18next";
import { CbEvents } from "open-im-sdk-wasm";
import { GroupAtType, OnlineState, Platform, SessionType } from "open-im-sdk-wasm";
import { UserOnlineState, WSEvent } from "open-im-sdk-wasm/lib/types/entity";
import { memo, useCallback, useEffect, useRef, useState } from "react";

import group_member from "@/assets/images/chatHeader/group_member.png";
import group_notice from "@/assets/images/chatHeader/group_notice.png";
import launch_group from "@/assets/images/chatHeader/launch_group.png";
import search_history from "@/assets/images/chatHeader/search_history.png";
import settings from "@/assets/images/chatHeader/settings.png";
import OIMAvatar from "@/components/OIMAvatar";
import UserLoginInfo from "@/components/UserLoginInfo";
import UserLoginRecordList from "@/components/UserLoginRecordList";
import { OverlayVisibleHandle } from "@/hooks/useOverlayVisible";
import { IMSDK } from "@/layout/MainContentWrap";
import { useConversationStore, useUserStore } from "@/store";
import emitter from "@/utils/events";
import { isGroupSession } from "@/utils/imCommon";
import { Drawer, message } from "antd";

import GroupAnnouncementDrawer from "../GroupAnnouncementDrawer";
import GroupSetting from "../GroupSetting";
import SearchMessageDrawer from "../SearchMessageDrawer";
import SingleSetting from "../SingleSetting";
import styles from "./chat-header.module.scss";
import GroupAnnouncementCard from "./GroupAnnouncementCard";

const menuList = [
  {
    title: t("placeholder.groupAnnouncement"),
    icon: group_notice,
    idx: 0,
  },
  {
    title: t("placeholder.historyList"),
    icon: search_history,
    idx: 1,
  },
  // {
  //   title: t("placeholder.file"),
  //   icon: file_manage,
  //   idx: 2,
  // },
  {
    title: t("placeholder.createGroup"),
    icon: launch_group,
    idx: 3,
  },
  {
    title: t("placeholder.invitation"),
    icon: launch_group,
    idx: 4,
  },
  {
    title: t("placeholder.setting"),
    icon: settings,
    idx: 5,
  },
];

i18n.on("languageChanged", () => {
  menuList[0].title = t("placeholder.groupAnnouncement");
  menuList[1].title = t("placeholder.historyList");
  menuList[2].title = t("placeholder.createGroup");
  menuList[3].title = t("placeholder.invitation");
  menuList[4].title = t("placeholder.setting");
});

const ChatHeader = () => {
  const singleSettingRef = useRef<OverlayVisibleHandle>(null);
  const groupSettingRef = useRef<OverlayVisibleHandle>(null);
  const searchMessageRef = useRef<OverlayVisibleHandle>(null);
  const groupAnnouncementRef = useRef<OverlayVisibleHandle>(null);
  const [loginRecordsVisible, setLoginRecordsVisible] = useState(false);
  const [showCopySuccess, setShowCopySuccess] = useState(false);

  const currentConversation = useConversationStore(
    (state) => state.currentConversation,
  );
  const currentGroupInfo = useConversationStore((state) => state.currentGroupInfo);
  const inGroup = useConversationStore((state) =>
    Boolean(state.currentMemberInGroup?.groupID),
  );

  // locale re render
  useUserStore((state) => state.appSettings.locale);

  useEffect(() => {
    if (singleSettingRef.current?.isOverlayOpen) {
      singleSettingRef.current?.closeOverlay();
    }
    if (groupSettingRef.current?.isOverlayOpen) {
      groupSettingRef.current?.closeOverlay();
    }
    if (groupAnnouncementRef.current?.isOverlayOpen) {
      groupAnnouncementRef.current?.closeOverlay();
    }
    if (searchMessageRef.current?.isOverlayOpen) {
      searchMessageRef.current?.closeOverlay();
    }
  }, [currentConversation?.conversationID]);

  const openGroupAnnouncementDrawer = useCallback(() => {
    groupAnnouncementRef.current?.openOverlay();
  }, []);

  const menuClick = (idx: number) => {
    switch (idx) {
      case 0:
        groupAnnouncementRef.current?.openOverlay();
        break;
      case 1:
        searchMessageRef.current?.openOverlay();
        break;
      // case 2:
      //   break;
      case 3:
      case 4:
        emitter.emit("OPEN_CHOOSE_MODAL", {
          type: isSingle ? "CRATE_GROUP" : "INVITE_TO_GROUP",
          extraData: isSingle
            ? [{ ...currentConversation }]
            : currentConversation?.groupID,
        });
        break;
      case 5:
        if (isGroupSession(currentConversation?.conversationType)) {
          groupSettingRef.current?.openOverlay();
        } else {
          singleSettingRef.current?.openOverlay();
        }
        break;
      default:
        break;
    }
  };

  const showCard = () => {
    if (isSingle || isNotification) {
      window.userClick(currentConversation?.userID);
      return;
    }
    if (currentGroupInfo) {
      emitter.emit("OPEN_GROUP_CARD", currentGroupInfo);
    }
  };

  const isNotification =
    currentConversation?.conversationType === SessionType.Notification;
  const isSingle = currentConversation?.conversationType === SessionType.Single;

  const hasGroupAnnouncement =
    currentConversation?.groupAtType === GroupAtType.AtGroupNotice;

  const handleCopyName = () => {
    if (currentConversation?.showName) {
      navigator.clipboard.writeText(currentConversation.showName).then(() => {
        setShowCopySuccess(true);
        setTimeout(() => setShowCopySuccess(false), 2000);
        message.success(`已复制: ${currentConversation.showName}`);
      }).catch(() => {
        message.error("复制失败，请重试");
      });
    }
  };

  return (
    <Layout.Header className="app-chat-header relative border-b border-b-[var(--gap-text)] !bg-white !px-3">
      <div className="app-chat-header-content flex h-full items-center leading-none">
        <div className="flex flex-1 items-center overflow-hidden">
          <OIMAvatar
            src={currentConversation?.faceURL}
            text={currentConversation?.showName}
            isgroup={Boolean(currentConversation?.groupID)}
            isnotification={isNotification}
            onClick={showCard}
          />
          <div
            className={clsx(
              "ml-3 flex flex-1 flex-col justify-center overflow-hidden",
              isNotification && "!justify-center",
            )}
          >
            <div className="relative">
              <div 
                className="truncate text-base font-semibold cursor-pointer select-text hover:text-[var(--primary)] transition-colors"
                onDoubleClick={handleCopyName}
                title="双击复制用户名"
              >
                {currentConversation?.showName}
              </div>
              {showCopySuccess && (
                <div className="absolute left-0 top-full mt-1 bg-green-500 text-white px-3 py-1 rounded text-xs whitespace-nowrap shadow-lg animate-fade-in">
                  ✓ 已复制
                </div>
              )}
            </div>
            {isSingle && (
              <div className="flex flex-col gap-0.5">
                <OnlineOrTypingStatus userID={currentConversation?.userID} />
                <UserLoginInfo 
                  userID={currentConversation?.userID} 
                  showInline={true}
                  onClick={() => setLoginRecordsVisible(true)}
                />
              </div>
            )}
            {!isSingle && !isNotification && (
              <div className="flex items-center text-xs text-[var(--sub-text)]">
                <img width={20} src={group_member} alt="member" />
                <span>{currentGroupInfo?.memberCount}</span>
              </div>
            )}
          </div>
        </div>

        {!isNotification && (
          <div className="app-chat-header-actions mr-5 flex">
            {menuList.map((menu) => {
              if (
                (menu.idx === 0 || menu.idx === 4) &&
                (isSingle || (!inGroup && !isSingle))
              ) {
                return null;
              }
              if (menu.idx === 3 && !isSingle) {
                return null;
              }

              return (
                <Tooltip title={menu.title} key={menu.idx}>
                  <button type="button" className="app-icon-button" aria-label={menu.title}
                    onClick={() => menuClick(menu.idx)}>
                    <img width={20} src={menu.icon} alt="" />
                  </button>
                </Tooltip>
              );
            })}
          </div>
        )}
      </div>
      {hasGroupAnnouncement && (
        <GroupAnnouncementCard
          currentGroupInfo={currentGroupInfo}
          conversationID={currentConversation?.conversationID}
          openGroupAnnouncementDrawer={openGroupAnnouncementDrawer}
        />
      )}
      {/* <GroupCallingCard currentGroupInfo={currentGroupInfo} /> */}
      <SingleSetting ref={singleSettingRef} />
      <GroupSetting ref={groupSettingRef} />
      <SearchMessageDrawer ref={searchMessageRef} />
      <GroupAnnouncementDrawer ref={groupAnnouncementRef} />
      
      {/* 登录记录抽屉 */}
      <Drawer
        title={`${currentConversation?.showName} 的登录记录`}
        placement="right"
        width={600}
        open={loginRecordsVisible}
        onClose={() => setLoginRecordsVisible(false)}
      >
        {loginRecordsVisible && currentConversation?.userID && (
          <UserLoginRecordList userID={currentConversation.userID} />
        )}
      </Drawer>
    </Layout.Header>
  );
};

export default memo(ChatHeader);

const OnlineOrTypingStatus = ({ userID }: { userID: string }) => {
  const [typing, setTyping] = useState(false);
  const [onlineState, setOnlineState] = useState<UserOnlineState>();
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    const userStatusChangeHandler = ({ data }: WSEvent<UserOnlineState>) => {
      if (data.userID === userID) {
        setOnlineState(data);
      }
    };
    IMSDK.on(CbEvents.OnUserStatusChanged, userStatusChangeHandler);
    IMSDK.subscribeUsersStatus([userID]).then(({ data }) => setOnlineState(data[0]));
    return () => {
      IMSDK.off(CbEvents.OnUserStatusChanged, userStatusChangeHandler);
      IMSDK.unsubscribeUsersStatus([userID]);
      setTyping(false);
    };
  }, [userID]);

  useEffect(() => {
    const typingHandler = () => {
      setTyping(true);
      timer.current = setTimeout(() => {
        if (timer.current) {
          clearTimeout(timer.current);
        }
        setTyping(false);
      }, 500);
    };
    emitter.on("TYPING_UPDATE", typingHandler);
    return () => {
      if (timer.current) {
        clearTimeout(timer.current);
      }
      emitter.off("TYPING_UPDATE", typingHandler);
    };
  }, []);

  return (
    <div className="flex items-center">
      {typing ? (
        <p className="text-xs text-[var(--sub-text)]">
          {t("placeholder.typing")} <span className={styles["dot-1"]}>.</span>
          <span className={styles["dot-2"]}>.</span>
          <span className={styles["dot-3"]}>.</span>
        </p>
      ) : (
        <>
          <i
            className={clsx(
              "mr-1.5 inline-block h-[6px] w-[6px] rounded-full bg-[#2ddd73]",
              {
                "bg-[#999]": onlineState?.status === OnlineState.Offline,
              },
            )}
          />
          <span className="text-xs text-[var(--sub-text)]">
            {platformToDetails(onlineState)}
          </span>
        </>
      )}
    </div>
  );
};

const platformMap: Record<Platform, string> = {
  1: "iOS",
  2: "Android",
  3: "Windows",
  4: "MacOSX",
  5: "Web",
  // @ts-ignore
  6: "MiniProgram",
  7: "Linux",
  8: "AndroidPad",
  9: "iPad",
};

const platformToDetails = (state?: UserOnlineState) => {
  if (!state || state.status === OnlineState.Offline) return t("placeholder.offLine");
  let string = "";
  state.platformIDs?.map((platform) => (string += `${platformMap[platform]}/`));
  return `${string.slice(0, -1)}${t("placeholder.online")}`;
};
