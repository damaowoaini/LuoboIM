package zookeeper

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-zookeeper/zk"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

type ownedTestLogger struct{}

func (ownedTestLogger) Printf(string, ...interface{}) {}

type ownedTestZK struct {
	events         chan zk.Event
	closeOnce      sync.Once
	closeRequested chan struct{}
	releaseClose   <-chan struct{}
	closes         atomic.Int32
	reads          atomic.Int32
	exists         func(string) (bool, *zk.Stat, error)
	authError      error
}

func newOwnedTestZK() *ownedTestZK {
	f := &ownedTestZK{events: make(chan zk.Event, 2), closeRequested: make(chan struct{})}
	f.events <- zk.Event{State: zk.StateConnected}
	return f
}
func (f *ownedTestZK) AddAuth(string, []byte) error { return f.authError }
func (f *ownedTestZK) Exists(path string) (bool, *zk.Stat, error) {
	if f.exists != nil {
		return f.exists(path)
	}
	return true, &zk.Stat{}, nil
}
func (f *ownedTestZK) Create(string, []byte, int32, []zk.ACL) (string, error) { return "/owned", nil }
func (f *ownedTestZK) ChildrenW(string) ([]string, *zk.Stat, <-chan zk.Event, error) {
	f.reads.Add(1)
	return []string{"one"}, &zk.Stat{}, nil, nil
}
func (f *ownedTestZK) Children(string) ([]string, *zk.Stat, error) {
	return []string{"one"}, &zk.Stat{}, nil
}
func (f *ownedTestZK) Get(string) ([]byte, *zk.Stat, error) {
	return []byte("127.0.0.1:1"), &zk.Stat{}, nil
}
func (f *ownedTestZK) CreateProtectedEphemeralSequential(string, []byte, []zk.ACL) (string, error) {
	return "/owned/one", nil
}
func (f *ownedTestZK) Delete(string, int32) error { return nil }
func (f *ownedTestZK) Close() {
	f.closeOnce.Do(func() {
		f.closes.Add(1)
		close(f.closeRequested)
		if f.releaseClose == nil {
			close(f.events)
		} else {
			go func() { <-f.releaseClose; close(f.events) }()
		}
	})
}
func (f *ownedTestZK) dial([]string, time.Duration, Logger) (ownedZKConnection, <-chan zk.Event, error) {
	return f, f.events, nil
}
func ownedTestClient(t *testing.T, f *ownedTestZK) *ZkClient {
	t.Helper()
	client, err := newClientContext(context.Background(), []string{"fixture.invalid:2181"}, "owned-fixture", f.dial, WithLogger(ownedTestLogger{}), WithFreq(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.CloseOwned(ctx); err != nil {
			t.Error(err)
		}
	})
	return client
}

