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
	"errors"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/common/startrpc"
	"time"

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
		ownedWork              *startrpc.OwnedRPCWork
		namespaces             *messagedelivery.Store
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

	initContext := context.Background()
	if startrpc.IsOwnedRPC(server) {
		initContext = startrpc.OwnedRPCContext(server)
	}
	readerPath, readerSHA, err := startrpc.OwnedRPCHistoryPreparation(server)
	if err != nil {
		return err
	}
	var historyReader *messagedelivery.HistoryWindowReader
	var readerStartup *messagedelivery.HistoryReaderStartupBinding
	if readerPath != "" {
		historyReader, readerStartup, err = messagedelivery.NewStartupHistoryWindowReader(initContext, mongo.GetDatabase(), "rpc-msg", readerPath, readerSHA)
		if err != nil {
			return err
		}
	}
	namespaceRuntime, err := messagedelivery.NewNamespaceRuntimeBindingForOwnedContext(initContext, mongo.GetDatabase())
	if err != nil {
		return err
	}
	if namespaceRuntime != nil {
		if historyReader == nil || readerStartup == nil {
			return messagedelivery.ErrUnprepared
		}
		if err = namespaceRuntime.BindOriginalReader(historyReader, "msg-history"); err != nil {
			return err
		}
		if err = startrpc.OwnRPCNamespaceRuntimeBinding(server, namespaceRuntime); err != nil {
			return err
		}
		initContext = namespaceRuntime.BindRuntimeContext(initContext)
	}
	if err := mongo.CreateMsgIndex(); err != nil {
		return err
	}
	msgGroupRead, err := controller.NewPersistentMsgGroupReadDatabase(initContext, mongo.GetDatabase())
	if err != nil {
		return err
	}
	cacheModel := cache.NewMsgCacheModel(rdb)
	msgDocModel := unrelation.NewMsgMongoDriver(mongo.GetDatabase())
	conversationClient := rpcclient.NewConversationRpcClient(client)
	userRpcClient := rpcclient.NewUserRpcClient(client)
	groupRpcClient := rpcclient.NewGroupRpcClient(client)
	friendRpcClient := rpcclient.NewFriendRpcClient(client)
	if readerPath == "" {
		historyReader, readerStartup, err = messagedelivery.NewStartupHistoryWindowReader(initContext, mongo.GetDatabase(), "rpc-msg", "", "")
		if err != nil {
			return err
		}
	}
	namespaces, err := messagedelivery.NewMessageNamespaceAdmissionFromEnvironment(mongo.GetDatabase())
	if err != nil {
		return err
	}
	if namespaceRuntime != nil {
		if namespaces == nil {
			return messagedelivery.ErrUnprepared
		}
		if err = namespaceRuntime.BindOriginalStore(namespaces, "msg-namespaces"); err != nil {
			return err
		}
		if err = namespaceRuntime.CheckPreparedCatalog(initContext); err != nil {
			return err
		}
	}
	if namespaces != nil && readerStartup == nil {
		// Deliberately still fenced until importer, all writers and n_ readers
		// are ready. Constructing a Store is not activation evidence.
		readyCtx, cancel := context.WithTimeout(initContext, 5*time.Second)
		err := namespaces.CheckNewGroupSeedRuntimeReady(readyCtx)
		cancel()
		if err != nil {
			return err
		}
	}
	var msgDatabase controller.CommonMsgDatabase
	if startrpc.IsOwnedRPC(server) {
		msgDatabase, err = controller.NewOwnedCommonMsgDatabase(startrpc.OwnedRPCContext(server), msgDocModel, cacheModel,
			func(closeFn func(context.Context) error) error {
				return startrpc.OwnRPCResource(server, "kafka-producers", closeFn)
			}, historyReader)
		if err != nil {
			return err
		}
	} else {
		msgDatabase = controller.NewCommonMsgDatabase(msgDocModel, cacheModel, historyReader)
	}

	// The owned branch borrows this role's original lifetime and clients. The
	// actual two loops are registered immediately, before any later initializer.
	var sensitiveWordChecker *sensitiveword.SensitiveWordChecker
	if startrpc.IsOwnedRPC(server) {
		ownedCtx := startrpc.OwnedRPCContext(server)
		sensitiveWordDB, err := mgo.NewSensitiveWordMongoWithContext(ownedCtx, mongo.GetDatabase())
		if err != nil {
			return err
		}
		checker, err := sensitiveword.NewOwnedSensitiveWordChecker(ownedCtx, sensitiveWordDB, rdb)
		if err != nil {
			return err // the constructor joins/closes its own partial subscription
		}
		if err := startrpc.OwnRPCResource(server, "sensitive-word-workers", checker.Stop); err != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return errors.Join(err, checker.Stop(closeCtx))
		}
		if namespaceRuntime != nil {
			if err := startrpc.OwnRPCNamespaceActivation(server, checker.StartPrepared); err != nil {
				return err
			}
		}
		sensitiveWordChecker = checker.SensitiveWordChecker
	} else {
		// Preserve the existing default constructor and background semantics.
		sensitiveWordDB, err := mgo.NewSensitiveWordMongo(mongo.GetDatabase())
		if err != nil {
			return err
		}
		sensitiveWordChecker = sensitiveword.NewSensitiveWordChecker(sensitiveWordDB, rdb)
	}

	s := &msgServer{
		ownedWork:              startrpc.OwnedRPCWorkers(server),
		Conversation:           &conversationClient,
		User:                   &userRpcClient,
		Group:                  &groupRpcClient,
		MsgDatabase:            msgDatabase,
		namespaces:             namespaces,
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
	if readerStartup != nil {
		if err := startrpc.OwnRPCHistoryReader(server, readerStartup); err != nil {
			return err
		}
		if namespaces == nil {
			return messagedelivery.ErrUnprepared
		}
		if err := startrpc.OwnRPCPreparedWriterFinalCheck(server, func(ctx context.Context) error {
			if err := readerStartup.AwaitFinalRuntimeReady(ctx); err != nil {
				return err
			}
			return namespaces.CheckNewGroupSeedRuntimeReady(ctx)
		}); err != nil {
			return err
		}
	}
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
