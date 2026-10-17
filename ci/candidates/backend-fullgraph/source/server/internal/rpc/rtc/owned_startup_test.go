package rtc

import (
	"context"
	"errors"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/protocol/user"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestOwnedRTCUserActivationIsSynchronousBoundedAndNeverReplaysUnknown(t *testing.T) {
	t.Run("unknown-admission-never-queries-and-cannot-renew", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		known := false
		calls := 0
		start := newOwnedRTCUserActivation(ctx, func() error {
			if !known {
				return errors.New("synthetic publication unavailable")
			}
			return nil
		}, func(context.Context) error { calls++; return nil })
		if start() == nil || calls != 0 {
			t.Fatal("unconfirmed publication exposed user lookup")
		}
		known = true
		if start() == nil || calls != 0 {
			t.Fatal("old failed phase silently renewed its permission")
		}
	})
	t.Run("known-admission-waits-for-real-lookup-before-ready-phase-returns", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		entered, release := make(chan struct{}), make(chan struct{})
		start := newOwnedRTCUserActivation(ctx, func() error { return nil }, func(call context.Context) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-call.Done():
				return call.Err()
			}
		})
		done := make(chan error, 1)
		go func() { done <- start() }()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("startup lookup did not run")
		}
		select {
		case <-done:
			t.Fatal("startup returned before original user check")
		default:
		}
		close(release)
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(time.Second):
			t.Fatal("synchronous phase did not join")
		}
		if start() == nil {
			t.Fatal("startup check replayed")
		}
	})
	t.Run("original-owner-cancel-joins-the-pending-phase", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		entered := make(chan struct{})
		start := newOwnedRTCUserActivation(ctx, func() error { return nil }, func(call context.Context) error { close(entered); <-call.Done(); return call.Err() })
		done := make(chan error, 1)
		go func() { done <- start() }()
		select {
		case <-entered:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("original startup did not enter its lookup")
		}
		cancel()
		select {
		case e := <-done:
			if e == nil {
				t.Fatal("canceled startup became ready")
			}
		case <-time.After(time.Second):
			t.Fatal("phase detached from original owner")
		}
		if start() == nil {
			t.Fatal("canceled identity renewed")
		}
	})
	t.Run("application-error-or-unknown-remains-one-attempt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		failure := errors.New("synthetic unknown RPC outcome")
		start := newOwnedRTCUserActivation(ctx, func() error { return nil }, func(context.Context) error { calls++; return failure })
		if e := start(); !errors.Is(e, failure) {
			t.Fatal("original error semantics lost", e)
		}
		if start() == nil || calls != 1 {
			t.Fatal("unknown startup lookup automatically replayed")
		}
	})
}

type ownedRTCServiceUserFixture struct {
	user.UnimplementedUserServer
	calls atomic.Int32
	want  string
}

func (s *ownedRTCServiceUserFixture) GetDesignateUsers(ctx context.Context, r *user.GetDesignateUsersReq) (*user.GetDesignateUsersResp, error) {
	s.calls.Add(1)
	if len(r.UserIDs) != 1 || r.UserIDs[0] != s.want {
		return nil, errors.New("wrong exact service user")
	}
	return &user.GetDesignateUsersResp{UsersInfo: []*sdkws.UserInfo{{UserID: s.want}}}, nil
}
func TestOwnedRTCOriginalSDKLookupWaitsForActualUserServeAndOwnerCancel(t *testing.T) {
	t.Run("exact-original-wrapper-resolves-only-after-real-Serve", func(t *testing.T) {
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		defer listener.Close()
		conn, e := grpc.DialContext(context.Background(), listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if e != nil {
			t.Fatal(e)
		}
		defer conn.Close()
		service := &ownedRTCServiceUserFixture{want: "synthetic-rtc-owner"}
		server := grpc.NewServer()
		user.RegisterUserServer(server, service)
		defer server.Stop()
		client := rpcclient.UserRpcClient{Client: &ownedRTCReadyUserClient{UserClient: user.NewUserClient(conn)}}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() {
			info, e := client.GetUserInfo(ctx, service.want)
			if e == nil && (info == nil || info.UserID != service.want) {
				e = errors.New("SDK returned another owner")
			}
			done <- e
		}()
		select {
		case <-done:
			t.Fatal("lookup succeeded before actual USER Serve")
		case <-time.After(30 * time.Millisecond):
		}
		served := make(chan error, 1)
		go func() { served <- server.Serve(listener) }()
		defer func() {
			server.Stop()
			select {
			case <-served:
			case <-time.After(time.Second):
				t.Error("actual USER Serve did not join")
			}
		}()
		select {
		case e := <-done:
			if e != nil || service.calls.Load() != 1 {
				t.Fatal("original wrapper lookup failed or repeated", e)
			}
		case <-ctx.Done():
			t.Fatal("actual USER readiness did not release lookup")
		}
	})
	t.Run("cancellation-of-unserved-peer-does-not-create-a-request", func(t *testing.T) {
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		defer listener.Close()
		conn, e := grpc.DialContext(context.Background(), listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if e != nil {
			t.Fatal(e)
		}
		defer conn.Close()
		client := rpcclient.UserRpcClient{Client: &ownedRTCReadyUserClient{UserClient: user.NewUserClient(conn)}}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, e := client.GetUserInfo(ctx, "synthetic-rtc-owner"); done <- e }()
		cancel()
		select {
		case e := <-done:
			if e == nil {
				t.Fatal("unserved USER lookup became success")
			}
		case <-time.After(time.Second):
			t.Fatal("SDK request escaped original owner cancellation")
		}
	})
}
