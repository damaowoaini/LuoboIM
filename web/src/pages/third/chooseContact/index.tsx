import { subscribeSessionEventOnce } from "@/core/session/desktop";
import { CloseOutlined } from "@ant-design/icons";
import { Button } from "antd";
import { t } from "i18next";
import { FriendUserItem, GroupItem } from "open-im-sdk-wasm/lib/types/entity";
import { useEffect, useRef, useState } from "react";

import ChooseBox, { ChooseBoxHandle } from "@/pages/common/ChooseModal/ChooseBox";
import { useContactStore } from "@/store/contact";

interface ContactData {
  notConversation: boolean;
  friendList: FriendUserItem[];
  groupList: GroupItem[];
}

export const ChooseContact = () => {
  const chooseBoxRef = useRef<ChooseBoxHandle>(null);
  const [notConversation, setNotConversation] = useState(false);
  const setFriendList = useContactStore((state) => state.setFriendList);
  const setGroupList = useContactStore((state) => state.setGroupList);

  useEffect(() => {
    const raw = new URLSearchParams(window.location.hash.split("?")[1] || "").get(
      "precheck",
    );
    let contactData: ContactData = {
      notConversation: false,
      friendList: [],
      groupList: [],
    };
    try {
      if (raw) contactData = JSON.parse(raw) as ContactData;
    } catch {
      /* Empty initial selection remains valid. */
    }
    chooseBoxRef.current?.updatePrevCheckList([
      ...contactData.friendList,
      ...contactData.groupList,
    ]);
    setNotConversation(contactData.notConversation);

    subscribeSessionEventOnce("transferContactStoreData", (data: string) => {
      const contactData = JSON.parse(data) as ContactData;
      setFriendList(contactData.friendList);
      setGroupList(contactData.groupList);
    });
    window.electronAPI?.ipcInvoke("getContactStoreData", { key: "choose-contact" });
  }, []);

  const confirmChoose = () => {
    const choosedList = chooseBoxRef.current?.getCheckedList();
    window.electronAPI?.ipcInvoke("transferChooseModalData", {
      key: "moments",
      data: JSON.stringify({ notConversation, choosedList }),
    });
    closeWindow();
  };

  const closeWindow = () => {
    window.electronAPI?.ipcInvoke("closeWindow", "choose-contact");
  };

  return (
    <div className="flex h-screen flex-col">
      <div className="app-drag flex h-16 items-center justify-between bg-[var(--gap-text)] px-7">
        <div>{t("placeholder.selectMember")}</div>
        <CloseOutlined
          className="app-no-drag cursor-pointer text-[var(--sub-text)]"
          rev={undefined}
          onClick={closeWindow}
        />
      </div>
      <ChooseBox
        className="flex-1"
        ref={chooseBoxRef}
        notConversation={notConversation}
      />
      <div className="flex justify-end px-9 py-6">
        <Button
          className="mr-6 border-0 bg-[var(--chat-bubble)] px-6"
          onClick={closeWindow}
        >
          {t("cancel")}
        </Button>
        <Button className="px-6" type="primary" onClick={confirmChoose}>
          {t("confirm")}
        </Button>
      </div>
    </div>
  );
};
