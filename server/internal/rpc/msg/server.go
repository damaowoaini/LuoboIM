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

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/conversation"
	"github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/tools/discoveryregistry"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/localcache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/sensitiveword"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
)

type (
	MessageInterceptorChain []MessageInterceptorFunc
	msgServer               struct {
		RegisterCenter         discoveryregistry.SvcDiscoveryRegistry
		MsgDatabase            controller.CommonMsgDatabase
		MsgGroupReadDatabase   controller.MsgGroupReadDatabase
		Group                  *rpcclient.GroupRpcClient
		User                   *rpcclient.UserRpcClient
		Conversation           *rpcclient.ConversationRpcClient
		friend                 *rpcclient.FriendRpcClient
		GroupLocalCache        *localcache.GroupLocalCache
		ConversationLocalCache *localcache.ConversationLocalCache
		Handlers               MessageInterceptorChain
		notificationSender     *rpcclient.NotificationSender
		rdb                    redis.UniversalClient               // ✅ 添加Redis客户端，用于限流
		sensitiveWordChecker   *sensitiveword.SensitiveWordChecker // 敏感词检查器
	}
)

func (m *msgServer) addInterceptorHandler(interceptorFunc ...MessageInterceptorFunc) {
	m.Handlers = append(m.Handlers, interceptorFunc...)
}

func (m *msgServer) execInterceptorHandler(ctx context.Context, req *msg.SendMsgReq) error {
	for _, handler := range m.Handlers {
		msgData, err := handler(ctx, req)
		if err != nil {
			return err
		}
		req.MsgData = msgData
	}
	return nil
}

func Start(client discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) error {
	rdb, err := cache.NewRedis()
	if err != nil {
		return err
	}
	mongo, err := unrelation.NewMongo()
	if err != nil {
		return err
	}
	if err := mongo.CreateMsgIndex(); err != nil {
		return err
	}
	msgGroupRead, err := controller.NewPersistentMsgGroupReadDatabase(context.Background(), mongo.GetDatabase())
	if err != nil {
		return err
	}
	cacheModel := cache.NewMsgCacheModel(rdb)
	msgDocModel := unrelation.NewMsgMongoDriver(mongo.GetDatabase())
	conversationClient := rpcclient.NewConversationRpcClient(client)
	userRpcClient := rpcclient.NewUserRpcClient(client)
	groupRpcClient := rpcclient.NewGroupRpcClient(client)
	friendRpcClient := rpcclient.NewFriendRpcClient(client)
	msgDatabase := controller.NewCommonMsgDatabase(msgDocModel, cacheModel)

	// 初始化敏感词检查器
	sensitiveWordDB, err := mgo.NewSensitiveWordMongo(mongo.GetDatabase())
	if err != nil {
		return err
	}
	sensitiveWordChecker := sensitiveword.NewSensitiveWordChecker(sensitiveWordDB, rdb)

	s := &msgServer{
		Conversation:           &conversationClient,
		User:                   &userRpcClient,
		Group:                  &groupRpcClient,
		MsgDatabase:            msgDatabase,
		MsgGroupReadDatabase:   msgGroupRead,
		RegisterCenter:         client,
		GroupLocalCache:        localcache.NewGroupLocalCache(&groupRpcClient),
		ConversationLocalCache: localcache.NewConversationLocalCache(&conversationClient),
		friend:                 &friendRpcClient,
		rdb:                    rdb,                  // ✅ 保存Redis客户端
		sensitiveWordChecker:   sensitiveWordChecker, // 敏感词检查器
	}
	s.notificationSender = rpcclient.NewNotificationSender(rpcclient.WithLocalSendMsg(s.SendMsg))
	s.addInterceptorHandler(MessageHasReadEnabled)
	msg.RegisterMsgServer(server, s)
	return nil
}

func (m *msgServer) conversationAndGetRecvID(conversation *conversation.Conversation, userID string) (recvID string) {
	if conversation.ConversationType == constant.SingleChatType ||
		conversation.ConversationType == constant.NotificationChatType {
		if userID == conversation.OwnerUserID {
			recvID = conversation.UserID
		} else {
			recvID = conversation.OwnerUserID
		}
	} else if conversation.ConversationType == constant.SuperGroupChatType {
		recvID = conversation.GroupID
	}
	return
}
