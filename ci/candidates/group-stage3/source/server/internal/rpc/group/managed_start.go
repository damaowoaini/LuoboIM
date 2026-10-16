package group

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/OpenIMSDK/tools/utils"
	"google.golang.org/grpc"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mongotx"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/startrpc"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient/grouphash"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient/notification"
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
	lifetime, cancel := context.WithCancel(parent)
	runtime := &ownedGroupRuntime{cancel: cancel}
	stop := startrpc.ManagedStop(runtime.stop)
	mongoDB, err := unrelation.NewMongo()
	if err != nil {
		return stop, errors.New("group storage initialization failed")
	}
	runtime.disconnect = mongoDB.GetClient().Disconnect
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
	if err := lifetime.Err(); err != nil {
		return stop, err
	}
	group.RegisterGroupServer(server, &service)
	worker := make(chan error, 1)
	runtime.worker = worker
	go func() { worker <- authority.RunCacheRecovery(lifetime, invalidator.Apply, nil) }()
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
	err           error
}

func (r *ownedGroupRuntime) stop(ctx context.Context) error {
	r.once.Do(func() {
		if r.cancel != nil {
			r.cancel()
		}
		if r.worker != nil {
			select {
			case err := <-r.worker:
				if err != nil && !errors.Is(err, context.Canceled) {
					r.err = errors.Join(r.err, errors.New("group cache worker stopped unexpectedly"))
				}
			case <-ctx.Done():
				r.err = errors.New("group cache worker shutdown unconfirmed")
				return
			}
		}
		if r.closeRecovery != nil {
			r.err = errors.Join(r.err, r.closeRecovery())
		}
		if r.closeShared != nil {
			r.err = errors.Join(r.err, r.closeShared())
		}
		if r.disconnect != nil {
			current, cancel := context.WithTimeout(ctx, 2*time.Second)
			r.err = errors.Join(r.err, r.disconnect(current))
			cancel()
		}
	})
	return r.err
}
