package startrpc

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"google.golang.org/grpc"
)

var errOwnedRPC = errors.New("owned RPC lifecycle is unconfirmed")
var ownedRPCScopes sync.Map // exact *grpc.Server -> one original scope, never a role lookup

type ownedResource struct {
	name  string
	close func(context.Context) error
}
type ownedRPCScope struct {
	role           string
	mode           string
	workers        *OwnedRPCWork
	mu             sync.Mutex
	resources      map[string]ownedResource
	admission      *ownedRPCAdmissionHooks
	sealed         bool
	database       string
	mongoSource    *ownedmongo.Observer
	databaseSHA256 string
	closeOnce      sync.Once
	closeDone      chan struct{}
	closedNames    []string
	closeErr       error
}

func ownedScope(server *grpc.Server) *ownedRPCScope {
	if value, ok := ownedRPCScopes.Load(server); ok {
		return value.(*ownedRPCScope)
	}
	return nil
}
func IsOwnedRPC(server *grpc.Server) bool { return ownedScope(server) != nil }
func OwnedRPCMode(server *grpc.Server) string {
	if s := ownedScope(server); s != nil {
		return s.mode
	}
	return ""
}
func CancelOwnedRPC(server *grpc.Server) {
	if s := ownedScope(server); s != nil {
		s.workers.cancel()
	}
}
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
	allowed := scope.resourceNames()
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
	if scope.sealed || name == "" || !ownedHash(scope.databaseSHA256) || ownedSHA([]byte(name)) != scope.databaseSHA256 || (scope.database != "" && scope.database != name) {
		return errOwnedRPC
	}
	scope.database = name
	return nil
}
func ownedResourceNames(role string) []string {
	switch role {
	case "push":
		return []string{"push-consumer", "push-provider", "callback-http", "redis"}
	case "rpc-msg":
		return []string{"sensitive-word-workers", "kafka-producers", "redis", "mongo"}
	case "rpc-third":
		return []string{"object-provider", "redis", "mongo"}
	case "rpc-conversation", "rpc-user", "rpc-friend", "rpc-encryption", "rpc-rtc":
		return []string{"redis", "mongo"}
	case "msggateway":
		return []string{"websocket-runtime", "callback-http", "redis"}
	case "rpc-auth":
		return []string{"redis"}
	case "rpc-group":
		return []string{"group-runtime"}
	default:
		return nil // placeholder/consumer/object-provider loops have separate owners
	}
}

// Only this finite role has mode-dependent live resources. Required delivery
// does not open the raw consumer; the same RPC retains token administration.
func (s *ownedRPCScope) resourceNames() []string {
	if s.role == "push" && s.mode == "required" {
		return []string{"redis"}
	}
	return ownedResourceNames(s.role)
}
func (s *ownedRPCScope) seal(database string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	expected := s.resourceNames()
	if s.sealed || expected == nil || !ownedHash(s.databaseSHA256) || ownedSHA([]byte(database)) != s.databaseSHA256 || len(s.resources) != len(expected) {
		return errOwnedRPC
	}
	for _, name := range expected {
		if s.resources[name].close == nil {
			return errOwnedRPC
		}
	}
	// Every binding is checked against the original private bootstrap before
	// constructors may initialize indexes/routes. Seal checks it again.
	// Auth uses no Mongo client; it binds the original logical tenant database,
	// without claiming that this role opened or read that Mongo database.
	if s.role != "rpc-auth" && s.role != "push" && s.database != database {
		return errOwnedRPC
	}
	if (s.role == "msggateway" || (s.role == "push" && s.mode == "legacy")) && s.admission == nil {
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
	for _, name := range s.resourceNames() {
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
	namespaceDeferred  bool
	namespaceActivated bool
	namespacePending   []func(context.Context)
	ctx                context.Context
	cancel             context.CancelFunc
	mu                 sync.Mutex
	stopping           bool
	active             int
	done               chan struct{}
	rejected           bool
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
	if w.namespaceDeferred && !w.namespaceActivated {
		w.namespacePending = append(w.namespacePending, fn)
		w.mu.Unlock()
		return true
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
		w.namespacePending = nil
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

// Only the actual private required startup selects this phase, before any real
// constructor receives the work owner. Original legacy/default behavior is
// immediate. Queued functions are not reported as started or joined workers.
func (w *OwnedRPCWork) deferUntilNamespaceAdmission() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.namespaceDeferred = true
}
func (w *OwnedRPCWork) activateAfterNamespaceAdmission() error {
	if w == nil {
		return errOwnedRPC
	}
	w.mu.Lock()
	if !w.namespaceDeferred {
		w.mu.Unlock()
		return nil
	}
	if w.stopping || w.ctx.Err() != nil || w.namespaceActivated {
		w.mu.Unlock()
		return errOwnedRPC
	}
	w.namespaceActivated = true
	pending := w.namespacePending
	w.namespacePending = nil
	w.mu.Unlock()
	for _, fn := range pending {
		if !w.Run(fn) {
			return errOwnedRPC
		}
	}
	return nil
}
