package api

import (
	"context"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/conversation"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/internal/favorite"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
)

type favoriteMessageSource struct {
	conversations conversation.ConversationClient
	messages      controller.MessageIdentityReader
}

func favoriteSourceAbsent(err error) bool {
	code, ok := errs.Unwrap(err).(errs.CodeError)
	return ok && (code.Code() == errs.NoPermissionError || code.Code() == errs.RecordNotFoundError ||
		code.Code() == errs.UserIDNotFoundError || code.Code() == errs.GroupIDNotFoundError)
}

func (s *favoriteMessageSource) Retainable(ctx context.Context, actor, conversationID, clientID string) (bool, error) {
	resp, err := s.conversations.GetConversation(ctx, &conversation.GetConversationReq{OwnerUserID: actor, ConversationID: conversationID})
	if err != nil {
		if favoriteSourceAbsent(err) {
			return false, nil
		}
		return false, err
	}
	if resp == nil || resp.Conversation == nil {
		return false, nil
	}
	row := resp.Conversation
	if row.OwnerUserID != actor || row.ConversationID != conversationID ||
		(row.ConversationType != constant.SingleChatType && row.ConversationType != constant.SuperGroupChatType && row.ConversationType != constant.GroupChatType) {
		return false, nil
	}
	messages, err := s.messages.GetMsgByClientMsgIDs(ctx, actor, conversationID, []string{clientID})
	if err != nil {
		if favoriteSourceAbsent(err) {
			return false, nil
		}
		return false, err
	}
	message := messages[clientID]
	if message == nil || message.ClientMsgID != clientID || message.Seq < 1 || message.Seq < row.MinSeq ||
		(row.MaxSeq > 0 && message.Seq > row.MaxSeq) || message.ContentType >= 1000 || message.Status == constant.MsgDeleted ||
		message.SessionType != row.ConversationType ||
		msgprocessor.GetConversationIDByMsg(message) != conversationID {
		return false, nil
	}
	if message.SessionType == constant.SingleChatType {
		if message.SendID != actor && message.RecvID != actor {
			return false, nil
		}
	} else if message.GroupID == "" || message.GroupID != row.GroupID {
		return false, nil
	}
	return favorite.MessageAllowsRetention(message.AttachedInfo, message.ContentType, message.Content), nil
}