func TestOwnedDiscoveryCloseJoinsRegistryAndActualGRPCConnections(t *testing.T) {
	f := newOwnedTestZK()
	client := ownedTestClient(t, f)
	connection, err := client.GetConn(context.Background(), "fixture", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.CloseOwned(ctx); err != nil {
		t.Fatal(err)
	}
	if connection.GetState() != connectivity.Shutdown || f.closes.Load() != 1 {
		t.Fatal("actual connection was not closed once")
	}
	if _, err := client.GetConn(context.Background(), "fixture", grpc.WithTransportCredentials(insecure.NewCredentials())); !errors.Is(err, ErrClosed) {
		t.Fatal("closed owner admitted another dial", err)
	}
	if err := client.CloseOwned(ctx); err != nil || f.closes.Load() != 1 {
		t.Fatal("second close did not await original completion", err)
	}
}

func TestOwnedDiscoveryTimeoutCannotClaimDriverJoinOrStartSecondClose(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	f := newOwnedTestZK()
	f.releaseClose = release
	client := ownedTestClient(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- client.CloseOwned(ctx) }()
	<-f.closeRequested
	cancel()
	if err := <-result; !errors.Is(err, ErrClosePending) {
		t.Fatal("unclosed driver was called complete", err)
	}
	select {
	case <-client.owned.closeDone:
		t.Fatal("driver event stream still live")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	joined, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	if err := client.CloseOwned(joined); err != nil || f.closes.Load() != 1 {
		t.Fatal("original close could not finish", err)
	}
}

func TestOwnedDiscoveryPartialInitializationClosesTheOriginalConnection(t *testing.T) {
	t.Run("auth-error", func(t *testing.T) {
		f := newOwnedTestZK()
		f.authError = errors.New("synthetic auth rejection")
		client, err := newClientContext(context.Background(), []string{"fixture.invalid:2181"}, "owned-fixture", f.dial, WithLogger(ownedTestLogger{}), WithUserNameAndPassword("synthetic", "synthetic"))
		if client != nil || err == nil || f.closes.Load() != 1 {
			t.Fatal("partial auth initialization retained driver", err)
		}
		select {
		case _, ok := <-f.events:
			if ok {
				t.Fatal("driver not joined")
			}
		default:
			t.Fatal("driver not joined")
		}
	})
	t.Run("cancel-during-root-rpc", func(t *testing.T) {
		f := newOwnedTestZK()
		entered := make(chan struct{})
		var once sync.Once
		f.exists = func(string) (bool, *zk.Stat, error) {
			once.Do(func() { close(entered) })
			<-f.closeRequested
			return false, nil, zk.ErrClosing
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result := make(chan error, 1)
		go func() {
			client, err := newClientContext(ctx, []string{"fixture.invalid:2181"}, "owned-fixture", f.dial, WithLogger(ownedTestLogger{}))
			if client != nil {
				result <- errors.New("canceled initialization became ready")
				return
			}
			result <- err
		}()
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("actual initialization did not reach root RPC")
		}
		cancel()
		select {
		case err := <-result:
			if err == nil || f.closes.Load() != 1 {
				t.Fatal("cancel did not close exact driver", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("original startup RPC did not unblock")
		}
	})
}

func TestOwnedDiscoverySameSchemeInstancesKeepTheirOwnResolverAndClose(t *testing.T) {
	first, second := newOwnedTestZK(), newOwnedTestZK()
	a, b := ownedTestClient(t, first), ownedTestClient(t, second)
	connA, err := a.GetConn(context.Background(), "fixture", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	connB, err := b.GetConn(context.Background(), "fixture", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := a.CloseOwned(ctx); err != nil {
		t.Fatal(err)
	}
	if connA.GetState() != connectivity.Shutdown || connB.GetState() == connectivity.Shutdown || second.closes.Load() != 0 {
		t.Fatal("one registry retired the other owner")
	}
	if err := b.CloseOwned(ctx); err != nil || connB.GetState() != connectivity.Shutdown {
		t.Fatal("second owner did not close its own connection", err)
	}
}

func TestOwnedDiscoveryCloseCancelsAndJoinsAnUnreturnedBlockingDial(t *testing.T) {
	for _, method := range []string{"one", "all"} {
		t.Run(method, func(t *testing.T) {
			f := newOwnedTestZK()
			client := ownedTestClient(t, f)
			entered, dialStopped := make(chan struct{}), make(chan struct{})
			var enterOnce, stoppedOnce sync.Once
			var active atomic.Int32
			dialer := func(ctx context.Context, _ string) (net.Conn, error) {
				active.Add(1)
				defer func() { active.Add(-1); stoppedOnce.Do(func() { close(dialStopped) }) }()
				enterOnce.Do(func() { close(entered) })
				<-ctx.Done()
				return nil, ctx.Err()
			}
			result := make(chan error, 1)
			go func() {
				options := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(), grpc.WithContextDialer(dialer)}
				if method == "one" {
					connection, err := client.GetConn(context.Background(), "fixture", options...)
					if connection != nil {
						_ = connection.Close()
					}
					result <- err
				} else {
					connections, err := client.GetConns(context.Background(), "fixture", options...)
					for _, connection := range connections {
						_ = connection.Close()
					}
					result <- err
				}
			}()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("actual blocking gRPC dial did not begin")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := client.CloseOwned(ctx); err != nil {
				t.Fatal("owner close did not join its unreturned dial", err)
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("owner cancellation did not reach actual caller", err)
				}
			case <-ctx.Done():
				t.Fatal("unreturned dial outlived successful owner close")
			}
			select {
			case <-dialStopped:
				if active.Load() != 0 || f.closes.Load() != 1 {
					t.Fatal("dial or original ZK connection remained active")
				}
			case <-ctx.Done():
				t.Fatal("actual transport dial did not exit")
			}
		})
	}
}
