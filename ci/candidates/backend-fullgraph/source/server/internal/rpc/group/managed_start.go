package group

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/msggateway"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/OpenIMSDK/tools/utils"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/msgtransfer"
	"github.com/openimsdk/open-im-server/v3/internal/push"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/aliasvendor"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mongotx"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/startrpc"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient/grouphash"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient/notification"
	"os"
)

// Legacy callers cannot accidentally start an authority service with an
// unowned recovery goroutine. The group executable uses StartManaged below.
func Start(discoveryregistry.SvcDiscoveryRegistry, *grpc.Server) error {
	return errors.New("group service requires managed startup")
}

// StartManaged refuses standalone/weak catalog storage before registering any
// group RPC. Creating the dedicated outbox is an explicit operator prerequisite;
// neither startup nor recovery creates it or repairs unknown collection options.
func StartManaged(parent context.Context, client discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) (startrpc.ManagedStop, error) {
	if parent == nil || parent.Done() == nil || server == nil {
		return nil, errors.New("managed group lifecycle required")
	}
	// Constructor work inherits the exact owned scope/nonce and deferred-worker context.
	if owned := startrpc.OwnedRPCContext(server); owned != nil {
		parent = owned
	}
	lifetime, cancel := context.WithCancel(parent)
	runtime := &ownedGroupRuntime{cancel: cancel}
	stop := startrpc.ManagedStop(runtime.stop)
	mongoDB, err := unrelation.NewProcessRuntimeMongo(startrpc.OwnedRPCContext(server))
	if mongoDB != nil {
		runtime.disconnect = mongoDB.GetClient().Disconnect
		if trackErr := startrpc.OwnRPCDatabase(server, mongoDB.GetDatabase().Name()); trackErr != nil {
			return stop, trackErr
		}
	}
	if err != nil {
		return stop, errors.New("group storage initialization failed")
	}

	binding, err := messagedelivery.NewNamespaceRuntimeBindingForOwnedContext(lifetime, mongoDB.GetDatabase())
	if err != nil {
		return stop, err
	}
	if binding != nil {
		if err = startrpc.OwnRPCNamespaceRuntimeBinding(server, binding); err != nil {
			return stop, err
		}
		lifetime = binding.BindRuntimeContext(lifetime)
	}
	rdb, err := cache.NewGroupRuntimeRedis(lifetime)
	if err != nil {
		return stop, errors.New("group cache initialization failed")
	}
	runtime.closeShared = rdb.Close
	invalidator, err := cache.NewGroupAuthorityInvalidator(rdb)
	if err != nil {
		return stop, errors.New("group cache recovery initialization failed")
	}
	runtime.closeRecovery = invalidator.Close
	authority, err := groupauthority.NewStore(mongoDB.GetDatabase())
	if err != nil {
		return stop, errors.New("group authority initialization failed")
	}
	ready, done := context.WithTimeout(lifetime, 5*time.Second)
	err = authority.CheckReady(ready)
	done()
	if err != nil {
		return stop, errors.New("group authority storage is not ready")
	}
	if err = invalidator.CheckReady(lifetime); err != nil {
		return stop, errors.New("group authority cache is not ready")
	}
	modelsContext, modelsDone := context.WithTimeout(lifetime, 10*time.Second)
	defer modelsDone()
	groupDB, err := mgo.NewGroupMongoContext(modelsContext, mongoDB.GetDatabase())
	if err != nil {
		return stop, errors.New("group model initialization failed")
	}
	memberDB, err := mgo.NewGroupMemberContext(modelsContext, mongoDB.GetDatabase())
	if err != nil {
		return stop, errors.New("group member model initialization failed")
	}
	requestDB, err := mgo.NewGroupRequestMgoContext(modelsContext, mongoDB.GetDatabase())
	if err != nil {
		return stop, errors.New("group request model initialization failed")
	}
	user := rpcclient.NewUserRpcClient(client)
	message := rpcclient.NewMessageRpcClient(client)
	var service groupServer
	database := controller.NewGroupDatabase(rdb, groupDB, memberDB, requestDB, mongotx.New(mongoDB.GetClient()), grouphash.NewGroupHashFromGroupServer(&service))
	service.db, service.authority, service.invalidate = database, authority, invalidator.Apply
	service.User, service.msgRpcClient, service.rdb = user, message, rdb
	service.conversationRpcClient = rpcclient.NewConversationRpcClient(client)
	service.Notification = notification.NewGroupNotificationSender(database, &message, &user, func(ctx context.Context, userIDs []string) ([]notification.CommonUser, error) {
		users, err := user.GetUsersInfo(ctx, userIDs)
		if err != nil {
			return nil, err
		}
		return utils.Slice(users, func(value *sdkws.UserInfo) notification.CommonUser { return value }), nil
	})
	// The service may not acknowledge a new typed source until the complete
	// receiver/retained registry and membership reader are ready. No implicit
	// current-config repair or legacy StartTransfer activation occurs here.
	boot, err := msgtransfer.LoadOwnedGroupReceiverBootstrap(mongoDB.GetDatabase().Name())
	if err != nil {
		return stop, errors.New("owned group receiver manifest is required")
	}
	if os.Getenv(messagedelivery.HistoryWindowModeEnv) != "required" || os.Getenv(messagedelivery.HistoryWindowEpochEnv) != boot.Epoch || os.Getenv(messagedelivery.HistoryWindowActivationEnv) != boot.ActivationSHA256 {
		return stop, errors.New("group membership reader activation mismatch")
	}
	history, err := messagedelivery.NewHistoryWindowReaderFromEnvironment(mongoDB.GetDatabase())
	if err != nil {
		return stop, errors.New("owned group membership reader unavailable")
	}
	readCtx, readDone := context.WithTimeout(lifetime, 5*time.Second)
	if binding != nil {
		err = binding.BindOriginalReader(history, "group-history")
		if err == nil {
			err = binding.CheckPreparedCatalog(readCtx)
		}
	} else {
		err = history.CheckReady(readCtx)
	}
	readDone()
	if err != nil {
		return stop, errors.New("owned group membership reader unprepared")
	}
	// Base-store construction never writes reader/window Ready flags. A prior
	// complete immutable namespace inventory+drain registration is mandatory;
	// row absence/new GroupID alone never authorizes a zero seed.
	newGroups, err := messagedelivery.NewStore(mongoDB.GetDatabase(), boot.Epoch, boot.ActivationSHA256)
	if err != nil {
		return stop, errors.New("new group namespace initializer unavailable")
	}
	seedReady, seedDone := context.WithTimeout(lifetime, 5*time.Second)
	if binding != nil {
		err = binding.BindOriginalStore(newGroups, "new-group-namespaces")
		if err == nil {
			err = binding.CheckPreparedCatalog(seedReady)
		}
	} else {
		err = newGroups.CheckNewGroupSeedRuntimeReady(seedReady)
	}
	seedDone()
	if err != nil {
		return stop, errors.New("new group namespace migration and complete writer admission required")
	}
	service.newGroups = newGroups
	if len(config.Config.Manager.UserID) == 0 || !authverify.IsManagerUserID(config.Config.Manager.UserID[0]) {
		return stop, errors.New("owned gateway service identity unavailable")
	}
	manager := config.Config.Manager.UserID[0]
	// Trusted startup identity only. Never inherit an incoming account/opuser.
	authorize := func(ctx context.Context, request proto.Message) (context.Context, error) {
		if ctx == nil || ctx.Err() != nil {
			return nil, errors.New("gateway context unavailable")
		}
		if _, ok := request.(*msggateway.OnlineBatchPushOneMsgReq); !ok {
			return nil, errors.New("unexpected gateway request")
		}
		return mcontext.WithOpUserIDContext(ctx, manager), nil
	}
	receiver, err := msgtransfer.NewOwnedGroupReceiverRuntime(lifetime, mongoDB.GetDatabase(), rdb, cache.NewMsgCacheModel(rdb), controller.NewReadOnlyMessageReader(rdb, mongoDB.GetDatabase(), history), client, config.Config.RpcRegisterName.OpenImMessageGatewayName, authorize, boot.Epoch, boot.ActivationSHA256, boot.BadgeEvidenceSHA256, msgtransfer.OwnedGroupReceiverInputs{Vendor: aliasvendor.OwnedInputFile{Path: boot.VendorPath, ExpectedSHA256: boot.VendorSHA256}, MessageCallbacks: push.OwnedKafkaCallbackFile{Path: boot.MessageCallbacksPath, ExpectedSHA256: boot.MessageCallbacksSHA256}, OfflineCallbacks: push.OwnedKafkaCallbackFile{Path: boot.OfflineCallbacksPath, ExpectedSHA256: boot.OfflineCallbacksSHA256}, RoleCallbacks: push.OwnedKafkaCallbackFile{Path: boot.RoleCallbacksPath, ExpectedSHA256: boot.RoleCallbacksSHA256}})
	if err != nil {
		return stop, errors.New("owned group receiver initialization failed")
	}
	runtime.stopReceiver = receiver.Stop
	service.notices, err = newOwnedGroupNoticeRuntime(mongoDB.GetDatabase(), boot.Epoch, boot.ActivationSHA256, receiver.Profile().Transport.VendorBinding)
	if err != nil {
		return stop, errors.New("owned group source profile initialization failed")
	}
	startReceiver := func() error {
		if e := newGroups.CheckNewGroupSeedRuntimeReady(lifetime); e != nil {
			return e
		}
		if e := receiver.Start(); e != nil {
			return errors.New("owned group receiver readiness rejected")
		}
		return nil
	}
	if binding != nil {
		if err = startrpc.OwnRPCNamespaceActivation(server, startReceiver); err != nil {
			return stop, err
		}
	} else if err = startReceiver(); err != nil {
		return stop, err
	}
	if err := lifetime.Err(); err != nil {
		return stop, err
	}
	group.RegisterGroupServer(server, &service)
	startRecovery := func() error {
		if lifetime.Err() != nil {
			return lifetime.Err()
		}
		worker := make(chan error, 1)
		runtime.worker = worker
		go func() { worker <- authority.RunCacheRecovery(lifetime, invalidator.Apply, nil) }()
		return nil
	}
	if binding != nil {
		if err = startrpc.OwnRPCNamespaceActivation(server, startRecovery); err != nil {
			return stop, err
		}
	} else if err = startRecovery(); err != nil {
		return stop, err
	}
	return stop, nil
}

