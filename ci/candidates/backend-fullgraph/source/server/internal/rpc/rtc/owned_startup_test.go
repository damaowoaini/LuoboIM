package rtc

import (
	"context"
	"errors"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/protocol/user"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/pkg/common/rpcmiddleware"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"net"
	"strings"
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

type rtcActivationPrivateCodeError struct{ code int }

func (e rtcActivationPrivateCodeError) Error() string { panic("private cause must not be serialized") }
func (e rtcActivationPrivateCodeError) Code() int     { return e.code }

type rtcActivationMiddlewareUserFixture struct {
	user.UnimplementedUserServer
	calls atomic.Int32
	want  string
	deny  bool
}

func (f *rtcActivationMiddlewareUserFixture) GetDesignateUsers(ctx context.Context, r *user.GetDesignateUsersReq) (*user.GetDesignateUsersResp, error) {
	f.calls.Add(1)
	if len(r.UserIDs) != 1 || r.UserIDs[0] != f.want || ctx.Value(constant.OpUserID) != f.want {
		return nil, errs.ErrArgs.Wrap("synthetic exact metadata required")
	}
	if f.deny {
		return nil, errs.ErrNoPermission.Wrap("synthetic private permission details")
	}
	return &user.GetDesignateUsersResp{}, nil
}

func TestOwnedRTCActivationPhasePreservesRealSDKCauseWithoutPrivateTextOrReplay(t *testing.T) {
	check := func(t *testing.T, err error, phase ownedRTCActivationPhase, cause error, class string) {
		t.Helper()
		var failure *ownedRTCActivationFailure
		if !errors.Is(err, errOwnedRTCUserActivation) || !errors.As(err, &failure) || failure.phase != phase || failure.cause != cause || failure.causeClass != class {
			t.Fatal("closed phase or original error identity changed")
		}
		if cause != nil && !errors.Is(err, cause) {
			t.Fatal("original cause is not retained")
		}
	}
	t.Run("parent-and-admission-guards-retain-cause-without-lookup", func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		cancel()
		queries := 0
		start := newOwnedRTCUserActivation(parent, func() error { return nil }, func(context.Context) error { queries++; return nil })
		check(t, start(), rtcActivationParent, context.Canceled, "context-cancelled")
		check(t, start(), rtcActivationAttempted, nil, "none")
		if queries != 0 {
			t.Fatal("canceled parent exposed a lookup")
		}
		live, stop := context.WithCancel(context.Background())
		defer stop()
		check(t, newOwnedRTCUserActivation(nil, func() error { return nil }, func(context.Context) error { return nil })(), rtcActivationParent, nil, "none")
		check(t, newOwnedRTCUserActivation(context.Background(), func() error { return nil }, func(context.Context) error { return nil })(), rtcActivationParent, nil, "none")
		check(t, newOwnedRTCUserActivation(live, nil, func(context.Context) error { return nil })(), rtcActivationCallbacks, nil, "none")
		private := rtcActivationPrivateCodeError{code: errs.ErrUserIDNotFound.Code()}
		err := newOwnedRTCUserActivation(live, func() error { return private }, func(context.Context) error { queries++; return nil })()
		check(t, err, rtcActivationAdmitted, private, "sdk-user-missing")
		if strings.Contains(err.Error(), "synthetic") || queries != 0 {
			t.Fatal("admission guard serialized private data or queried")
		}
		unknown := rtcActivationPrivateCodeError{code: 987654321}
		err = newOwnedRTCUserActivation(live, func() error { return nil }, func(context.Context) error { return unknown })()
		check(t, err, rtcActivationLookup, unknown, "other")
		if strings.Contains(err.Error(), "987654321") {
			t.Fatal("unknown numeric code escaped")
		}
		callParent, callCancel := context.WithCancel(context.Background())
		defer callCancel()
		err = newOwnedRTCUserActivation(callParent, func() error { return nil }, func(context.Context) error { callCancel(); return nil })()
		check(t, err, rtcActivationCallContext, context.Canceled, "context-cancelled")
	})
	t.Run("real-middleware-and-sdk-errors-remain-one-original-lookup", func(t *testing.T) {
		for _, deny := range []bool{false, true} {
			func() {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				fixture := &rtcActivationMiddlewareUserFixture{want: "synthetic-rtc-owner", deny: deny}
				server := grpc.NewServer(rpcmiddleware.GrpcServer())
				user.RegisterUserServer(server, fixture)
				served := make(chan error, 1)
				go func() { served <- server.Serve(listener) }()
				defer func() {
					server.Stop()
					select {
					case <-served:
					case <-time.After(time.Second):
						t.Error("real USER Serve did not join")
					}
				}()
				connection, err := grpc.DialContext(context.Background(), listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), rpcmiddleware.GrpcClient())
				if err != nil {
					t.Fatal(err)
				}
				defer connection.Close()
				client := rpcclient.UserRpcClient{Client: &ownedRTCReadyUserClient{UserClient: user.NewUserClient(connection)}}
				parent, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				parent = context.WithValue(parent, constant.OperationID, "rtc-activation-source-control")
				parent = context.WithValue(parent, constant.OpUserID, fixture.want)
				var original error
				lookups := 0
				start := newOwnedRTCUserActivation(parent, func() error { return nil }, func(call context.Context) error {
					lookups++
					_, original = client.GetUserInfo(call, fixture.want)
					return original
				})
				failure := start()
				class := "sdk-user-missing"
				want := errs.ErrUserIDNotFound.Code()
				if deny {
					class = "permission"
					want = errs.ErrNoPermission.Code()
				}
				check(t, failure, rtcActivationLookup, original, class)
				code, ok := errs.Unwrap(original).(interface{ Code() int })
				if !ok || code.Code() != want || fixture.calls.Load() != 1 || lookups != 1 {
					t.Fatal("actual SDK/middleware code or one exact request changed")
				}
				if strings.Contains(failure.Error(), fixture.want) || strings.Contains(failure.Error(), "private permission") {
					t.Fatal("private RPC details escaped typed startup failure")
				}
				check(t, start(), rtcActivationAttempted, nil, "none")
				if fixture.calls.Load() != 1 || lookups != 1 {
					t.Fatal("failed original lookup was replayed")
				}
			}()
		}
	})
	t.Run("real-unserved-peer-cancellation-retains-lookup-cause", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		connection, err := grpc.DialContext(context.Background(), listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), rpcmiddleware.GrpcClient())
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		client := rpcclient.UserRpcClient{Client: &ownedRTCReadyUserClient{UserClient: user.NewUserClient(connection)}}
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		parent = context.WithValue(parent, constant.OperationID, "rtc-activation-cancel-control")
		parent = context.WithValue(parent, constant.OpUserID, "synthetic-rtc-owner")
		entered := make(chan struct{})
		done := make(chan error, 1)
		var original error
		lookups := 0
		start := newOwnedRTCUserActivation(parent, func() error { return nil }, func(call context.Context) error {
			lookups++
			close(entered)
			_, original = client.GetUserInfo(call, "synthetic-rtc-owner")
			return original
		})
		go func() { done <- start() }()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("original SDK lookup not entered")
		}
		select {
		case <-done:
			t.Fatal("unserved lookup completed without cancellation")
		default:
		}
		cancel()
		select {
		case failure := <-done:
			check(t, failure, rtcActivationLookup, original, "grpc-cancelled")
		case <-time.After(time.Second):
			t.Fatal("original cancelled lookup did not join")
		}
		check(t, start(), rtcActivationAttempted, nil, "none")
		if lookups != 1 {
			t.Fatal("cancelled original request replayed")
		}
	})
}
