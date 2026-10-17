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

package conversation

import (
	"context"
	"errors"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/retention"
	"github.com/openimsdk/open-im-server/v3/pkg/common/startrpc"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"sort"

	"github.com/OpenIMSDK/protocol/sdkws"
	pbuser "github.com/OpenIMSDK/protocol/user"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mongotx"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"

	"google.golang.org/grpc"

	"github.com/OpenIMSDK/protocol/constant"
	pbconversation "github.com/OpenIMSDK/protocol/conversation"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/OpenIMSDK/tools/utils"

	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/convert"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	tablerelation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient/notification"
)

type conversationServer struct {
	msgRpcClient                   *rpcclient.MessageRpcClient
	user                           *rpcclient.UserRpcClient
	groupRpcClient                 *rpcclient.GroupRpcClient
	conversationDatabase           controller.ConversationDatabase
	conversationNotificationSender *notification.ConversationNotificationSender
}

func Start(client discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) error {
	rdb, err := cache.NewProcessRuntimeRedis(startrpc.OwnedRPCContext(server))
	if err != nil {
		return err
	}
	if err := startrpc.OwnRPCResource(server, "redis", func(context.Context) error { return rdb.Close() }); err != nil {
		_ = rdb.Close()
		return err
	}

	mongo, err := unrelation.NewProcessRuntimeMongo(startrpc.OwnedRPCContext(server))
	if mongo != nil {
		if trackErr := startrpc.OwnRPCDatabase(server, mongo.GetDatabase().Name()); trackErr != nil {
			_ = mongo.GetClient().Disconnect(context.Background())
			return trackErr
		}
		if trackErr := startrpc.OwnRPCResource(server, "mongo", mongo.GetClient().Disconnect); trackErr != nil {
			_ = mongo.GetClient().Disconnect(context.Background())
			return trackErr
		}
	}
	if err != nil {
		return err
	}

	readyContext := context.Background()
	if startrpc.IsOwnedRPC(server) {
		readyContext = startrpc.OwnedRPCContext(server)
	}
	readerPath, readerSHA, err := startrpc.OwnedRPCHistoryPreparation(server)
	if err != nil {
		return err
	}
	var history *messagedelivery.HistoryWindowReader
	var readerStartup *messagedelivery.HistoryReaderStartupBinding
	if readerPath != "" {
		history, readerStartup, err = messagedelivery.NewStartupHistoryWindowReader(readyContext, mongo.GetDatabase(), "rpc-conversation", readerPath, readerSHA)
		if err != nil {
			return err
		}
	}
	namespaceRuntime, err := messagedelivery.NewNamespaceRuntimeBindingForOwnedContext(readyContext, mongo.GetDatabase())
	if err != nil {
		return err
	}
	if namespaceRuntime != nil {
		if history == nil || readerStartup == nil {
			return messagedelivery.ErrUnprepared
		}
		if err = namespaceRuntime.BindOriginalReader(history, "conversation-history"); err != nil {
			return err
		}
		if err = startrpc.OwnRPCNamespaceRuntimeBinding(server, namespaceRuntime); err != nil {
			return err
		}
		readyContext = namespaceRuntime.BindRuntimeContext(readyContext)
	}
	conversationDB, err := mgo.NewConversationMongo(mongo.GetDatabase())
	if err != nil {
		return err
	}
	if readerPath == "" {
		history, readerStartup, err = messagedelivery.NewStartupHistoryWindowReader(readyContext, mongo.GetDatabase(), "rpc-conversation", "", "")
		if err != nil {
			return err
		}
	}
	var conversations controller.ConversationDatabase
	if history == nil {
		conversations = controller.NewConversationDatabase(conversationDB, cache.NewConversationRedis(rdb, cache.GetDefaultOpt(), conversationDB), mongotx.New(mongo.GetClient()))
	} else {
		conversations, err = controller.NewConversationDatabaseWithNamespaceHistory(conversationDB, cache.NewConversationRedis(rdb, cache.GetDefaultOpt(), conversationDB), mongotx.New(mongo.GetClient()), history)
		if err != nil {
			return err
		}
	}
	groupRpcClient := rpcclient.NewGroupRpcClient(client)
	msgRpcClient := rpcclient.NewMessageRpcClient(client)
	userRpcClient := rpcclient.NewUserRpcClient(client)
	pbconversation.RegisterConversationServer(server, &conversationServer{
		msgRpcClient:                   &msgRpcClient,
		user:                           &userRpcClient,
		conversationNotificationSender: notification.NewConversationNotificationSender(&msgRpcClient),
		groupRpcClient:                 &groupRpcClient,
		conversationDatabase:           conversations,
	})
	if readerStartup != nil {
		if err = startrpc.OwnRPCHistoryReader(server, readerStartup); err != nil {
			return err
		}
		if err = startrpc.OwnRPCPreparedWriterFinalCheck(server, readerStartup.AwaitFinalRuntimeReady); err != nil {
			return err
		}
	}
	return nil
}

