package msg

import (
	"context"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/mcontext"

	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
)

// Every receipt operation is bound to a real message in the target member's
// visible history. Possession of a group or client message ID is not authority.
func (m *msgServer) groupReceiptScope(ctx context.Context, owner, conversationID string, ids []string) (string, string, map[string]*sdkws.MsgData, []string, error) {
	actor := mcontext.GetOpUserID(ctx)
	if owner == "" {
		owner = actor
	}
	if err := authverify.CheckAccessV3(ctx, owner); err != nil {
		return "", "", nil, nil, err
	}
	if !strings.HasPrefix(conversationID, "sg_") || len(conversationID) <= 3 || len(ids) < 1 || len(ids) > 100 {
		return "", "", nil, nil, errs.ErrArgs.Wrap("invalid group receipt scope or batch size")
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || len(id) > 256 || seen[id] {
			return "", "", nil, nil, errs.ErrArgs.Wrap("invalid or duplicate client message ID")
		}
		seen[id] = true
	}
	groupID := strings.TrimPrefix(conversationID, "sg_")
	members, err := m.Group.GetGroupMemberIDs(ctx, groupID)
	if err != nil {
		return "", "", nil, nil, err
	}
	isMember := false
	for _, member := range members {
		if member == owner {
			isMember = true
		}
	}
	if !isMember {
		return "", "", nil, nil, errs.ErrNoPermission.Wrap("group receipts require current group membership")
	}
	row, err := m.Conversation.GetConversation(ctx, owner, conversationID)
	if err != nil {
		return "", "", nil, nil, err
	}
	if row == nil || row.OwnerUserID != owner || row.ConversationID != conversationID || row.GroupID != groupID || row.ConversationType != constant.SuperGroupChatType {
		return "", "", nil, nil, errs.ErrNoPermission.Wrap("group receipt conversation is not owned")
	}
	messages, err := m.MsgDatabase.GetMsgByClientMsgIDs(ctx, owner, conversationID, ids)
	if err != nil {
		return "", "", nil, nil, err
	}
	for _, id := range ids {
		message := messages[id]
		if message == nil || message.ClientMsgID != id || message.SendID == "" || message.SessionType != constant.SuperGroupChatType || message.GroupID != groupID || message.Seq < 1 || message.Seq < row.MinSeq || (row.MaxSeq > 0 && message.Seq > row.MaxSeq) || message.ContentType >= 1000 {
			return "", "", nil, nil, errs.ErrNoPermission.Wrap("receipt message is absent or outside visible group history")
		}
	}
	return groupID, owner, messages, members, nil
}

func receiptAudience(members []string, sender string) []string {
	result := make([]string, 0, len(members))
	seen := make(map[string]bool, len(members))
	for _, user := range members {
		if user != "" && user != sender && !seen[user] {
			seen[user] = true
			result = append(result, user)
		}
	}
	return result
}

func (m *msgServer) markGroupMessageRead(ctx context.Context, conversationID, userID string, groupID string, mark map[string]int32, readTime int64) {
	users := []*sdkws.GroupMsgReadTipsUser{{UserID: userID, ReadTime: readTime}}
	tips := sdkws.GroupMsgReadTips{Reads: make([]*sdkws.GroupMsgReadTipsRead, 0, len(mark))}
	for clientID, ok := range mark {
		if ok == constant.GroupMessageReadMarkNew {
			readNum, unreadNum, err := m.MsgGroupReadDatabase.GetMsgNum(ctx, conversationID, clientID)
			if err != nil {
				log.ZError(ctx, "markGroupMessageRead GetMsgReadNum failed", err, "conversationID", conversationID, "clientID", clientID)
			}
			tips.Reads = append(tips.Reads, &sdkws.GroupMsgReadTipsRead{
				ConversationID: conversationID,
				ClientMsgID:    clientID,
				ReadNum:        readNum,
				UnreadNum:      unreadNum,
				Users:          users,
			})
		}
	}
	if len(tips.Reads) == 0 {
		return
	}
	_ = m.notificationSender.NotificationWithSesstionType(ctx, userID, groupID, constant.HasGroupReadReceipt, constant.SuperGroupChatType, &tips)
}

