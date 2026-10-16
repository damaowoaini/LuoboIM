import { CloseOutlined } from "@ant-design/icons";
import { Button, Input, Radio } from "antd";
import { t } from "i18next";
import {
  FullUserItemWithCache,
  GroupItem,
  WSEvent,
} from "open-im-sdk-wasm/lib/types/entity";
import { forwardRef, ForwardRefRenderFunction, memo, useEffect, useRef, useState } from "react";

import { message } from "@/AntdGlobalComp";
import { getBusinessUserInfo, searchBusinessUserInfo } from "@/api/login";
import { sessionCoordinator } from "@/core/session/browser";
import { resolveContactLookup, type ContactLookupMode } from "@/core/contactLookup";
import DraggableModalWrap from "@/components/DraggableModalWrap";
import { OverlayVisibleHandle, useOverlayVisible } from "@/hooks/useOverlayVisible";
import { feedbackToast } from "@/utils/common";

import { IMSDK } from "../MainContentWrap";

interface ISearchUserOrGroupProps {
  isSearchGroup: boolean;
  openUserCardWithID: (userID: string) => void;
  openGroupCardWithData: (data: GroupItem) => void;
}

const SearchUserOrGroup: ForwardRefRenderFunction<
  OverlayVisibleHandle,
  ISearchUserOrGroupProps
> = ({ isSearchGroup, openUserCardWithID, openGroupCardWithData }, ref) => {
  const [keyword, setKeyword] = useState("");
  const [mode, setMode] = useState<ContactLookupMode>("userID");
  const [loading, setLoading] = useState(false);
  const ticket = useRef(0);
  const live = useRef({ open: false, generation: 0, session: sessionCoordinator.current });
  const { isOverlayOpen, closeOverlay } = useOverlayVisible(ref);
  live.current = { open: isOverlayOpen, generation: sessionCoordinator.generation, session: sessionCoordinator.current };
  useEffect(() => { ++ticket.current; setLoading(false); }, [isOverlayOpen, isSearchGroup]);
  useEffect(() => () => { ++ticket.current; live.current.open = false; }, []);
  const close = () => { ++ticket.current; live.current.open = false; setLoading(false); closeOverlay(); };

  const searchData = async () => {
    if (!keyword.trim() || loading || !isOverlayOpen) return;
    const request = ++ticket.current;
    const session = sessionCoordinator.current;
    const generation = sessionCoordinator.generation;
    const current = () => live.current.open && request === ticket.current
      && sessionCoordinator.current === session && sessionCoordinator.generation === generation;
    setLoading(true);

    try {
      if (isSearchGroup) {
        const { data } = await IMSDK.getSpecifiedGroupsInfo([keyword]);
        if (!current()) return;
        const groupInfo = data[0];
        if (!groupInfo) {
          message.warning(t("empty.noSearchResults"));
          return;
        }
        openGroupCardWithData(groupInfo);
      } else {
        const result = await resolveContactLookup(mode, keyword, {
          findByIDs: async (userIDs) => (await getBusinessUserInfo(userIDs)).data.users,
          searchPhone: async (phone) => (await searchBusinessUserInfo(phone)).data,
        });
        if (!current()) return;
        if (result.state !== "found") {
          const key = result.state === "ambiguous" ? "toast.contactLookupAmbiguous"
            : result.state === "invalid" ? "toast.contactLookupInvalid" : "empty.noSearchResults";
          message.warning(t(key));
          return;
        }
        // No incomplete cardInfo: UserCard must perform its existing exact SDK
        // and masked business-profile hydration, including allowAddFriend.
        openUserCardWithID(result.userID);
      }
    } catch (error) {
      if (!current()) return;
      if ((error as WSEvent).errCode === 1004) {
          message.warning(t("empty.noSearchResults"));
          return;
      }
      feedbackToast({ error });
    } finally {
      if (current()) setLoading(false);
    }
  };

  return (
    <DraggableModalWrap
      title={null}
      footer={null}
      open={isOverlayOpen}
      closable={false}
      width={332}
      onCancel={close}
      styles={{
        mask: {
          opacity: 0,
          transition: "none",
        },
      }}
      afterClose={() => {
        setKeyword("");
        setMode("userID");
      }}
      ignoreClasses=".ignore-drag, .cursor-pointer"
      className="no-padding-modal"
      maskTransitionName=""
    >
      <div className="flex h-12 items-center justify-between bg-[var(--gap-text)] px-5.5">
        <div>
          {isSearchGroup ? t("placeholder.addGroup") : t("placeholder.addFriends")}
        </div>
        <CloseOutlined
          className="cursor-pointer text-[var(--sub-text)]"
          rev={undefined}
          onClick={close}
        />
      </div>
      <div className="ignore-drag">
        <div className="border-b border-[var(--gap-text)] px-5.5 py-6">
          {!isSearchGroup && (
            <Radio.Group
              className="mb-3"
              value={mode}
              onChange={(event) => {
                ++ticket.current;
                setLoading(false);
                setMode(event.target.value);
                setKeyword("");
              }}
              aria-label={t("placeholder.contactLookupMode")}
            >
              <Radio.Button value="userID">{t("placeholder.recipientAccountID")}</Radio.Button>
              <Radio.Button value="phoneNumber">{t("placeholder.phoneNumber")}</Radio.Button>
            </Radio.Group>
          )}
          <label className="mb-2 block" htmlFor="contact-lookup-keyword">
            {isSearchGroup ? t("placeholder.recipientGroupID")
              : mode === "userID" ? t("placeholder.recipientAccountID") : t("placeholder.phoneNumber")}
          </label>
          <Input.Search
            id="contact-lookup-keyword"
            className="no-addon-search"
            placeholder={t("placeholder.pleaseEnter")}
            value={keyword}
            addonAfter={null}
            spellCheck={false}
            onChange={(e) => {
              ++ticket.current;
              setLoading(false);
              setKeyword(e.target.value);
            }}
            onSearch={searchData}
          />
        </div>
        <div className="flex justify-end px-5.5 py-2.5">
          <Button
            className="px-6"
            type="primary"
            disabled={!keyword}
            loading={loading}
            onClick={searchData}
          >
            {t("confirm")}
          </Button>
          <Button
            className="ml-3 border-0 bg-[var(--chat-bubble)] px-6"
            onClick={close}
          >
            {t("cancel")}
          </Button>
        </div>
      </div>
    </DraggableModalWrap>
  );
};

export default memo(forwardRef(SearchUserOrGroup));