func (c *conversationServer) GetConversation(ctx context.Context, req *pbconversation.GetConversationReq) (*pbconversation.GetConversationResp, error) {
	opUserID := mcontext.GetOpUserID(ctx)
	if req.OwnerUserID == "" {
		req.OwnerUserID = opUserID
	}
	if err := authverify.CheckAccessV3(ctx, req.OwnerUserID); err != nil {
		return nil, err
	}
	conversations, err := c.conversationDatabase.FindConversations(ctx, req.OwnerUserID, []string{req.ConversationID})
	if err != nil {
		return nil, err
	}
	if len(conversations) < 1 {
		return nil, errs.ErrRecordNotFound.Wrap("conversation not found")
	}
	resp := &pbconversation.GetConversationResp{Conversation: &pbconversation.Conversation{}}
	resp.Conversation = convert.ConversationDB2Pb(conversations[0])
	return resp, nil
}

func (m *conversationServer) GetConversationList(ctx context.Context, req *pbconversation.GetConversationListReq) (resp *pbconversation.GetConversationListResp, err error) {
	opUserID := mcontext.GetOpUserID(ctx)
	if opUserID == "" {
		return nil, errs.ErrNoPermission.Wrap("opUserID is empty")
	}
	if req.UserID == "" {
		req.UserID = opUserID
	}
	if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	log.ZDebug(ctx, "GetConversationList", "seqs", req, "userID", req.UserID)
	var conversationIDs []string
	if len(req.ConversationIDs) == 0 {
		conversationIDs, err = m.conversationDatabase.GetConversationIDs(ctx, req.UserID)
		if err != nil {
			return nil, err
		}
	} else {
		conversationIDs = req.ConversationIDs
	}

	conversations, err := m.conversationDatabase.FindConversations(ctx, req.UserID, conversationIDs)
	if err != nil {
		return nil, err
	}
	if len(conversations) == 0 {
		return nil, errs.ErrRecordNotFound.Wrap()
	}

	maxSeqs, err := m.msgRpcClient.GetMaxSeqs(ctx, conversationIDs)
	if err != nil {
		return nil, err
	}

	// req.UserID was checked against the actual actor above. Summary visibility
	// must use this owner even when an authorized administrator reads it.
	summaryCtx := context.WithValue(ctx, constant.OpUserID, req.UserID)
	chatLogs, err := m.msgRpcClient.GetMsgByConversationIDs(summaryCtx, conversationIDs, maxSeqs)
	if err != nil {
		return nil, err
	}

	conversationMsg, err := m.getConversationInfo(ctx, chatLogs, req.UserID)
	if err != nil {
		return nil, err
	}

	hasReadSeqs, err := m.msgRpcClient.GetHasReadSeqs(ctx, req.UserID, conversationIDs)
	if err != nil {
		return nil, err
	}

	conversation_unreadCount := make(map[string]int64)
	for conversationID, maxSeq := range maxSeqs {
		conversation_unreadCount[conversationID] = maxSeq - hasReadSeqs[conversationID]
	}

	resp = &pbconversation.GetConversationListResp{ConversationElems: make([]*pbconversation.ConversationElem, 0, len(conversations))}
	for _, row := range conversations {
		element := conversationMsg[row.ConversationID]
		if element == nil {
			element = &pbconversation.ConversationElem{ConversationID: row.ConversationID, MsgInfo: &pbconversation.MsgInfo{}}
		}
		element.RecvMsgOpt = row.RecvMsgOpt
		element.IsPinned = row.IsPinned
		element.UnreadCount = max(conversation_unreadCount[row.ConversationID], 0)
		resp.ConversationElems = append(resp.ConversationElems, element)
	}
	sort.SliceStable(resp.ConversationElems, func(i, j int) bool {
		a, b := resp.ConversationElems[i], resp.ConversationElems[j]
		if a.IsPinned != b.IsPinned {
			return a.IsPinned
		}
		if a.GetMsgInfo().GetLatestMsgRecvTime() != b.GetMsgInfo().GetLatestMsgRecvTime() {
			return a.GetMsgInfo().GetLatestMsgRecvTime() > b.GetMsgInfo().GetLatestMsgRecvTime()
		}
		return a.ConversationID < b.ConversationID
	})
	return resp, nil
}

