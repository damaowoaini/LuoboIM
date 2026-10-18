package api

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http"
	"sync"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/sessionlogout"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/sensitiveword"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
)

// All API database-backed routes borrow this exact owned database and Redis
// client. They never create or close a second singleton/client on the owned path.
type ownedRouteScope struct {
	ctx                context.Context
	cancel             context.CancelFunc
	database           *mongo.Database
	historyReader      *messagedelivery.HistoryWindowReader
	logout             *sessionlogout.Service
	sensitive          *sensitiveword.OwnedChecker
	logoutStarted      bool
	logoutDone         chan struct{}
	startOnce          sync.Once
	metrics            gin.HandlerFunc
	validatorClient    *http.Client
	validatorTransport *http.Transport
}

func newOwnedRouteScope(parent context.Context, database *mongo.Database) (*ownedRouteScope, error) {
	ctx, cancel := context.WithCancel(parent)
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		cancel()
		return nil, errOwnedAPI
	}
	transport := base.Clone()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &ownedRouteScope{ctx: ctx, cancel: cancel, database: database, logoutDone: make(chan struct{}), validatorClient: client, validatorTransport: transport}, nil
}
func (s *ownedRouteScope) newSensitiveWordAPI(rdb redis.UniversalClient) (*SensitiveWordApi, error) {
	if s == nil || s.database == nil || rdb == nil {
		return nil, errOwnedAPI
	}
	model, err := mgo.NewSensitiveWordMongoWithContext(s.ctx, s.database)
	if err != nil {
		return nil, err
	}
	checker, err := sensitiveword.NewOwnedSensitiveWordChecker(s.ctx, model, rdb)
	if err != nil {
		return nil, err
	}
	s.sensitive = checker
	return &SensitiveWordApi{db: model, checker: checker.SensitiveWordChecker}, nil
}
func (s *ownedRouteScope) start() error {
	if s == nil || s.logout == nil || s.sensitive == nil || s.ctx.Err() != nil {
		return errOwnedAPI
	}
	if err := s.sensitive.StartPrepared(); err != nil {
		return err
	}
	started := false
	s.startOnce.Do(func() {
		s.logoutStarted, started = true, true
		go func() { defer close(s.logoutDone); s.logout.Run(s.ctx) }()
	})
	if !started {
		return errOwnedAPI
	}
	return nil
}
func (s *ownedRouteScope) stopLogout(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.cancel()
	if !s.logoutStarted {
		return nil
	}
	select {
	case <-s.logoutDone:
		return nil
	case <-ctx.Done():
		return errors.Join(errOwnedAPI, ctx.Err())
	}
}
func (s *ownedRouteScope) stopSensitive(ctx context.Context) error {
	if s == nil || s.sensitive == nil {
		return nil
	}
	s.cancel()
	return s.sensitive.Stop(ctx)
}
func (s *ownedRouteScope) closeValidationHTTP(context.Context) error {
	if s == nil || s.validatorTransport == nil {
		return nil
	}
	// Every Validate call has already joined with its HTTP handler; close only
	// this client's actual idle sockets, never the process-global transport.
	s.validatorTransport.CloseIdleConnections()
	return nil
}
