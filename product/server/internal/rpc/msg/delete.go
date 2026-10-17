// Copyright © 2023 OpenIM. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package msg

import (
	"context"

	"github.com/openimsdk/open-im-server/v3/pkg/authverify"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/conversation"
	"github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/utils"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
)

func (m *msgServer) ownedMessageConversation(ctx context.Context, owner, id string) (*conversation.Conversation, error) {
	if err := authverify.CheckAccessV3(ctx, owner); err != nil {
		return nil, err
	}
	row, err := m.Conversation.GetConversation(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	if row == nil || row.OwnerUserID != owner || row.ConversationID != id || id == "" {
		return nil, errs.ErrNoPermission.Wrap("message conversation is not owned")
	}
	valid := false
	switch row.ConversationType {
	case constant.SingleChatType:
		valid = row.UserID != "" && id == msgprocessor.GetConversationIDBySessionType(constant.SingleChatType, owner, row.UserID)
	case constant.GroupChatType, constant.SuperGroupChatType:
		valid = row.GroupID != "" && id == msgprocessor.GetConversationIDBySessionType(int(row.ConversationType), row.GroupID)
	case constant.NotificationChatType:
		valid = id == "sn_"+owner || (row.UserID != "" && id == "sn_"+row.UserID+"_"+owner)
	}
	if !valid {
		return nil, errs.ErrNoPermission.Wrap("message conversation does not match its participants")
	}
	return row, nil
}

func (m *msgServer) mayClearSharedConversation(ctx context.Context, owner string, row *conversation.Conversation) error {
	if authverify.IsAppManagerUid(ctx) {
		return nil
	}
	if row.ConversationType == constant.SingleChatType {
		// Retain the existing explicit both-sides clear for a real participant.
		return nil
	}
	if row.ConversationType == constant.GroupChatType || row.ConversationType == constant.SuperGroupChatType {
		member, err := m.Group.GetGroupMemberInfo(ctx, row.GroupID, owner)
		if err != nil {
			return err
		}
		if member != nil && member.RoleLevel >= constant.GroupAdmin {
			return nil
		}
	}
	return errs.ErrNoPermission.Wrap("shared conversation clearing requires management permission")
}

func (m *msgServer) getMinSeqs(maxSeqs map[string]int64) map[string]int64 {
	minSeqs := make(map[string]int64)
	for k, v := range maxSeqs {
		minSeqs[k] = v + 1
	}
	return minSeqs
}

func (m *msgServer) validateDeleteSyncOpt(opt *msg.DeleteSyncOpt) (isSyncSelf, isSyncOther bool) {
	if opt == nil {
		return
	}
	return opt.IsSyncSelf, opt.IsSyncOther
}

func (m *msgServer) ClearConversationsMsg(
	ctx context.Context,
	req *msg.ClearConversationsMsgReq,
) (*msg.ClearConversationsMsgResp, error) {
	if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	if err := m.clearConversation(ctx, req.ConversationIDs, req.UserID, req.DeleteSyncOpt); err != nil {
		return nil, err
	}
	return &msg.ClearConversationsMsgResp{}, nil
}

func (m *msgServer) UserClearAllMsg(
	ctx context.Context,
	req *msg.UserClearAllMsgReq,
) (*msg.UserClearAllMsgResp, error) {
	if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	conversationIDs, err := m.ConversationLocalCache.GetConversationIDs(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	log.ZDebug(ctx, "GetMaxSeq", "conversationIDs", conversationIDs)
	if err := m.clearConversation(ctx, conversationIDs, req.UserID, req.DeleteSyncOpt); err != nil {
		return nil, err
	}
	return &msg.UserClearAllMsgResp{}, nil
}

func (m *msgServer) DeleteMsgs(ctx context.Context, req *msg.DeleteMsgsReq) (*msg.DeleteMsgsResp, error) {
	if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	if len(req.Seqs) < 1 || len(req.Seqs) > 100 {
		return nil, errs.ErrArgs.Wrap("message deletion requires 1..100 sequences")
	}
	for _, seq := range req.Seqs {
		if seq < 1 {
			return nil, errs.ErrArgs.Wrap("message deletion sequence must be positive")
		}
	}
	row, err := m.ownedMessageConversation(ctx, req.UserID, req.ConversationID)
	if err != nil {
		return nil, err
	}
	isSyncSelf, isSyncOther := m.validateDeleteSyncOpt(req.DeleteSyncOpt)
	if isSyncOther {
		_, _, messages, err := m.MsgDatabase.GetMsgBySeqs(ctx, req.UserID, req.ConversationID, req.Seqs)
		if err != nil {
			return nil, err
		}
		seqs := make([]int64, 0, len(messages))
		for _, message := range messages {
			if message == nil || message.Seq < row.MinSeq || (row.MaxSeq > 0 && message.Seq > row.MaxSeq) {
				continue
			}
			if message.SendID != req.UserID && !authverify.IsAppManagerUid(ctx) {
				if (row.ConversationType != constant.GroupChatType && row.ConversationType != constant.SuperGroupChatType) || m.mayClearSharedConversation(ctx, req.UserID, row) != nil {
					return nil, errs.ErrNoPermission.Wrap("shared deletion requires original sender or group management")
				}
			}
			seqs = append(seqs, message.Seq)
		}
		if len(seqs) == 0 {
			return &msg.DeleteMsgsResp{}, nil
		}
		if err := m.MsgDatabase.DeleteMsgsPhysicalBySeqs(ctx, req.ConversationID, seqs); err != nil {
			return nil, err
		}
		tips := &sdkws.DeleteMsgsTips{UserID: req.UserID, ConversationID: req.ConversationID, Seqs: seqs}
		m.notificationSender.NotificationWithSesstionType(
			ctx,
			req.UserID,
			m.conversationAndGetRecvID(row, req.UserID),
			constant.DeleteMsgsNotification,
			row.ConversationType,
			tips,
		)
	} else {
		if err := m.MsgDatabase.DeleteUserMsgsBySeqs(ctx, req.UserID, req.ConversationID, req.Seqs); err != nil {
			return nil, err
		}
		if isSyncSelf {
			tips := &sdkws.DeleteMsgsTips{UserID: req.UserID, ConversationID: req.ConversationID, Seqs: req.Seqs}
			m.notificationSender.NotificationWithSesstionType(ctx, req.UserID, req.UserID, constant.DeleteMsgsNotification, constant.SingleChatType, tips)
		}
	}
	return &msg.DeleteMsgsResp{}, nil
}

func (m *msgServer) DeleteMsgPhysicalBySeq(
	ctx context.Context,
	req *msg.DeleteMsgPhysicalBySeqReq,
) (*msg.DeleteMsgPhysicalBySeqResp, error) {
	if err := authverify.CheckAdmin(ctx); err != nil {
		return nil, err
	}
	err := m.MsgDatabase.DeleteMsgsPhysicalBySeqs(ctx, req.ConversationID, req.Seqs)
	if err != nil {
		return nil, err
	}
	return &msg.DeleteMsgPhysicalBySeqResp{}, nil
}

func (m *msgServer) DeleteMsgPhysical(
	ctx context.Context,
	req *msg.DeleteMsgPhysicalReq,
) (*msg.DeleteMsgPhysicalResp, error) {
	if err := authverify.CheckAdmin(ctx); err != nil {
		return nil, err
	}
	remainTime := utils.GetCurrentTimestampBySecond() - req.Timestamp
	for _, conversationID := range req.ConversationIDs {
		if err := m.MsgDatabase.DeleteConversationMsgsAndSetMinSeq(ctx, conversationID, remainTime); err != nil {
			log.ZWarn(
				ctx,
				"DeleteConversationMsgsAndSetMinSeq error",
				err,
				"conversationID",
				conversationID,
				"err",
				err,
			)
		}
	}
	return &msg.DeleteMsgPhysicalResp{}, nil
}

func (m *msgServer) clearConversation(
	ctx context.Context,
	conversationIDs []string,
	userID string,
	deleteSyncOpt *msg.DeleteSyncOpt,
) error {
	defer log.ZDebug(ctx, "clearConversation return line")
	if len(conversationIDs) == 0 {
		return nil
	}
	isSyncSelf, isSyncOther := m.validateDeleteSyncOpt(deleteSyncOpt)
	conversations := make([]*conversation.Conversation, 0, len(conversationIDs))
	for _, id := range conversationIDs {
		row, err := m.ownedMessageConversation(ctx, userID, id)
		if err != nil {
			return err
		}
		if isSyncOther {
			if err := m.mayClearSharedConversation(ctx, userID, row); err != nil {
				return err
			}
		}
		conversations = append(conversations, row)
	}
	var existConversations []*conversation.Conversation
	var existConversationIDs []string
	for _, conversation := range conversations {
		existConversations = append(existConversations, conversation)
		existConversationIDs = append(existConversationIDs, conversation.ConversationID)
	}
	log.ZDebug(ctx, "ClearConversationsMsg", "existConversationIDs", existConversationIDs)
	maxSeqs, err := m.MsgDatabase.GetMaxSeqs(ctx, existConversationIDs)
	if err != nil {
		return err
	}
	if !isSyncOther {
		if err := m.MsgDatabase.SetUserConversationsMinSeqs(ctx, userID, m.getMinSeqs(maxSeqs)); err != nil {
			return err
		}
		// notification 2 self
		if isSyncSelf {
			tips := &sdkws.ClearConversationTips{UserID: userID, ConversationIDs: existConversationIDs}
			m.notificationSender.NotificationWithSesstionType(
				ctx,
				userID,
				userID,
				constant.ClearConversationNotification,
				constant.SingleChatType,
				tips,
			)
		}
	} else {
		if err := m.MsgDatabase.SetMinSeqs(ctx, m.getMinSeqs(maxSeqs)); err != nil {
			return err
		}
		for _, conversation := range existConversations {
			tips := &sdkws.ClearConversationTips{UserID: userID, ConversationIDs: []string{conversation.ConversationID}}
			m.notificationSender.NotificationWithSesstionType(ctx, userID, m.conversationAndGetRecvID(conversation, userID), constant.ClearConversationNotification, conversation.ConversationType, tips)
		}
	}
	if err := m.MsgDatabase.UserSetHasReadSeqs(ctx, userID, maxSeqs); err != nil {
		return err
	}
	return nil
}
