package zookeeper

import (
	"context"
	"github.com/go-zookeeper/zk"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
	"net"
	"sync"
	"testing"
	"time"
)

// Only the external ZK driver boundary is a fixture. The original owned client,
// watch/refresh bridge, per-dial resolvers and both gRPC connections are real.
// This is not evidence of a real ZK deployment or fourteen-role admission.
type lateTargetZK struct {
	*ownedTestZK
	mu     sync.Mutex
	target string
}

func (f *lateTargetZK) ChildrenW(string) ([]string, *zk.Stat, <-chan zk.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.target == "" {
		return nil, &zk.Stat{}, nil, nil
	}
	return []string{"one"}, &zk.Stat{}, nil, nil
}
func (f *lateTargetZK) Children(string) ([]string, *zk.Stat, error) {
	rows, s, _, e := f.ChildrenW("")
	return rows, s, e
}
func (f *lateTargetZK) Get(string) ([]byte, *zk.Stat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []byte(f.target), &zk.Stat{}, nil
}
func (f *lateTargetZK) dial([]string, time.Duration, Logger) (ownedZKConnection, <-chan zk.Event, error) {
	return f, f.events, nil
}
func waitResolverCount(t *testing.T, c *ZkClient, name string, want int) {
	t.Helper()
	end := time.NewTimer(time.Second)
	defer end.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		c.lock.Lock()
		count := len(c.resolvers[name])
		c.lock.Unlock()
		if count == want {
			return
		}
		select {
		case <-end.C:
			t.Fatal("original resolver instance count did not settle", count, want)
		case <-tick.C:
		}
	}
}
func TestOwnedDiscoveryLateTargetUpdatesEveryOriginalConnectionAndCloseIsPerInstance(t *testing.T) {
	f := &lateTargetZK{ownedTestZK: newOwnedTestZK()}
	client, e := newClientContext(context.Background(), []string{"fixture.invalid:2181"}, "owned-late-target", f.dial, WithLogger(ownedTestLogger{}), WithFreq(time.Hour), WithRoundRobin())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if e := client.CloseOwned(ctx); e != nil {
			t.Error("owned registry join incomplete", e)
		}
	})
	first, e := client.GetConn(context.Background(), "fixture", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if e != nil {
		t.Fatal(e)
	}
	second, e := client.GetConn(context.Background(), "fixture", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if e != nil {
		t.Fatal(e)
	}
	t.Run("two-initially-empty-connections-retain-two-original-resolvers", func(t *testing.T) {
		if first == second {
			t.Fatal("connection identity substituted")
		}
		waitResolverCount(t, client, "fixture", 2)
	})
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	service := grpc.NewServer()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("fixture", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(service, healthServer)
	joined := make(chan error, 1)
	go func() { joined <- service.Serve(listener) }()
	t.Cleanup(func() {
		service.Stop()
		listener.Close()
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Error("real gRPC fixture Serve not joined")
		}
	})
	t.Run("actual-late-target-event-reaches-both-real-RPC-connections", func(t *testing.T) {
		f.mu.Lock()
		f.target = listener.Addr().String()
		f.mu.Unlock()
		f.events <- zk.Event{Type: zk.EventNodeChildrenChanged, Path: "/owned-late-target/fixture"}
		for _, conn := range []*grpc.ClientConn{first, second} {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			reply, e := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: "fixture"}, grpc.WaitForReady(true))
			cancel()
			if e != nil || reply.GetStatus() != healthpb.HealthCheckResponse_SERVING {
				t.Fatal("live original connection missed late registration", e)
			}
		}
	})
	t.Run("closing-one-removes-only-its-original-instance", func(t *testing.T) {
		client.CloseConn(first)
		waitResolverCount(t, client, "fixture", 1)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, e := healthpb.NewHealthClient(second).Check(ctx, &healthpb.HealthCheckRequest{Service: "fixture"}); e != nil {
			t.Fatal("sibling resolver lost with first connection", e)
		}
	})
	t.Run("owner-join-closes-the-remaining-real-connection", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if e := client.CloseOwned(ctx); e != nil {
			t.Fatal(e)
		}
		if first.GetState() != connectivity.Shutdown || second.GetState() != connectivity.Shutdown || f.closes.Load() != 1 {
			t.Fatal("original close ownership lost")
		}
		waitResolverCount(t, client, "fixture", 0)
	})
	t.Run("UpdateState-can-reenter-original-instance-close-without-set-lock", func(t *testing.T) {
		owner := &ZkClient{lock: &sync.Mutex{}, logger: ownedTestLogger{}, localConns: map[string][]*grpc.ClientConn{}, resolvers: map[string]map[*Resolver]struct{}{}}
		callback := &closingResolverConn{}
		r := &Resolver{owner: owner, serviceName: "reentrant", cc: callback, getConnsRemote: func(string) ([]resolver.Address, error) { return []resolver.Address{{Addr: "127.0.0.1:1"}}, nil }}
		callback.close = r.Close
		owner.resolvers["reentrant"] = map[*Resolver]struct{}{r: {}}
		done := make(chan struct{})
		go func() { owner.flushResolverAndDeleteLocal("reentrant"); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("UpdateState/Close callback deadlocked on collection lock")
		}
		waitResolverCount(t, owner, "reentrant", 0)
	})
	t.Run("initial-empty-snapshots-are-visible-to-the-only-late-registration-event", func(t *testing.T) {
		release := make(chan struct{})
		var releaseOnce sync.Once
		f := &blockedEmptyZK{lateTargetZK: &lateTargetZK{ownedTestZK: newOwnedTestZK()}, captured: make(chan struct{}, 2), release: release}
		eventSeen := make(chan struct{})
		eventLogger := &initialEventLogger{seen: eventSeen}
		owner, e := newClientContext(context.Background(), []string{"fixture.invalid:2181"}, "owned-initial-race", f.dial, WithLogger(eventLogger), WithFreq(time.Hour), WithRoundRobin())
		if e != nil {
			t.Fatal(e)
		}
		results := make(chan originalDialResult, 2)
		received := 0
		defer func() {
			releaseOnce.Do(func() { close(release) })
			end, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if e := owner.CloseOwned(end); e != nil {
				t.Error("blocked original dials did not join", e)
			}
			for received < 2 {
				select {
				case result := <-results:
					received++
					if result.conn != nil {
						owner.CloseConn(result.conn)
					}
				case <-end.Done():
					t.Error("original dial goroutine did not return")
					return
				}
			}
		}()
		for n := 0; n < 2; n++ {
			go func() {
				conn, e := owner.GetConn(context.Background(), "initial-race", grpc.WithTransportCredentials(insecure.NewCredentials()))
				results <- originalDialResult{conn, e}
			}()
		}
		// Both driver reads have already captured their original empty snapshot.
		// Keep them blocked until the sole late registration has been announced.
		for n := 0; n < 2; n++ {
			select {
			case <-f.captured:
			case <-time.After(time.Second):
				t.Fatal("original initial empty snapshot was not captured")
			}
		}
		waitResolverCount(t, owner, "initial-race", 2)
		f.mu.Lock()
		f.target = listener.Addr().String()
		f.mu.Unlock()
		f.events <- zk.Event{Type: zk.EventNodeChildrenChanged, Path: "/owned-initial-race/initial-race"}
		select {
		case <-eventSeen:
		case <-time.After(time.Second):
			t.Fatal("original watch did not begin its late registration broadcast")
		}
		releaseOnce.Do(func() { close(release) })
		for received < 2 {
			select {
			case result := <-results:
				received++
				if result.err != nil || result.conn == nil {
					t.Fatal("original connection construction failed", result.err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				reply, e := healthpb.NewHealthClient(result.conn).Check(ctx, &healthpb.HealthCheckRequest{Service: "fixture"}, grpc.WaitForReady(true))
				cancel()
				if e != nil || reply.GetStatus() != healthpb.HealthCheckResponse_SERVING {
					t.Fatal("initial empty snapshot swallowed the sole late registration", e)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("original dial did not return")
			}
		}
	})
}

type closingResolverConn struct{ close func() }

func (c *closingResolverConn) UpdateState(resolver.State) error                   { c.close(); return nil }
func (*closingResolverConn) ReportError(error)                                    {}
func (*closingResolverConn) NewAddress([]resolver.Address)                        {}
func (*closingResolverConn) NewServiceConfig(string)                              {}
func (*closingResolverConn) ParseServiceConfig(string) *serviceconfig.ParseResult { return nil }

// Captures and blocks only the two initial empty Children results. The watch
// event goes through the original production event bridge, not a direct flush.
type blockedEmptyZK struct {
	*lateTargetZK
	gate     sync.Mutex
	blocked  int
	captured chan struct{}
	release  <-chan struct{}
}

func (f *blockedEmptyZK) Children(path string) ([]string, *zk.Stat, error) {
	rows, stat, e := f.lateTargetZK.Children(path)
	f.gate.Lock()
	block := e == nil && len(rows) == 0 && f.blocked < 2
	if block {
		f.blocked++
	}
	f.gate.Unlock()
	if block {
		f.captured <- struct{}{}
		<-f.release
	}
	return rows, stat, e
}
func (f *blockedEmptyZK) dial([]string, time.Duration, Logger) (ownedZKConnection, <-chan zk.Event, error) {
	return f, f.events, nil
}

type originalDialResult struct {
	conn *grpc.ClientConn
	err  error
}

// This observes the original watch-to-flush boundary; it never calls flush or
// supplies resolver results, and keeps both initial snapshots blocked until it.
type initialEventLogger struct {
	seen chan struct{}
	once sync.Once
}

func (l *initialEventLogger) Printf(format string, _ ...interface{}) {
	if format == "start flush %s" {
		l.once.Do(func() { close(l.seen) })
	}
}
