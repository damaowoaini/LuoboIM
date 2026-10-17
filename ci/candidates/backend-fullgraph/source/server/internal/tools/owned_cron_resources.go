package tools

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/retention"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mongotx"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	kdisc "github.com/openimsdk/open-im-server/v3/pkg/common/discoveryregister"
	"github.com/openimsdk/open-im-server/v3/pkg/common/rpcmiddleware"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient/notification"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func newOwnedCronResources(ctx context.Context, checkDatabase func(string) error) (*ownedCronResources, error) {
	if ctx == nil || ctx.Done() == nil || ctx.Err() != nil || ownedmongo.FromContext(ctx) == nil || checkDatabase == nil {
		return nil, errOwnedCron
	}
	out := &ownedCronResources{}
	connection, err := unrelation.NewProcessRuntimeMongo(ctx)
	if connection != nil {
		out.closeMongo = func(call context.Context) error { return connection.GetClient().Disconnect(call) }
	}
	if err != nil {
		return out, err
	}
	if connection == nil {
		return out, errOwnedCron
	}
	// Freeze this exact original handle and check it before any model indexes.
	database := connection.GetDatabase()
	if err := checkDatabase(database.Name()); err != nil {
		return out, err
	}
	binding, err := messagedelivery.NewNamespaceRuntimeBindingForOwnedContext(ctx, database)
	if err != nil {
		return out, err
	}
	if binding != nil {
		ctx = binding.BindRuntimeContext(ctx)
		out.namespaceBinding = binding
	}
	rdb, err := cache.NewProcessRuntimeRedis(ctx)
	if rdb != nil {
		out.locker = rdb
		out.closeRedis = func(context.Context) error { return rdb.Close() }
	}
	if err != nil {
		return out, err
	}
	if rdb == nil {
		return out, errOwnedCron
	}
	discovery, err := kdisc.NewOwnedDiscoveryRegister(ctx, config.Config.Envs.Discovery)
	if discovery != nil {
		out.closeDiscovery = discovery.CloseOwned
	}
	if err != nil {
		return out, err
	}
	if discovery == nil {
		return out, errOwnedCron
	}
	discovery.AddOption(rpcmiddleware.GrpcClient(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	userDB, err := mgo.NewUserMongo(database)
	if err != nil {
		return out, err
	}
	history, err := messagedelivery.NewHistoryWindowReaderFromEnvironment(database)
	if err != nil {
		return out, err
	}
	if binding != nil {
		if err = binding.BindOriginalReader(history, "cron-history"); err != nil {
			return out, err
		}
		if err = binding.CheckPreparedCatalog(ctx); err != nil {
			return out, err
		}
	} else if err = history.CheckReady(ctx); err != nil {
		return out, err
	}
	msgDatabase, err := controller.NewOwnedCommonMsgDatabase(ctx, unrelation.NewMsgMongoDriver(database), cache.NewMsgCacheModel(rdb),
		func(closeFn func(context.Context) error) error {
			if out.closeProducers != nil || closeFn == nil {
				return errOwnedCron
			}
			out.closeProducers = closeFn
			return nil
		}, history)
	if err != nil {
		return out, err
	}
	tx := mongotx.New(database.Client())
	userDatabase := controller.NewUserDatabase(userDB, cache.NewUserCacheRedis(rdb, userDB, cache.GetDefaultOpt()), tx, unrelation.NewUserMongoDriver(database))
	groupDB, err := mgo.NewGroupMongo(database)
	if err != nil {
		return out, err
	}
	members, err := mgo.NewGroupMember(database)
	if err != nil {
		return out, err
	}
	requests, err := mgo.NewGroupRequestMgo(database)
	if err != nil {
		return out, err
	}
	conversations, err := mgo.NewConversationMongo(database)
	if err != nil {
		return out, err
	}
	groupDatabase := controller.NewGroupDatabase(rdb, groupDB, members, requests, tx, nil)
	conversationDatabase := controller.NewConversationDatabase(conversations, cache.NewConversationRedis(rdb, cache.GetDefaultOpt(), conversations), tx)
	messageRPC := rpcclient.NewMessageRpcClient(discovery)
	notifications := notification.NewMsgNotificationSender(rpcclient.WithRpcClient(&messageRPC))
	tool := NewMsgTool(msgDatabase, userDatabase, groupDatabase, conversationDatabase, notifications)
	if os.Getenv("LUOBO_RETENTION_SAFE_CRON_V1") == "1" {
		store := retention.NewMongoStore(database)
		store.LegacyFloors = func(call context.Context, owner, cid string) (int64, int64, error) {
			own, err := msgDatabase.GetConversationUserMinSeq(call, cid, owner)
			if err != nil {
				return 0, 0, err
			}
			global, err := msgDatabase.GetMinSeq(call, cid)
			return own, global, err
		}
		initial, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := store.EnsureIndexes(initial)
		cancel()
		if err != nil {
			return out, err
		}
		tool.retentionStore = store
	}
	out.jobs = [2]func(context.Context) error{
		func(call context.Context) error { tool.allConversationClearMsgAndFixSeq(call); return nil },
		func(call context.Context) error { tool.conversationsDestructMsgs(call); return nil },
	}
	if ctx.Err() != nil {
		return out, errors.Join(errOwnedCron, ctx.Err())
	}
	return out, nil
}