func (c *conversationServer) GetAllConversations(ctx context.Context, req *pbconversation.GetAllConversationsReq) (*pbconversation.GetAllConversationsResp, error) {
	opUserID := mcontext.GetOpUserID(ctx)
	if opUserID == "" {
		return nil, errs.ErrNoPermission.Wrap("opUserID is empty")
	}
	if req.OwnerUserID == "" {
		req.OwnerUserID = opUserID
	}
	if err := authverify.CheckAccessV3(ctx, req.OwnerUserID); err != nil {
		return nil, err
	}
	conversations, err := c.conversationDatabase.GetUserAllConversation(ctx, req.OwnerUserID)
	if err != nil {
		return nil, err
	}
	resp := &pbconversation.GetAllConversationsResp{Conversations: []*pbconversation.Conversation{}}
	resp.Conversations = convert.ConversationsDB2Pb(conversations)
	return resp, nil
}

func (c *conversationServer) GetConversations(ctx context.Context, req *pbconversation.GetConversationsReq) (*pbconversation.GetConversationsResp, error) {
	opUserID := mcontext.GetOpUserID(ctx)
	if req.OwnerUserID == "" {
		req.OwnerUserID = opUserID
	}
	if opUserID != "" {
		if err := authverify.CheckAccessV3(ctx, req.OwnerUserID); err != nil {
			return nil, err
		}
	}
	conversations, err := c.conversationDatabase.FindConversations(ctx, req.OwnerUserID, req.ConversationIDs)
	if err != nil {
		return nil, err
	}
	resp := &pbconversation.GetConversationsResp{Conversations: []*pbconversation.Conversation{}}
	resp.Conversations = convert.ConversationsDB2Pb(conversations)
	return resp, nil
}

func (c *conversationServer) SetConversation(ctx context.Context, req *pbconversation.SetConversationReq) (*pbconversation.SetConversationResp, error) {
	if req == nil || req.Conversation == nil {
		return nil, errs.ErrArgs.Wrap("conversation is empty")
	}
	if err := authverify.CheckAccessV3(ctx, req.Conversation.OwnerUserID); err != nil {
		return nil, err
	}
	if err := c.validateConversationIdentity(ctx, req.Conversation); err != nil {
		return nil, err
	}
	var conversation tablerelation.ConversationModel
	if err := utils.CopyStructFields(&conversation, req.Conversation); err != nil {
		return nil, err
	}
	err := c.conversationDatabase.SetUserConversations(ctx, req.Conversation.OwnerUserID, []*tablerelation.ConversationModel{&conversation})
	if err != nil {
		return nil, err
	}
	if err := c.conversationNotificationSender.ConversationChangeNotification(ctx, req.Conversation.OwnerUserID, []string{req.Conversation.ConversationID}); err != nil {
		return nil, retention.ErrSyncPending // The preference has already committed; never claim rollback.
	}
	resp := &pbconversation.SetConversationResp{}
	return resp, nil
}

