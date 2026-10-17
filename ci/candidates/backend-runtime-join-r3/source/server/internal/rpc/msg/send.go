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
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"

	"github.com/openimsdk/open-im-server/v3/pkg/common/prommetrics"
	"github.com/openimsdk/open-im-server/v3/pkg/common/riskcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"

	"github.com/OpenIMSDK/protocol/constant"
	pbconversation "github.com/OpenIMSDK/protocol/conversation"
	pbmsg "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/protocol/wrapperspb"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/OpenIMSDK/tools/utils"
)

// Called only after the original final access/callback checks. The known
// Mongo reservation commits before MQ, and no MQ send runs in TX retries.
func (m *msgServer) enqueueNamespaced(ctx context.Context, queueKey string, message *sdkws.MsgData) error {
	if message == nil {
		return errs.ErrArgs.Wrap("msgData is nil")
	}
	if err := messagedelivery.CheckSupportedMessageContentType(message.ContentType); err != nil {
		return errs.ErrArgs.Wrap(err.Error())
	}
	if m.namespaces == nil {
		if err := messagedelivery.CheckLegacyMsgTransferHistoryMode(); err != nil {
			return err
		}
		return m.MsgDatabase.MsgToMQ(ctx, queueKey, message)
	}
	permit, err := m.namespaces.ReserveMessageNamespaces(ctx, message)
	if err != nil {
		return err
	}
	if err := permit.Check(ctx, message); err != nil {
		return err
	}
	return m.MsgDatabase.MsgToMQ(ctx, queueKey, message)
}

func (m *msgServer) SendMsg(ctx context.Context, req *pbmsg.SendMsgReq) (resp *pbmsg.SendMsgResp, error error) {
	resp = &pbmsg.SendMsgResp{}
	if req.MsgData != nil {
		if err := authverify.CheckAccessV3(ctx, req.MsgData.SendID); err != nil {
			return nil, err
		}
		if req.MsgData.MsgFrom != constant.UserMsgType || req.MsgData.ContentType >= constant.NotificationBegin || req.MsgData.SessionType == constant.NotificationChatType {
			if err := authverify.CheckAdmin(ctx); err != nil {
				return nil, err
			}
		}
		if err := messagedelivery.CheckSupportedMessageContentType(req.MsgData.ContentType); err != nil {
			return nil, errs.ErrArgs.Wrap(err.Error())
		}
		flag := isMessageHasReadEnabled(req.MsgData)
		if !flag {
			return nil, errs.ErrMessageHasReadDisable.Wrap()
		}
		m.encapsulateMsgData(req.MsgData)
		switch req.MsgData.SessionType {
		case constant.SingleChatType:
			return m.sendMsgSingleChat(ctx, req)
		case constant.NotificationChatType:
			return m.sendMsgNotification(ctx, req)
		case constant.SuperGroupChatType:
			return m.sendMsgSuperGroupChat(ctx, req)
		default:
			return nil, errs.ErrArgs.Wrap("unknown sessionType")
		}
	} else {
		return nil, errs.ErrArgs.Wrap("msgData is nil")
	}
}

func (m *msgServer) sendMsgSuperGroupChat(
	ctx context.Context,
	req *pbmsg.SendMsgReq,
) (resp *pbmsg.SendMsgResp, err error) {
	err = m.withVerifiedGroupMessage(ctx, req, func(ctx context.Context, req *pbmsg.SendMsgReq) error {
		if blocked, err := riskcontrol.IsActionBlocked(ctx, riskcontrol.RiskDisableGroupMsgKey); err != nil {
			return err
		} else if blocked {
			return errs.ErrNoPermission.Wrap("当前时段禁止发送群消息")
		}
		if err := callbackBeforeSendGroupMsg(ctx, req); err != nil {
			return err
		}
		return callbackMsgModify(ctx, req)
	}, func(ctx context.Context, req *pbmsg.SendMsgReq) error {
		return m.enqueueNamespaced(ctx, utils.GenConversationUniqueKeyForGroup(req.MsgData.GroupID), req.MsgData)
	})
	if err != nil {
		prommetrics.GroupChatMsgProcessFailedCounter.Inc()
		return nil, err
	}
	if req.MsgData.ContentType == constant.AtText {
		m.ownedWork.Go(func() { m.setConversationAtInfo(ctx, req.MsgData) })
	}
	if err = callbackAfterSendGroupMsg(ctx, req); err != nil {
		log.ZWarn(ctx, "CallbackAfterSendGroupMsg", err)
	}
	prommetrics.GroupChatMsgProcessSuccessCounter.Inc()
	resp = &pbmsg.SendMsgResp{}
	resp.SendTime = req.MsgData.SendTime
	resp.ServerMsgID = req.MsgData.ServerMsgID
	resp.ClientMsgID = req.MsgData.ClientMsgID
	return resp, nil
}