func (m *msgServer) MarkGroupMessageRead(ctx context.Context, req *msg.MarkGroupMessageReadReq) (*msg.MarkGroupMessageReadResp, error) {
	ids := make([]string, 0, len(req.ClientMsgs))
	for id := range req.ClientMsgs {
		ids = append(ids, id)
	}
	groupID, owner, messages, members, err := m.groupReceiptScope(ctx, req.UserID, req.ConversationID, ids)
	if err != nil {
		return nil, err
	}
	// Validate the complete batch before making any writes. An empty sender
	// remains compatible; a supplied legacy sender must match stored metadata.
	for id, claimedSender := range req.ClientMsgs {
		if claimedSender != "" && claimedSender != messages[id].SendID {
			return nil, errs.ErrNoPermission.Wrap("client-reported message sender does not match stored message")
		}
	}
	resp := &msg.MarkGroupMessageReadResp{Mark: make(map[string]int32)}
	now := time.Now()
	for clientMsgID := range req.ClientMsgs {
		sender := messages[clientMsgID].SendID
		audience := receiptAudience(members, sender)
		users := func(context.Context) ([]string, error) { return audience, nil }
		if err := m.MsgGroupReadDatabase.EnsureGroupMessage(ctx, req.ConversationID, clientMsgID, users); err != nil {
			return nil, err
		}
		if sender == owner {
			resp.Mark[clientMsgID] = constant.GroupMessageReadMarked
			continue
		}
		state, err := m.MsgGroupReadDatabase.MarkGroupMessageRead(ctx, req.ConversationID, clientMsgID, owner, &now, users)
		if err != nil {
			return nil, err
		}
		resp.Mark[clientMsgID] = state
	}
	m.markGroupMessageRead(ctx, req.ConversationID, owner, groupID, resp.Mark, now.UnixMilli())
	return resp, nil
}

func (m *msgServer) GetGroupMessageReadNum(ctx context.Context, req *msg.GetGroupMessageReadNumReq) (*msg.GetGroupMessageReadNumResp, error) {
	_, _, messages, members, err := m.groupReceiptScope(ctx, req.UserID, req.ConversationID, req.ClientMsgIDs)
	if err != nil {
		return nil, err
	}
	resp := &msg.GetGroupMessageReadNumResp{Num: make(map[string]*msg.MessageReadInfo)}
	for _, clientMsgID := range req.ClientMsgIDs {
		audience := receiptAudience(members, messages[clientMsgID].SendID)
		if err := m.MsgGroupReadDatabase.EnsureGroupMessage(ctx, req.ConversationID, clientMsgID, func(context.Context) ([]string, error) { return audience, nil }); err != nil {
			return nil, err
		}
		readNum, unreadNum, err := m.MsgGroupReadDatabase.GetMsgNum(ctx, req.ConversationID, clientMsgID)
		if err != nil {
			return nil, err
		}
		resp.Num[clientMsgID] = &msg.MessageReadInfo{
			ReadNum:   readNum,
			UnreadNum: unreadNum,
		}
	}
	return resp, nil
}

func (m *msgServer) GetGroupMessageHasRead(ctx context.Context, req *msg.GetGroupMessageHasReadReq) (*msg.GetGroupMessageHasReadResp, error) {
	if req.Pagination == nil || req.Pagination.PageNumber < 1 || req.Pagination.ShowNumber < 1 || req.Pagination.ShowNumber > 100 {
		return nil, errs.ErrArgs.Wrap("pagination is invalid")
	}
	var read bool
	switch req.Type {
	case constant.GroupMessageReadList:
		read = true
	case constant.GroupMessageUnreadList:
		read = false
	default:
		return nil, errs.ErrArgs.Wrap("type is invalid")
	}
	_, _, messages, members, err := m.groupReceiptScope(ctx, "", req.ConversationID, []string{req.ClientMsgID})
	if err != nil {
		return nil, err
	}
	audience := receiptAudience(members, messages[req.ClientMsgID].SendID)
	if err := m.MsgGroupReadDatabase.EnsureGroupMessage(ctx, req.ConversationID, req.ClientMsgID, func(context.Context) ([]string, error) { return audience, nil }); err != nil {
		return nil, err
	}
	count, res, err := m.MsgGroupReadDatabase.PageReadUserList(ctx, req.ConversationID, req.ClientMsgID, read, req.Pagination.PageNumber, req.Pagination.ShowNumber)
	if err != nil {
		return nil, err
	}
	resp := &msg.GetGroupMessageHasReadResp{
		Count: count,
		Reads: make([]*msg.GroupMessageUserReadTime, 0, len(res)),
	}
	for _, re := range res {
		resp.Reads = append(resp.Reads, &msg.GroupMessageUserReadTime{
			UserID:   re.UserID,
			ReadTime: re.Time,
		})
	}
	return resp, nil
}
