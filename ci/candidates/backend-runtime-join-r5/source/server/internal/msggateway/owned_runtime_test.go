package msggateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pu "github.com/OpenIMSDK/protocol/user"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/golang-jwt/jwt/v4"
	"github.com/gorilla/websocket"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	callbackhttp "github.com/openimsdk/open-im-server/v3/pkg/common/http"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"google.golang.org/grpc"
)

type ownedGatewayStatus struct {
	pu.UserClient
	online, offline         chan struct{}
	release                 <-chan struct{}
	onlineOnce, offlineOnce sync.Once
	invalid                 atomic.Bool
}

func (s *ownedGatewayStatus) SetUserStatus(ctx context.Context, req *pu.SetUserStatusReq, _ ...grpc.CallOption) (*pu.SetUserStatusResp, error) {
	if req.UserID != "owned-gateway-user" || req.PlatformID != 2 || mcontext.GetOpUserID(ctx) != "owned-gateway-user" {
		s.invalid.Store(true)
	}
	if req.Status == constant.Online {
		s.onlineOnce.Do(func() { close(s.online) })
	} else {
		if ctx.Err() != nil {
			s.invalid.Store(true)
		} // final presence has its own bounded original identity
		s.offlineOnce.Do(func() { close(s.offline) })
		if s.release != nil {
			select {
			case <-s.release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return &pu.SetUserStatusResp{}, nil
}

type ownedGatewayMessage struct {
	MessageHandler
	entered, canceled chan struct{}
	release           <-chan struct{}
	calls             atomic.Int32
	invalid           atomic.Bool
}

func (m *ownedGatewayMessage) GetSeq(ctx context.Context, req *Req) ([]byte, error) {
	if mcontext.GetOpUserID(ctx) != "owned-gateway-user" || mcontext.GetOperationID(ctx) != "original-request" || req.SendID != "owned-gateway-user" {
		m.invalid.Store(true)
	}
	m.calls.Add(1)
	close(m.entered)
	<-ctx.Done()
	close(m.canceled)
	<-m.release
	return nil, ctx.Err()
}
func ownedGatewayAwait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("real owned gateway boundary did not complete")
	}
}
func ownedGatewayFixture(t *testing.T, status *ownedGatewayStatus) (*ownedWebsocketRuntime, string) {
	t.Helper()
	previous := config.Config
	t.Cleanup(func() { config.Config = previous })
	config.Config.Secret = "synthetic-owned-gateway-secret"
	config.Config.Rpc.ListenIP = "127.0.0.1"
	config.Config.Envs.Discovery = "fixture"
	config.Config.MultiLoginPolicy = constant.DefalutNotKick
	config.Config.Manager.UserID = nil
	config.Config.IMAdmin.UserID = nil
	config.Config.Callback.CallbackUserOnline.Enable = false
	config.Config.Callback.CallbackUserOffline.Enable = false
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, tokenverify.BuildClaims("owned-gateway-user", 2, 1)).SignedString([]byte(config.Config.Secret))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := NewWsServer(WithPort(0), WithMaxConnNum(8), WithHandshakeTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ws.cache = &registrationCache{values: map[string]int{token: constant.NormalToken}}
	ws.userClient = &rpcclient.UserRpcClient{Client: status}
	callbacks, err := callbackhttp.NewOwnedCallbackHTTP()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	owned, err := startOwnedWebsocket(ctx, ws, callbacks, cancel)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		end, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if err := owned.Stop(end); err != nil {
			t.Error("actual owned gateway cleanup not joined", err)
		}
		if err := callbacks.Close(); err != nil {
			t.Error(err)
		}
	})
	return owned, token
}
func ownedGatewayDial(t *testing.T, o *ownedWebsocketRuntime, token string) *websocket.Conn {
	t.Helper()
	endpoint := url.URL{Scheme: "ws", Host: o.listener.Addr().String(), Path: "/", RawQuery: url.Values{"token": {token}, "sendID": {"owned-gateway-user"}, "platformID": {"2"}, "operationID": {"original-handshake"}}.Encode()}
	conn, _, err := websocket.DefaultDialer.Dial(endpoint.String(), nil)
	if err != nil {
		t.Fatal("original authenticated websocket handshake failed", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	kind, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var configMessage Resp
	if kind != websocket.BinaryMessage || o.ws.Decode(raw, &configMessage) != nil || configMessage.ReqIdentifier != WSServerConfigMsg {
		t.Fatal("original production config message missing")
	}
	return conn
}

func TestOwnedGatewayRealHTTPAndSocketStopWaitForOriginalRequestReaderAndPresence(t *testing.T) {
	releaseRequest, releasePresence := make(chan struct{}), make(chan struct{})
	var requestOnce, presenceOnce sync.Once
	status := &ownedGatewayStatus{online: make(chan struct{}), offline: make(chan struct{}), release: releasePresence}
	o, token := ownedGatewayFixture(t, status)
	t.Cleanup(func() {
		requestOnce.Do(func() { close(releaseRequest) })
		presenceOnce.Do(func() { close(releasePresence) })
	})
	response, err := http.Get("http://" + o.listener.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatal("socket admission opened before actual full start")
	}
	if err := o.enable(); err != nil {
		t.Fatal(err)
	}
	message := &ownedGatewayMessage{entered: make(chan struct{}), canceled: make(chan struct{}), release: releaseRequest}
	o.ws.MessageHandler = message
	conn := ownedGatewayDial(t, o, token)
	ownedGatewayAwait(t, status.online)
	req := &Req{ReqIdentifier: WSGetNewestSeq, SendID: "owned-gateway-user", OperationID: "original-request", MsgIncr: "1"}
	raw, err := o.ws.Encode(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, raw); err != nil {
		t.Fatal(err)
	}
	ownedGatewayAwait(t, message.entered)
	if err := o.stopAdmission(context.Background()); err != nil {
		t.Fatal(err)
	}
	ownedGatewayAwait(t, message.canceled)
	done := make(chan error, 1)
	go func() {
		end, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		done <- o.Stop(end)
	}()
	select {
	case err := <-done:
		t.Fatal("stop falsely joined an active original request", err)
	default:
	}
	requestOnce.Do(func() { close(releaseRequest) })
	ownedGatewayAwait(t, status.offline)
	select {
	case err := <-done:
		t.Fatal("stop falsely joined an unfinished original presence call", err)
	default:
	}
	presenceOnce.Do(func() { close(releasePresence) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("actual socket/callback loop did not join")
	}
	if message.calls.Load() != 1 || message.invalid.Load() || status.invalid.Load() || o.ws.onlineUserConnNum.Load() != 0 {
		t.Fatal("shutdown changed original request/identity or leaked registered connection")
	}
	if err := o.enable(); err == nil {
		t.Fatal("stopped listener was reopened")
	}
	if _, err := net.DialTimeout("tcp", o.listener.Addr().String(), time.Second); err == nil {
		t.Fatal("original listener remained open")
	}
}

func TestOwnedGatewayUnexpectedListenerFailureCancelsTheSameOwnerAndCannotReenable(t *testing.T) {
	status := &ownedGatewayStatus{online: make(chan struct{}), offline: make(chan struct{})}
	o, _ := ownedGatewayFixture(t, status)
	if err := o.enable(); err != nil {
		t.Fatal(err)
	}
	if err := o.listener.Close(); err != nil {
		t.Fatal(err)
	}
	ownedGatewayAwait(t, o.messages.Done())
	if o.Failure() == nil {
		t.Fatal("unexpected Serve failure was lost to signal cancellation")
	}
	if err := o.enable(); err == nil {
		t.Fatal("failed server borrowed a fresh ready gate")
	}
	end, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := o.Stop(end); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatal("original Serve/socket resources failed cleanup", err)
	}
}

func TestOwnedGatewayOccupiedListenerFailsWithoutReadyAndClosesOnlyItsPartialOwner(t *testing.T) {
	previous := config.Config.Rpc.ListenIP
	config.Config.Rpc.ListenIP = "127.0.0.1"
	defer func() { config.Config.Rpc.ListenIP = previous }()
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	ws, err := NewWsServer(WithPort(occupied.Addr().(*net.TCPAddr).Port))
	if err != nil {
		t.Fatal(err)
	}
	callbacks, err := callbackhttp.NewOwnedCallbackHTTP()
	if err != nil {
		t.Fatal(err)
	}
	defer callbacks.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner, err := startOwnedWebsocket(ctx, ws, callbacks, cancel)
	if err == nil || owner == nil || owner.server != nil || ws.owned != nil {
		t.Fatal("partial listener construction pretended successful runtime", err)
	}
	if err := owner.enable(); err == nil {
		t.Fatal("partial owner announced ready")
	}
	if err := owner.Stop(context.Background()); err != nil {
		t.Fatal("original partial owner did not close", err)
	}
	connection, err := net.DialTimeout("tcp", occupied.Addr().String(), time.Second)
	if err != nil {
		t.Fatal("unowned existing listener was closed", err)
	}
	_ = connection.Close()
}
