package msgtransfer

import (
	"context"
	"errors"
	pbconversation "github.com/OpenIMSDK/protocol/conversation"
	pbencryption "github.com/OpenIMSDK/protocol/encryption"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	unrelationtb "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	kdisc "github.com/openimsdk/open-im-server/v3/pkg/common/discoveryregister"
	"github.com/openimsdk/open-im-server/v3/pkg/common/kafka"
	"github.com/openimsdk/open-im-server/v3/pkg/common/rpcmiddleware"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"time"
)

func (r *ownedLegacyRuntime) construct(ctx context.Context, port int) error {
	connection, err := unrelation.NewProcessRuntimeMongo(ctx)
	if connection != nil {
		r.closeMongo = func(call context.Context) error { return connection.GetClient().Disconnect(call) }
	}
	if err != nil {
		return err
	}
	if connection == nil {
		return errOwnedLegacyTransfer
	}
	database := connection.GetDatabase()
	// Original private bootstrap, checked BEFORE any index write.
	if err = r.identity.CheckDatabase(database.Name()); err != nil {
		return err
	}
	for _, index := range []mongo.IndexModel{
		{Keys: bson.D{{Key: "doc_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "msgs.msg.client_msg_id", Value: 1}, {Key: "doc_id", Value: 1}}},
	} {
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err = database.Collection(unrelationtb.Msg).Indexes().CreateOne(bounded, index, options.CreateIndexes().SetMaxTime(10*time.Second))
		cancel()
		if err != nil {
			return err
		}
	}
	rdb, err := cache.NewOwnedSourceRedis(ctx)
	if rdb != nil {
		r.closeRedis = func(context.Context) error { return rdb.Close() }
	}
	if err != nil {
		return err
	}
	if rdb == nil {
		return errOwnedLegacyTransfer
	}
	discovery, err := kdisc.NewOwnedDiscoveryRegister(ctx, config.Config.Envs.Discovery)
	if discovery != nil {
		r.closeDiscovery = discovery.CloseOwned
	}
	if err != nil {
		return err
	}
	if discovery == nil {
		return errOwnedLegacyTransfer
	}
	if err = discovery.CreateRpcRootNodes(config.Config.GetServiceNames()); err != nil {
		return err
	}
	discovery.AddOption(rpcmiddleware.GrpcClient(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	model := unrelation.NewMsgMongoDriver(database)
	messages, err := controller.NewOwnedCommonMsgDatabase(ctx, model, cache.NewMsgCacheModel(rdb), func(closeFn func(context.Context) error) error {
		if closeFn == nil || r.closeProducers != nil {
			return errOwnedLegacyTransfer
		}
		r.closeProducers = closeFn
		return nil
	})
	if err != nil {
		return err
	}
	// Bind actual discovery/dial work to this original owner and return its
	// error through the already registered partial-resource cleanup. The default
	// rpcclient constructors remain unchanged; no Background/panic wrapper here.
	conversationConn, err := discovery.GetConn(ctx, config.Config.RpcRegisterName.OpenImConversationName)
	if err != nil || conversationConn == nil || ctx.Err() != nil {
		return errors.Join(err, ctx.Err(), errOwnedLegacyTransfer)
	}
	groupConn, err := discovery.GetConn(ctx, config.Config.RpcRegisterName.OpenImGroupName)
	if err != nil || groupConn == nil || ctx.Err() != nil {
		return errors.Join(err, ctx.Err(), errOwnedLegacyTransfer)
	}
	encryptionConn, err := discovery.GetConn(ctx, config.Config.RpcRegisterName.OpenImEncryptionName)
	if err != nil || encryptionConn == nil || ctx.Err() != nil {
		return errors.Join(err, ctx.Err(), errOwnedLegacyTransfer)
	}
	conversation := rpcclient.ConversationRpcClient{Client: pbconversation.NewConversationClient(conversationConn)}
	group := rpcclient.GroupRpcClient{Client: pbgroup.NewGroupClient(groupConn)}
	encryption := rpcclient.EncryptionRpcClient{Client: pbencryption.NewEncryptionClient(encryptionConn)}
	// These are the actual original processing methods, initialized WITHOUT their
	// unowned constructors' auto-started distributor/ticker or unrelated clients.
	redisHandler := &OnlineHistoryRedisConsumerHandler{msgDatabase: messages, conversationRpcClient: &conversation, groupRpcClient: &group, encryptionRpcClient: &encryption, owned: r.source}
	mongoHandler := &OnlineHistoryMongoConsumerHandler{msgDatabase: messages}
	lanes := &ownedLegacyLanes{}
	for _, item := range []struct {
		topic, group string
		handler      *ownedLegacyClaim
	}{
		{config.Config.Kafka.LatestMsgToRedis.Topic, config.Config.Kafka.ConsumerGroupID.MsgToRedis, &ownedLegacyClaim{source: r.source, redisHandler: redisHandler, lanes: lanes}},
		{config.Config.Kafka.MsgToMongo.Topic, config.Config.Kafka.ConsumerGroupID.MsgToMongo, &ownedLegacyClaim{source: r.source, mongoHandler: mongoHandler}},
	} {
		consumer, err := kafka.NewOwnedConsumerGroup(ctx, config.Config.Kafka.Addr, item.group)
		if err != nil {
			return err
		}
		run, err := kafka.NewOwnedConsumerRun(ctx, consumer, []string{item.topic}, item.handler, r.source.Fail)
		if err != nil {
			return errors.Join(err, consumer.Close())
		}
		item.handler.run = run
		r.consumers = append(r.consumers, run)
	}
	return r.metricsStart(port)
}
