package msgtransfer

import (
	"context"
	"errors"
	"time"

	"github.com/IBM/sarama"
	"github.com/OpenIMSDK/protocol/msggateway"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/push"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/aliasvendor"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	kdisc "github.com/openimsdk/open-im-server/v3/pkg/common/discoveryregister"
	"github.com/openimsdk/open-im-server/v3/pkg/common/kafka"
	"github.com/openimsdk/open-im-server/v3/pkg/common/rpcmiddleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

// Independent managed entry. The legacy StartTransfer is deliberately not
// edited or called. No startup path manufactures collections, Ready, namespace
// claims or migration evidence. The namespace successor must satisfy the
// existing complete runtime gate before any worker/consumer is exposed.
func StartManagedOrdinary(parent context.Context, prometheusPort int) (*ManagedOrdinaryRuntime, error) {
	if parent == nil || parent.Done() == nil {
		return nil, messagedelivery.ErrInvalid
	}
	boot, err := LoadOwnedOrdinaryBootstrap(config.Config.Mongo.Database)
	if err != nil {
		return nil, messagedelivery.ErrUnprepared
	}
	if boot.Topic != config.Config.Kafka.LatestMsgToRedis.Topic || boot.ConsumerGroup != config.Config.Kafka.ConsumerGroupID.MsgToRedis || len(config.Config.Kafka.Addr) == 0 {
		return nil, messagedelivery.ErrUnprepared
	}
	ctx, cancel := context.WithCancel(parent)
	r := newManagedOrdinaryRuntime(cancel)
	r.bootstrap, r.instance = boot, uuid.NewString()
	// All return paths return this owner so the command can join partial
	// construction. Closers are appended only for resources actually owned.
	var workers, callbacks, pools []ordinaryStopStep
	defer func() { r.steps = append(append(workers, callbacks...), pools...) }()
	mongoDB, openErr := unrelation.NewManagedMongo(ctx)
	if mongoDB != nil {
		pools = append(pools, ordinaryStopStep{"mongo", ordinaryCloseOnce(func() error {
			end, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			return mongoDB.GetClient().Disconnect(end)
		})})
	}
	if openErr != nil {
		return r, errors.New("managed ordinary storage initialization failed")
	}
	db := mongoDB.GetDatabase()
	namespaces, err := messagedelivery.NewMembershipBoundStore(db, boot.Epoch, boot.ActivationSHA256)
	if err != nil {
		return r, messagedelivery.ErrUnprepared
	}
	ready, endReady := context.WithTimeout(ctx, 10*time.Second)
	err = namespaces.CheckNewGroupSeedRuntimeReady(ready)
	endReady()
	if err != nil {
		return r, err
	}
	history, err := messagedelivery.NewHistoryWindowReaderFromEnvironment(db)
	if err != nil || history == nil {
		return r, messagedelivery.ErrUnprepared
	}
	if err := history.CheckReady(ctx); err != nil {
		return r, err
	}
	rdb, err := cache.NewGroupRuntimeRedis(ctx)
	if err != nil {
		return r, errors.New("managed ordinary cache initialization failed")
	}
	pools = append([]ordinaryStopStep{{"redis", ordinaryCloseOnce(rdb.Close)}}, pools...)
	discovery, err := kdisc.NewOwnedDiscoveryRegister(ctx, config.Config.Envs.Discovery)
	if err != nil {
		return r, errors.New("managed ordinary discovery initialization failed")
	}
	pools = append([]ordinaryStopStep{{"discovery", discovery.CloseOwned}}, pools...)
	if err := discovery.CreateRpcRootNodes(config.Config.GetServiceNames()); err != nil {
		return r, errors.New("managed ordinary discovery roots unavailable")
	}
	discovery.AddOption(rpcmiddleware.GrpcClient(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if len(config.Config.Manager.UserID) == 0 || !authverify.IsManagerUserID(config.Config.Manager.UserID[0]) {
		return r, messagedelivery.ErrUnprepared
	}
	manager := config.Config.Manager.UserID[0]
	authorize := func(ctx context.Context, request proto.Message) (context.Context, error) {
		if ctx == nil || ctx.Err() != nil {
			return nil, messagedelivery.ErrUnprepared
		}
		if _, ok := request.(*msggateway.OnlineBatchPushOneMsgReq); !ok {
			return nil, messagedelivery.ErrInvalid
		}
		return mcontext.WithOpUserIDContext(ctx, manager), nil
	}
	input := aliasvendor.OwnedInputFile{Path: boot.VendorPath, ExpectedSHA256: boot.VendorSHA256}
	registry, err := aliasvendor.LoadOwnedOfflineRegistry(ctx, input, db.Name(), boot.Epoch, boot.ActivationSHA256)
	if err != nil {
		return r, messagedelivery.ErrUnprepared
	}
	reader := controller.NewReadOnlyMessageReader(rdb, db, history)
	offline, err := NewOwnedOrdinaryOfflineRuntime(ctx, db, rdb, cache.NewMsgCacheModel(rdb), reader, boot.Epoch, boot.ActivationSHA256, boot.BadgeEvidenceSHA256, input, push.OwnedKafkaCallbackFile{Path: boot.OfflineCallbacksPath, ExpectedSHA256: boot.OfflineCallbacksSHA256})
	if err != nil {
		return r, err
	}
	r.offline = offline
	workers = append(workers, ordinaryStopStep{"offline", offline.Stop})
	policy := offline.Policy()
	policySHA, err := policy.Fingerprint()
	if err != nil || policySHA != boot.PolicySHA256 || policySHA != registry.CurrentPolicySHA256 {
		return r, messagedelivery.ErrUnprepared
	}
	var policies []messagedelivery.KafkaDeliveryPolicy
	for _, entry := range registry.Entries {
		policies = append(policies, entry.Policy)
	}
	engines, err := push.LoadOwnedKafkaOnlineCallbacks(ctx, push.OwnedKafkaCallbackFile{Path: boot.OnlineCallbacksPath, ExpectedSHA256: boot.OnlineCallbacksSHA256}, namespaces, db.Name(), boot.Epoch, boot.ActivationSHA256, policies)
	if err != nil {
		return r, err
	}
	callbacks = append(callbacks, ordinaryStopStep{"online-callbacks", func(context.Context) error {
		for _, engine := range engines {
			if err := engine.CloseOwned(); err != nil {
				return err
			}
		}
		return nil
	}})
	var onlineTransports []messagedelivery.KafkaOnlineTransport
	for _, entry := range registry.Entries {
		var configured []*push.FrozenKafkaPushCallbacks
		if engine := engines[entry.Fingerprint]; engine != nil {
			configured = append(configured, engine)
		}
		transport, err := push.NewKafkaFrozenGatewayTransport(discovery, config.Config.RpcRegisterName.OpenImMessageGatewayName, entry.Policy.Discovery == "k8s", authorize, namespaces, entry.Policy, configured...)
		if err != nil {
			return r, err
		}
		onlineTransports = append(onlineTransports, transport)
	}
	projection, err := messagedelivery.NewRedisProjection(rdb)
	if err != nil {
		return r, err
	}
	online, err := NewManagedKafkaOnlineWorker(namespaces, reader, projection, onlineTransports)
	if err != nil {
		return r, err
	}
	onlineStarted := false
	workers = append([]ordinaryStopStep{{"online", func(ctx context.Context) error {
		if !onlineStarted {
			return nil
		}
		return online.Stop(ctx)
	}}}, workers...)
	intake, err := NewDurableKafkaIntakeWithOfflineRuntime(db, boot.Epoch, boot.ActivationSHA256, boot.Topic, offline)
	if err != nil {
		return r, err
	}
	consumerConfig := sarama.NewConfig()
	consumerConfig.Version = sarama.V2_8_1_0
	consumerConfig.Consumer.Offsets.Initial = sarama.OffsetOldest
	consumerConfig.Consumer.Return.Errors = false
	consumerConfig.Net.DialTimeout, consumerConfig.Net.ReadTimeout, consumerConfig.Net.WriteTimeout = 5*time.Second, 5*time.Second, 5*time.Second
	if config.Config.Kafka.Username != "" || config.Config.Kafka.Password != "" {
		if config.Config.Kafka.Username == "" || config.Config.Kafka.Password == "" {
			return r, messagedelivery.ErrUnprepared
		}
		consumerConfig.Net.SASL.Enable, consumerConfig.Net.SASL.User, consumerConfig.Net.SASL.Password = true, config.Config.Kafka.Username, config.Config.Kafka.Password
	}
	kafka.SetupTLSConfig(consumerConfig)
	consumer, err := sarama.NewConsumerGroup(append([]string(nil), config.Config.Kafka.Addr...), boot.ConsumerGroup, consumerConfig)
	if err != nil {
		return r, errors.New("managed ordinary consumer initialization failed")
	}
	managed, err := NewManagedOrdinaryKafka(intake, consumer)
	if err != nil {
		_ = consumer.Close()
		return r, err
	}
	r.intake = managed
	workers = append([]ordinaryStopStep{{"intake", func(ctx context.Context) error {
		if r.intakeStarted {
			return managed.Stop(ctx)
		}
		return stopUnstartedOrdinaryIntake(ctx, managed)
	}}}, workers...)
	if config.Config.Prometheus.Enable {
		metricsStop, failure, err := startManagedOrdinaryMetrics(ctx, prometheusPort)
		if err != nil {
			return r, err
		}
		r.unexpected = failure
		pools = append([]ordinaryStopStep{{"metrics", metricsStop}}, pools...)
	}
	if err := online.Start(ctx); err != nil {
		return r, err
	}
	onlineStarted = true
	if err := offline.Start(); err != nil {
		return r, err
	}
	// Re-read immutable private registry immediately before intake admission.
	again, err := aliasvendor.LoadOwnedOfflineRegistry(ctx, input, db.Name(), boot.Epoch, boot.ActivationSHA256)
	if err != nil || again.CurrentPolicySHA256 != registry.CurrentPolicySHA256 {
		return r, messagedelivery.ErrUnprepared
	}
	if err := managed.Start(ctx); err != nil {
		return r, err
	}
	r.intakeStarted = true
	return r, nil
}