// Owned by one group process. Stop is called after handlers drain, cancels and
// joins the single recovery worker before closing its pools. A failed join does
// not falsely report cleanup or close a pool underneath a still-running worker.
type ownedGroupRuntime struct {
	cancel        context.CancelFunc
	worker        <-chan error
	closeRecovery func() error
	closeShared   func() error
	disconnect    func(context.Context) error
	once          sync.Once
	mu            sync.Mutex
	stopDone      chan struct{}
	stopReceiver  func(context.Context) error
	err           error
}

func (r *ownedGroupRuntime) stop(ctx context.Context) error {
	if r == nil || ctx == nil {
		return errors.New("managed group shutdown context required")
	}
	r.mu.Lock()
	if r.stopDone == nil {
		r.stopDone = make(chan struct{})
	}
	joined := r.stopDone
	r.mu.Unlock()
	r.once.Do(func() {
		go func() {
			if r.cancel != nil {
				r.cancel()
			}
			var failures []error
			// A caller timeout never closes the shared DB/Redis under a live receiver.
			// The single cleanup operation retains ownership for later Stop callers.
			if r.stopReceiver != nil {
				if err := r.stopReceiver(context.Background()); err != nil {
					failures = append(failures, err)
				}
			}
			if r.worker != nil {
				if err := <-r.worker; err != nil && !errors.Is(err, context.Canceled) {
					failures = append(failures, errors.New("group cache worker stopped unexpectedly"))
				}
			}
			if r.closeRecovery != nil {
				failures = append(failures, r.closeRecovery())
			}
			if r.closeShared != nil {
				failures = append(failures, r.closeShared())
			}
			if r.disconnect != nil {
				end, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				failures = append(failures, r.disconnect(end))
				cancel()
			}
			r.mu.Lock()
			r.err = errors.Join(failures...)
			r.mu.Unlock()
			close(joined)
		}()
	})
	select {
	case <-joined:
		r.mu.Lock()
		err := r.err
		r.mu.Unlock()
		return err
	case <-ctx.Done():
		return errors.New("group owned shutdown unconfirmed; retain pools")
	}
}
