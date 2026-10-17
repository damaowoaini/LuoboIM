package startrpc

import (
	"context"
	"errors"
	"sort"
	"sync"

	"google.golang.org/grpc"
)

var errOwnedRPC = errors.New("owned RPC lifecycle is unconfirmed")
var ownedRPCScopes sync.Map // exact *grpc.Server -> one original scope, never a role lookup

type ownedResource struct {
	name  string
	close func(context.Context) error
}
type ownedRPCScope struct {
	role        string
	workers     *OwnedRPCWork
	mu          sync.Mutex
	resources   map[string]ownedResource
	sealed      bool
	database    string
	closeOnce   sync.Once
	closeDone   chan struct{}
	closedNames []string
	closeErr    error
}

func ownedScope(server *grpc.Server) *ownedRPCScope {
	if value, ok := ownedRPCScopes.Load(server); ok {
		return value.(*ownedRPCScope)
	}
	return nil
}
func IsOwnedRPC(server *grpc.Server) bool { return ownedScope(server) != nil }
func OwnedRPCContext(server *grpc.Server) context.Context {
	if scope := ownedScope(server); scope != nil {
		return scope.workers.ctx
	}
	return nil
}
func OwnedRPCWorkers(server *grpc.Server) *OwnedRPCWork {
	if scope := ownedScope(server); scope != nil {
		return scope.workers
	}
	return nil
}

func OwnRPCResource(server *grpc.Server, name string, closeFn func(context.Context) error) error {
	scope := ownedScope(server)
	if scope == nil {
		return nil
	} // unchanged ordinary/unowned startup has no receipt
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.sealed || closeFn == nil || scope.resources[name].close != nil {
		return errOwnedRPC
	}
	allowed := ownedResourceNames(scope.role)
	found := false
	for _, value := range allowed {
		if name == value {
			found = true
		}
	}
	if !found {
		return errOwnedRPC
	}
	scope.resources[name] = ownedResource{name, closeFn}
	return nil
}
func OwnRPCDatabase(server *grpc.Server, name string) error {
	scope := ownedScope(server)
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.sealed || name == "" || (scope.database != "" && scope.database != name) {
		return errOwnedRPC
	}
	scope.database = name
	return nil
}
func ownedResourceNames(role string) []string {
	switch role {
	case "rpc-msg":
		return []string{"kafka-producers", "redis", "mongo"}
	case "rpc-conversation", "rpc-user", "rpc-friend", "rpc-encryption", "rpc-rtc":
		return []string{"redis", "mongo"}
	case "rpc-auth":
		return []string{"redis"}
	case "rpc-group":
		return []string{"group-runtime"}
	default:
		return nil // placeholder/consumer/object-provider loops have separate owners
	}
}
func (s *ownedRPCScope) seal(database string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	expected := ownedResourceNames(s.role)
	if s.sealed || expected == nil || len(s.resources) != len(expected) {
		return errOwnedRPC
	}
	for _, name := range expected {
		if s.resources[name].close == nil {
			return errOwnedRPC
		}
	}
	// Auth uses no Mongo client; it binds the configured logical tenant database,
	// without claiming that this role opened or read that Mongo database.
	if s.role != "rpc-auth" && s.database != database {
		return errOwnedRPC
	}
	s.sealed = true
	return nil
}
func (s *ownedRPCScope) names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var names []string
	for name := range s.resources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func (s *ownedRPCScope) closeResources(ctx context.Context) ([]string, error) {
	s.closeOnce.Do(func() {
		go func() {
			names, err := s.closeResourcesOnce(ctx)
			s.mu.Lock()
			s.closedNames = names
			s.closeErr = err
			s.mu.Unlock()
			close(s.closeDone)
		}()
	})
	select {
	case <-s.closeDone:
		s.mu.Lock()
		defer s.mu.Unlock()
		return append([]string{}, s.closedNames...), s.closeErr
	case <-ctx.Done():
		return nil, errors.Join(errOwnedRPC, ctx.Err())
	}
}
func (s *ownedRPCScope) closeResourcesOnce(ctx context.Context) ([]string, error) {
	var completed []string
	// Fixed production dependency order, not constructor map iteration order.
	for _, name := range ownedResourceNames(s.role) {
		s.mu.Lock()
		resource, exists := s.resources[name]
		s.mu.Unlock()
		if !exists {
			continue
		} // partial construction closes only resources actually made
		done := make(chan error, 1)
		go func() { done <- resource.close(ctx) }()
		select {
		case err := <-done:
			if err != nil {
				return completed, errors.Join(errOwnedRPC, err)
			}
			completed = append(completed, name)
		case <-ctx.Done():
			return completed, errors.Join(errOwnedRPC, ctx.Err())
		}
	}
	return completed, nil
}

// OwnedRPCWork tracks the actual detached Msg task and RTC Run loop. This is
// local completion only; it neither retries effects nor attests remote ACKs.
type OwnedRPCWork struct {
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	stopping bool
	active   int
	done     chan struct{}
	rejected bool
}

func newOwnedRPCWork() *OwnedRPCWork {
	ctx, cancel := context.WithCancel(context.Background())
	return &OwnedRPCWork{ctx: ctx, cancel: cancel, done: make(chan struct{})}
}
func (w *OwnedRPCWork) Go(fn func()) bool {
	if w == nil {
		go fn()
		return true
	}
	return w.Run(func(context.Context) { fn() })
}
func (w *OwnedRPCWork) Run(fn func(context.Context)) bool {
	if w == nil {
		go fn(context.Background())
		return true
	}
	w.mu.Lock()
	if w.stopping || fn == nil {
		w.rejected = true
		w.mu.Unlock()
		return false
	}
	w.active++
	w.mu.Unlock()
	go func() {
		defer func() {
			w.mu.Lock()
			w.active--
			if w.stopping && w.active == 0 {
				close(w.done)
			}
			w.mu.Unlock()
		}()
		fn(w.ctx)
	}()
	return true
}
func (w *OwnedRPCWork) stop(ctx context.Context) error {
	w.mu.Lock()
	if !w.stopping {
		w.stopping = true
		w.cancel()
		if w.active == 0 {
			close(w.done)
		}
	}
	w.mu.Unlock()
	select {
	case <-w.done:
		w.mu.Lock()
		rejected := w.rejected
		w.mu.Unlock()
		if rejected {
			return errOwnedRPC
		}
		return nil
	case <-ctx.Done():
		return errors.Join(errOwnedRPC, ctx.Err())
	}
}
