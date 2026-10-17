package conversation

import (
	"context"

	"github.com/OpenIMSDK/protocol/constant"
	pb "github.com/OpenIMSDK/protocol/conversation"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/internal/retention"
)

// A client preference patch may not redirect the peer receiving a private-mode
// change or invent a different owner/type for an already stored conversation.
func (c *conversationServer) validateLifecycleRequest(ctx context.Context, req *pb.SetConversationsReq) error {
	v := req.Conversation
	if v == nil {
		return errs.ErrArgs.Wrap("conversation required")
	}
	if v.IsPrivateChat == nil && v.BurnDuration == nil && v.IsMsgDestruct == nil && v.MsgDestructTime == nil {
		return nil
	}
	if len(req.UserIDs) == 0 {
		return errs.ErrArgs.Wrap("owner required")
	}
	if (v.IsPrivateChat != nil || v.BurnDuration != nil) && v.ConversationType != constant.SingleChatType {
		return errs.ErrArgs.Wrap("private settings require single chat")
	}
	if v.BurnDuration != nil && v.BurnDuration.Value < 0 {
		return errs.ErrArgs.Wrap("invalid burn duration")
	}
	for _, owner := range req.UserIDs {
		identity := &pb.Conversation{OwnerUserID: owner, ConversationID: v.ConversationID, ConversationType: v.ConversationType, UserID: v.UserID, GroupID: v.GroupID}
		if err := c.validateConversationIdentity(ctx, identity); err != nil {
			return err
		}
		rows, err := c.conversationDatabase.FindConversations(ctx, owner, []string{v.ConversationID})
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return errs.ErrRecordNotFound.Wrap("conversation not found")
		}
		p := rows[0]
		if p.OwnerUserID != owner || p.ConversationID != v.ConversationID || p.ConversationType != v.ConversationType || p.UserID != v.UserID || p.GroupID != v.GroupID {
			return errs.ErrArgs.Wrap("conversation identity changed")
		}
		enabled, duration := p.IsMsgDestruct, p.MsgDestructTime
		if v.IsMsgDestruct != nil {
			enabled = v.IsMsgDestruct.Value
		}
		if v.MsgDestructTime != nil {
			duration = v.MsgDestructTime.Value
		}
		if v.IsMsgDestruct != nil || v.MsgDestructTime != nil {
			if err := retention.ValidateDuration(enabled, duration); err != nil {
				return errs.ErrArgs.Wrap("invalid retention duration")
			}
		}
	}
	return nil
}