func (m *msgServer) setConversationAtInfo(nctx context.Context, msg *sdkws.MsgData) {
	log.ZDebug(nctx, "setConversationAtInfo", "msg", msg)
	ctx := mcontext.NewCtx("@@@" + mcontext.GetOperationID(nctx))
	var atUserID []string
	conversation := &pbconversation.ConversationReq{
		ConversationID:   msgprocessor.GetConversationIDByMsg(msg),
		ConversationType: msg.SessionType,
		GroupID:          msg.GroupID,
	}
	tagAll := utils.IsContain(constant.AtAllString, msg.AtUserIDList)
	if tagAll {
		memberUserIDList, err := m.Group.GetGroupMemberIDs(ctx, msg.GroupID)
		if err != nil {
			log.ZWarn(ctx, "GetGroupMemberIDs", err)
			return
		}
		atUserID = utils.DifferenceString([]string{constant.AtAllString}, msg.AtUserIDList)
		if len(atUserID) == 0 { // just @everyone
			conversation.GroupAtType = &wrapperspb.Int32Value{Value: constant.AtAll}
		} else { //@Everyone and @other people
			conversation.GroupAtType = &wrapperspb.Int32Value{Value: constant.AtAllAtMe}
			err := m.Conversation.SetConversations(ctx, atUserID, conversation)
			if err != nil {
				log.ZWarn(ctx, "SetConversations", err, "userID", atUserID, "conversation", conversation)
			}
			memberUserIDList = utils.DifferenceString(atUserID, memberUserIDList)
		}
		conversation.GroupAtType = &wrapperspb.Int32Value{Value: constant.AtAll}
		err = m.Conversation.SetConversations(ctx, memberUserIDList, conversation)
		if err != nil {
			log.ZWarn(ctx, "SetConversations", err, "userID", memberUserIDList, "conversation", conversation)
		}
	} else {
		conversation.GroupAtType = &wrapperspb.Int32Value{Value: constant.AtMe}
		err := m.Conversation.SetConversations(ctx, msg.AtUserIDList, conversation)
		if err != nil {
			log.ZWarn(ctx, "SetConversations", err, msg.AtUserIDList, conversation)
		}
	}
}

func (m *msgServer) sendMsgNotification(
	ctx context.Context,
	req *pbmsg.SendMsgReq,
) (resp *pbmsg.SendMsgResp, err error) {
	if err := m.enqueueNamespaced(ctx, utils.GenConversationUniqueKeyForSingle(req.MsgData.SendID, req.MsgData.RecvID), req.MsgData); err != nil {
		return nil, err
	}
	resp = &pbmsg.SendMsgResp{
		ServerMsgID: req.MsgData.ServerMsgID,
		ClientMsgID: req.MsgData.ClientMsgID,
		SendTime:    req.MsgData.SendTime,
	}
	return resp, nil
}

func (m *msgServer) sendMsgSingleChat(ctx context.Context, req *pbmsg.SendMsgReq) (resp *pbmsg.SendMsgResp, err error) {
	if err := m.messageVerification(ctx, req); err != nil {
		return nil, err
	}
	isSend := true
	isNotification := msgprocessor.IsNotificationByMsg(req.MsgData)
	if !isNotification {
		isSend, err = m.modifyMessageByUserMessageReceiveOpt(
			ctx,
			req.MsgData.RecvID,
			utils.GenConversationIDForSingle(req.MsgData.SendID, req.MsgData.RecvID),
			constant.SingleChatType,
			req,
		)
		if err != nil {
			return nil, err
		}
	}
	if !isSend {
		prommetrics.SingleChatMsgProcessFailedCounter.Inc()
		return nil, nil
	} else {
		if blocked, err := riskcontrol.IsActionBlocked(ctx, riskcontrol.RiskDisablePrivateMsgKey); err != nil {
			return nil, err
		} else if blocked {
			return nil, errs.ErrNoPermission.Wrap("当前时段禁止发送私聊消息")
		}
		if err = callbackBeforeSendSingleMsg(ctx, req); err != nil {
			return nil, err
		}
		if err := callbackMsgModify(ctx, req); err != nil {
			return nil, err
		}
		if err := m.enqueueNamespaced(ctx, utils.GenConversationUniqueKeyForSingle(req.MsgData.SendID, req.MsgData.RecvID), req.MsgData); err != nil {
			prommetrics.SingleChatMsgProcessFailedCounter.Inc()
			return nil, err
		}
		err = callbackAfterSendSingleMsg(ctx, req)
		if err != nil {
			log.ZWarn(ctx, "CallbackAfterSendSingleMsg", err, "req", req)
		}
		resp = &pbmsg.SendMsgResp{
			ServerMsgID: req.MsgData.ServerMsgID,
			ClientMsgID: req.MsgData.ClientMsgID,
			SendTime:    req.MsgData.SendTime,
		}
		prommetrics.SingleChatMsgProcessSuccessCounter.Inc()
		return resp, nil
	}
}

func (m *msgServer) BatchSendMsg(ctx context.Context, in *pbmsg.BatchSendMessageReq) (*pbmsg.BatchSendMessageResp, error) {
	return nil, nil
}