// nolint
func (c *conversationServer) SetConversations(ctx context.Context, req *pbconversation.SetConversationsReq) (*pbconversation.SetConversationsResp, error) {
	if req == nil || req.Conversation == nil {
		return nil, errs.ErrArgs.Wrap("conversation required")
	}
	v := req.Conversation
	if v.IsMsgDestruct == nil && v.MsgDestructTime == nil && v.IsPrivateChat == nil && v.BurnDuration == nil {
		return c.setConversations(ctx, req)
	}
	provider, ok := c.conversationDatabase.(interface {
		RetentionTransaction(context.Context, func(context.Context) error) error
	})
	if !ok {
		return nil, retention.ErrUnavailable
	}
	var response *pbconversation.SetConversationsResp
	err := provider.RetentionTransaction(ctx, func(current context.Context) error {
		var inner error
		response, inner = c.setConversations(current, req)
		return inner
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (c *conversationServer) setConversations(ctx context.Context,
	req *pbconversation.SetConversationsReq,
) (*pbconversation.SetConversationsResp, error) {
	if req.Conversation == nil {
		return nil, errs.ErrArgs.Wrap("conversation must not be nil")
	}
	for _, userID := range req.UserIDs {
		if err := authverify.CheckAccessV3(ctx, userID); err != nil {
			return nil, err
		}
	}
	if err := c.validateLifecycleRequest(ctx, req); err != nil {
		return nil, err
	}
	if req.Conversation.ConversationType == constant.GroupChatType {
		groupInfo, err := c.groupRpcClient.GetGroupInfo(ctx, req.Conversation.GroupID)
		if err != nil {
			return nil, err
		}
		if groupInfo.Status == constant.GroupStatusDismissed {
			return nil, errs.ErrDismissedAlready.Wrap("group dismissed")
		}
	}
	var unequal int
	var conv tablerelation.ConversationModel
	if len(req.UserIDs) == 1 {
		cs, err := c.conversationDatabase.FindConversations(ctx, req.UserIDs[0], []string{req.Conversation.ConversationID})
		if err != nil {
			return nil, err
		}
		if len(cs) == 0 {
			return nil, errs.ErrRecordNotFound.Wrap("conversation not found")
		}
		conv = *cs[0]
	}
	var conversation tablerelation.ConversationModel
	conversation.ConversationID = req.Conversation.ConversationID
	conversation.ConversationType = req.Conversation.ConversationType
	conversation.UserID = req.Conversation.UserID
	conversation.GroupID = req.Conversation.GroupID
	m := make(map[string]any)
	if req.Conversation.RecvMsgOpt != nil {
		m["recv_msg_opt"] = req.Conversation.RecvMsgOpt.Value
		if req.Conversation.RecvMsgOpt.Value != conv.RecvMsgOpt {
			unequal++
		}
	}
	if req.Conversation.AttachedInfo != nil {
		m["attached_info"] = req.Conversation.AttachedInfo.Value
		if req.Conversation.AttachedInfo.Value != conv.AttachedInfo {
			unequal++
		}
	}
	if req.Conversation.Ex != nil {
		m["ex"] = req.Conversation.Ex.Value
		if req.Conversation.Ex.Value != conv.Ex {
			unequal++
		}
	}
	if req.Conversation.IsPinned != nil {
		m["is_pinned"] = req.Conversation.IsPinned.Value
		if req.Conversation.IsPinned.Value != conv.IsPinned {
			unequal++
		}
	}
	if req.Conversation.GroupAtType != nil {
		m["group_at_type"] = req.Conversation.GroupAtType.Value
		if req.Conversation.GroupAtType.Value != conv.GroupAtType {
			unequal++
		}
	}
	if req.Conversation.MsgDestructTime != nil {
		m["msg_destruct_time"] = req.Conversation.MsgDestructTime.Value
		if req.Conversation.MsgDestructTime.Value != conv.MsgDestructTime {
			unequal++
		}
	}
	if req.Conversation.IsMsgDestruct != nil {
		m["is_msg_destruct"] = req.Conversation.IsMsgDestruct.Value
		if req.Conversation.IsMsgDestruct.Value != conv.IsMsgDestruct {
			unequal++
		}
	}
	if req.Conversation.IsPrivateChat != nil && req.Conversation.ConversationType != constant.SuperGroupChatType {
		var conversations []*tablerelation.ConversationModel
		for _, ownerUserID := range req.UserIDs {
			conversation2 := conversation
			conversation2.OwnerUserID = ownerUserID
			conversation2.IsPrivateChat = req.Conversation.IsPrivateChat.Value
			conversations = append(conversations, &conversation2)
		}
		if err := c.conversationDatabase.SyncPeerUserPrivateConversationTx(ctx, conversations); err != nil {
			return nil, err
		}
		for _, userID := range req.UserIDs {
			owner, peer, private, cid := userID, req.Conversation.UserID, req.Conversation.IsPrivateChat.Value, req.Conversation.ConversationID
			err := retention.AfterCommit(ctx, func(after context.Context) error {
				return c.conversationNotificationSender.ConversationSetPrivateNotification(after, owner, peer, private, cid)
			})
			if err != nil {
				log.ZWarn(ctx, "send conversation set private notification failed", err,
					"userID", userID, "conversationID", req.Conversation.ConversationID)

				continue
			}
		}
	}
	if req.Conversation.BurnDuration != nil {
		m["burn_duration"] = req.Conversation.BurnDuration.Value
		if req.Conversation.BurnDuration.Value != conv.BurnDuration {
			unequal++
		}
	}
	if err := c.conversationDatabase.SetUsersConversationFiledTx(ctx, req.UserIDs, &conversation, m); err != nil {
		return nil, err
	}
	if unequal > 0 {
		for _, v := range req.UserIDs {
			owner, cid := v, req.Conversation.ConversationID
			if err := retention.AfterCommit(ctx, func(after context.Context) error {
				return c.conversationNotificationSender.ConversationChangeNotification(after, owner, []string{cid})
			}); err != nil {
				return nil, err
			}
		}
	}
	return &pbconversation.SetConversationsResp{}, nil
}

// 获取超级大群开启免打扰的用户ID.
func (c *conversationServer) GetRecvMsgNotNotifyUserIDs(ctx context.Context, req *pbconversation.GetRecvMsgNotNotifyUserIDsReq) (*pbconversation.GetRecvMsgNotNotifyUserIDsResp, error) {
	//userIDs, err := c.conversationDatabase.FindRecvMsgNotNotifyUserIDs(ctx, req.GroupID)
	//if err != nil {
	//	return nil, err
	//}
	//return &pbconversation.GetRecvMsgNotNotifyUserIDsResp{UserIDs: userIDs}, nil
	return nil, errors.New("deprecated")
}

// create conversation without notification for msg redis transfer.
func (c *conversationServer) CreateSingleChatConversations(ctx context.Context,
	req *pbconversation.CreateSingleChatConversationsReq,
) (*pbconversation.CreateSingleChatConversationsResp, error) {
	switch req.ConversationType {
	case constant.SingleChatType:
		var conversation tablerelation.ConversationModel
		conversation.ConversationID = req.ConversationID
		conversation.ConversationType = req.ConversationType
		conversation.OwnerUserID = req.SendID
		conversation.UserID = req.RecvID
		err := c.conversationDatabase.CreateConversation(ctx, []*tablerelation.ConversationModel{&conversation})
		if err != nil {
			log.ZWarn(ctx, "create conversation failed", err, "conversation", conversation)
		}

		conversation2 := conversation
		conversation2.OwnerUserID = req.RecvID
		conversation2.UserID = req.SendID
		err = c.conversationDatabase.CreateConversation(ctx, []*tablerelation.ConversationModel{&conversation2})
		if err != nil {
			log.ZWarn(ctx, "create conversation failed", err, "conversation2", conversation)
		}
	case constant.NotificationChatType:
		var conversation tablerelation.ConversationModel
		conversation.ConversationID = req.ConversationID
		conversation.ConversationType = req.ConversationType
		conversation.OwnerUserID = req.RecvID
		conversation.UserID = req.SendID
		err := c.conversationDatabase.CreateConversation(ctx, []*tablerelation.ConversationModel{&conversation})
		if err != nil {
			log.ZWarn(ctx, "create conversation failed", err, "conversation2", conversation)
		}
	}

	return &pbconversation.CreateSingleChatConversationsResp{}, nil
}

func (c *conversationServer) CreateGroupChatConversations(ctx context.Context, req *pbconversation.CreateGroupChatConversationsReq) (*pbconversation.CreateGroupChatConversationsResp, error) {
	err := c.conversationDatabase.CreateGroupChatConversation(ctx, req.GroupID, req.UserIDs)
	if err != nil {
		return nil, err
	}
	return &pbconversation.CreateGroupChatConversationsResp{}, nil
}

func (c *conversationServer) SetConversationMaxSeq(ctx context.Context, req *pbconversation.SetConversationMaxSeqReq) (*pbconversation.SetConversationMaxSeqResp, error) {
	if err := c.conversationDatabase.UpdateUsersConversationFiled(ctx, req.OwnerUserID, req.ConversationID,
		map[string]any{"max_seq": req.MaxSeq}); err != nil {
		return nil, err
	}
	return &pbconversation.SetConversationMaxSeqResp{}, nil
}

func (c *conversationServer) GetConversationIDs(ctx context.Context, req *pbconversation.GetConversationIDsReq) (*pbconversation.GetConversationIDsResp, error) {
	if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	conversationIDs, err := c.conversationDatabase.GetConversationIDs(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	return &pbconversation.GetConversationIDsResp{ConversationIDs: conversationIDs}, nil
}

func (c *conversationServer) GetUserConversationIDsHash(ctx context.Context, req *pbconversation.GetUserConversationIDsHashReq) (*pbconversation.GetUserConversationIDsHashResp, error) {
	if err := authverify.CheckAccessV3(ctx, req.OwnerUserID); err != nil {
		return nil, err
	}
	hash, err := c.conversationDatabase.GetUserConversationIDsHash(ctx, req.OwnerUserID)
	if err != nil {
		return nil, err
	}
	return &pbconversation.GetUserConversationIDsHashResp{Hash: hash}, nil
}

func (c *conversationServer) GetConversationsByConversationID(
	ctx context.Context,
	req *pbconversation.GetConversationsByConversationIDReq,
) (*pbconversation.GetConversationsByConversationIDResp, error) {
	conversations, err := c.conversationDatabase.GetConversationsByConversationID(ctx, req.ConversationIDs)
	if err != nil {
		return nil, err
	}
	opUserID := mcontext.GetOpUserID(ctx)
	if opUserID != "" && !authverify.IsAppManagerUid(ctx) {
		own := conversations[:0]
		for _, conv := range conversations {
			if conv.OwnerUserID == opUserID {
				own = append(own, conv)
			}
		}
		conversations = own
	}
	return &pbconversation.GetConversationsByConversationIDResp{Conversations: convert.ConversationsDB2Pb(conversations)}, nil
}

func (c *conversationServer) GetConversationOfflinePushUserIDs(
	ctx context.Context,
	req *pbconversation.GetConversationOfflinePushUserIDsReq,
) (*pbconversation.GetConversationOfflinePushUserIDsResp, error) {
	if req.ConversationID == "" {
		return nil, errs.ErrArgs.Wrap("conversationID is empty")
	}
	if len(req.UserIDs) == 0 {
		return &pbconversation.GetConversationOfflinePushUserIDsResp{}, nil
	}
	userIDs, err := c.conversationDatabase.GetConversationNotReceiveMessageUserIDs(ctx, req.ConversationID)
	if err != nil {
		return nil, err
	}
	if len(userIDs) == 0 {
		return &pbconversation.GetConversationOfflinePushUserIDsResp{UserIDs: req.UserIDs}, nil
	}
	userIDSet := make(map[string]struct{})
	for _, userID := range req.UserIDs {
		userIDSet[userID] = struct{}{}
	}
	for _, userID := range userIDs {
		delete(userIDSet, userID)
	}
	return &pbconversation.GetConversationOfflinePushUserIDsResp{UserIDs: utils.Keys(userIDSet)}, nil
}

func (c *conversationServer) conversationSort(
	conversations map[int64]string,
	resp *pbconversation.GetConversationListResp,
	conversation_unreadCount map[string]int64,
	conversationMsg map[string]*pbconversation.ConversationElem,
) {
	keys := []int64{}
	for key := range conversations {
		keys = append(keys, key)
	}

	sort.Slice(keys[:], func(i, j int) bool {
		return keys[i] > keys[j]
	})
	index := 0

	cons := make([]*pbconversation.ConversationElem, len(conversations))
	for _, v := range keys {
		conversationID := conversations[v]
		conversationElem := conversationMsg[conversationID]
		conversationElem.UnreadCount = conversation_unreadCount[conversationID]
		cons[index] = conversationElem
		index++
	}
	resp.ConversationElems = append(resp.ConversationElems, cons...)
}

func (c *conversationServer) getConversationInfo(
	ctx context.Context,
	chatLogs map[string]*sdkws.MsgData,
	userID string) (map[string]*pbconversation.ConversationElem, error) {
	var (
		sendIDs         []string
		groupIDs        []string
		sendMap         = make(map[string]*sdkws.UserInfo)
		groupMap        = make(map[string]*sdkws.GroupInfo)
		conversationMsg = make(map[string]*pbconversation.ConversationElem)
	)
	for _, chatLog := range chatLogs {
		if chatLog == nil {
			continue
		}
		switch chatLog.SessionType {
		case constant.SingleChatType:
			if chatLog.SendID == userID {
				sendIDs = append(sendIDs, chatLog.RecvID)
			}
			sendIDs = append(sendIDs, chatLog.SendID)
		case constant.GroupChatType, constant.SuperGroupChatType:
			groupIDs = append(groupIDs, chatLog.GroupID)
			sendIDs = append(sendIDs, chatLog.SendID)
		}
	}
	if len(sendIDs) != 0 {
		for _, id := range sendIDs {
			sendMap[id] = &sdkws.UserInfo{UserID: id, Nickname: id}
		}
		sendInfos, err := c.user.Client.GetDesignateUsers(ctx, &pbuser.GetDesignateUsersReq{UserIDs: utils.Distinct(sendIDs)})
		if err != nil {
			return nil, err
		}
		if sendInfos == nil {
			return nil, errs.ErrInternalServer.Wrap("user metadata response missing")
		}
		for _, sendInfo := range sendInfos.UsersInfo {
			sendMap[sendInfo.UserID] = sendInfo
		}
	}
	if len(groupIDs) != 0 {
		for _, id := range groupIDs {
			groupMap[id] = &sdkws.GroupInfo{GroupID: id, GroupName: id}
		}
		groupInfos, err := c.groupRpcClient.GetGroupInfos(ctx, groupIDs, false)
		if err != nil {
			return nil, err
		}
		for _, groupInfo := range groupInfos {
			groupMap[groupInfo.GroupID] = groupInfo
		}
	}
	for conversationID, chatLog := range chatLogs {
		if chatLog == nil {
			continue
		}
		pbchatLog := &pbconversation.ConversationElem{}
		msgInfo := &pbconversation.MsgInfo{}
		if err := utils.CopyStructFields(msgInfo, chatLog); err != nil {
			return nil, err
		}
		// MsgInfo has no keyVersion field and its content is a protobuf string.
		// Sending opaque CBC bytes here corrupts UTF-8; full messages retain their
		// original bytes/version through the ordinary history API.
		if chatLog.KeyVersion > 0 {
			msgInfo.Content = ""
		}
		switch chatLog.SessionType {
		case constant.SingleChatType:
			if chatLog.SendID == userID {
				msgInfo.FaceURL = sendMap[chatLog.RecvID].FaceURL
				msgInfo.SenderName = sendMap[chatLog.RecvID].Nickname
				break
			}
			msgInfo.FaceURL = sendMap[chatLog.SendID].FaceURL
			msgInfo.SenderName = sendMap[chatLog.SendID].Nickname
		case constant.GroupChatType, constant.SuperGroupChatType:
			msgInfo.GroupName = groupMap[chatLog.GroupID].GroupName
			msgInfo.GroupFaceURL = groupMap[chatLog.GroupID].FaceURL
			msgInfo.GroupMemberCount = groupMap[chatLog.GroupID].MemberCount
			msgInfo.GroupID = chatLog.GroupID
			msgInfo.GroupType = groupMap[chatLog.GroupID].GroupType
			msgInfo.SenderName = sendMap[chatLog.SendID].Nickname
		}
		pbchatLog.ConversationID = conversationID
		msgInfo.LatestMsgRecvTime = chatLog.SendTime
		pbchatLog.MsgInfo = msgInfo
		conversationMsg[conversationID] = pbchatLog
	}
	return conversationMsg, nil
}

func (c *conversationServer) validateConversationIdentity(ctx context.Context, conv *pbconversation.Conversation) error {
	expected := ""
	switch conv.ConversationType {
	case constant.SingleChatType:
		expected = msgprocessor.GetConversationIDBySessionType(constant.SingleChatType, conv.OwnerUserID, conv.UserID)
	case constant.GroupChatType, constant.SuperGroupChatType:
		expected = msgprocessor.GetConversationIDBySessionType(int(conv.ConversationType), conv.GroupID)
	case constant.NotificationChatType:
		expected = msgprocessor.GetConversationIDBySessionType(constant.NotificationChatType, conv.OwnerUserID)
		if conv.UserID != "" && conv.ConversationID == "sn_"+conv.UserID+"_"+conv.OwnerUserID {
			expected = conv.ConversationID
		}
	default:
		return errs.ErrArgs.Wrap("invalid conversation type")
	}
	if expected == "" || expected != conv.ConversationID {
		return errs.ErrArgs.Wrap("conversationID does not match participants")
	}
	if conv.GroupID != "" && !authverify.IsAppManagerUid(ctx) {
		existing, err := c.conversationDatabase.FindConversations(ctx, conv.OwnerUserID, []string{conv.ConversationID})
		if err != nil {
			return err
		}
		if len(existing) == 0 {
			if _, err := c.groupRpcClient.GetGroupMemberInfos(ctx, conv.GroupID, []string{conv.OwnerUserID}, true); err != nil {
				return err
			}
		}
	}
	return nil
}
