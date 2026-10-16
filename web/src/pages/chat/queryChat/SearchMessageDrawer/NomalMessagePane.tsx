import { useMessagePrivacy } from "@/hooks/useMessagePrivacy";
import { Empty, Spin } from "antd";
import { t } from "i18next";
import { MessageType } from "open-im-sdk-wasm";
import { FC, useRef } from "react";
import { usePagedMessageSearch } from "@/core/usePagedMessageSearch";
import { Virtuoso } from "react-virtuoso";

import JumpToMessageWrap from "@/components/JumpToMessageWrap";
import OIMAvatar from "@/components/OIMAvatar";
import { canSearchMessageTypes } from "@/constants";
import { IMSDK } from "@/layout/MainContentWrap";
import { ExMessageItem } from "@/store";
import { useContactStore } from "@/store/contact";
import { feedbackToast } from "@/utils/common";
import { formatMessageTime } from "@/utils/imCommon";

import { IMessageItemProps } from "../MessageItem";
import CardMessageRenderer from "../MessageItem/CardMessageRenderer";
import CatchMessageRender from "../MessageItem/CatchMsgRenderer";
import CustomMessageSwitcher from "../MessageItem/CustomMessageSwitcher";
import FileMessageRenderer from "../MessageItem/FileMessageRenderer";
import LocationMessageRenderer from "../MessageItem/LocationMessageRenderer";
import MediaMessageRender from "../MessageItem/MediaMessageRender";
import TextMessageRender from "../MessageItem/TextMessageRender";
import VoiceMessageRender from "../MessageItem/VoiceMessageRender";
import MessageSearchBar from "./MessageSearchBar";

const NomalMessagePane = ({
  conversationID,
  isOverlayOpen,
  closeOverlay,
}: {
  conversationID?: string;
  isOverlayOpen: boolean;
  closeOverlay: () => void;
}) => {
  const loadMoreKeyword = useRef("");
  const inputRef = useRef<{ clear: () => void }>(null);
  const {search,...loadState}=usePagedMessageSearch<ExMessageItem>({
    conversationID,enabled:isOverlayOpen,requireKeyword:true,
    loadPage:async(keyword,pageIndex)=>{
      const {data}=await IMSDK.searchLocalMessages({conversationID:conversationID!,keywordList:[keyword],keywordListMatchType:0,senderUserIDList:[],messageTypeList:canSearchMessageTypes,searchTimePosition:0,searchTimePeriod:0,pageIndex,count:20});
      return data.searchResultItems?.[0]?.messageList??[];
    },
    onError:error=>feedbackToast({error,msg:t("toast.getMessageListFailed")}),
  });
  const triggerSearch=(keyword:string,loadMore=false)=>{loadMoreKeyword.current=keyword;search(keyword,loadMore);};

  return (
    <div className="flex h-full flex-col">
      <MessageSearchBar ref={inputRef} triggerSearch={triggerSearch} />
      <div className="m-2 flex-1">
        <Virtuoso
          className="h-full overflow-x-hidden"
          data={loadState.messageList}
          endReached={() => triggerSearch(loadMoreKeyword.current, true)}
          components={{
            EmptyPlaceholder: () =>
              loadState.loading ? null : (
                <Empty
                  className="flex h-full flex-col items-center justify-center"
                  description={t("empty.noSearchResults")}
                />
              ),
            Footer: () =>
              loadState.loading ? (
                <div className="flex w-full justify-center py-3">
                  <Spin spinning />
                </div>
              ) : null,
          }}
          itemContent={(_, message) => (
            <NomalMessageItem
              message={message}
              conversationID={conversationID}
              closeOverlay={closeOverlay}
            />
          )}
        />
      </div>
    </div>
  );
};

export default NomalMessagePane;

const components: Record<number, FC<IMessageItemProps>> = {
  [MessageType.TextMessage]: TextMessageRender,
  [MessageType.AtTextMessage]: TextMessageRender,
  [MessageType.QuoteMessage]: TextMessageRender,
  [MessageType.VoiceMessage]: VoiceMessageRender,
  [MessageType.PictureMessage]: MediaMessageRender,
  [MessageType.VideoMessage]: MediaMessageRender,
  [MessageType.CardMessage]: CardMessageRenderer,
  [MessageType.FileMessage]: FileMessageRenderer,
  [MessageType.LocationMessage]: LocationMessageRenderer,
  [MessageType.CustomMessage]: CustomMessageSwitcher,
};

export const NomalMessageItem = ({
  message,
  conversationID,
  closeOverlay,
}: {
  message: ExMessageItem;
  conversationID?: string;
  closeOverlay: () => void;
}) => {
  const privacy = useMessagePrivacy(message, conversationID);
  const jumpWrapRef = useRef<{ jumpToHistory: () => Promise<void> }>(null);
  const friendList = useContactStore((state) => state.friendList);

  const MessageRenderComponent: FC<IMessageItemProps> = components[message.contentType] || CatchMessageRender;

  const friend = message.groupID
    ? friendList.find((f) => f.userID === message.sendID)
    : undefined;
  const displayName = friend?.remark || message.senderNickname;

  if (privacy.expired) return <span className="text-xs text-slate-500">消息已过期或已移除</span>;
  return (
    <JumpToMessageWrap
      ref={jumpWrapRef}
      message={message}
      conversationID={conversationID!}
      afterJump={closeOverlay}
    >
      <div
        data-message-retention={privacy.allowsRetention ? "allowed" : "blocked"}
        onCopyCapture={event => { if (!privacy.allowsRetention) event.preventDefault(); }}
        onDragStartCapture={event => { if (!privacy.allowsRetention) event.preventDefault(); }}
        className="flex items-start rounded-md px-3.5 py-3 hover:bg-[var(--primary-active)]"
        onDoubleClick={() => jumpWrapRef.current?.jumpToHistory()}
      >
        <OIMAvatar src={message.senderFaceUrl} text={displayName} />
        <div className="ml-3 flex-1 select-text">
          <div className="mb-1 flex items-center text-xs">
            <div
              title={displayName}
              className="max-w-[30%] truncate text-[var(--sub-text)]"
            >
              {displayName}
            </div>
            <div className="ml-2 text-[var(--sub-text)]">
              {formatMessageTime(message.sendTime)}
            </div>
          </div>
          <MessageRenderComponent disabled message={message} isSender={false} conversationID={conversationID} />
        </div>
      </div>
    </JumpToMessageWrap>
  );
};
