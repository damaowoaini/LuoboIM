package msggateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/OpenIMSDK/protocol/constant"
	pg "github.com/OpenIMSDK/protocol/msggateway"
	pu "github.com/OpenIMSDK/protocol/user"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"google.golang.org/grpc"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type registrationCache struct {
	cache.MsgModel
	values  map[string]int
	entered chan struct{}
	release chan struct{}
}

func (c *registrationCache) GetTokensWithoutError(context.Context, string, int) (map[string]int, error) {
	snapshot := make(map[string]int, len(c.values))
	for k, v := range c.values {
		snapshot[k] = v
	}
	if c.entered != nil {
		close(c.entered)
		<-c.release
	}
	return snapshot, nil
}

type statusFixture struct{ pu.UserClient }

func (s *statusFixture) SetUserStatus(context.Context, *pu.SetUserStatusReq, ...grpc.CallOption) (*pu.SetUserStatusResp, error) {
	return &pu.SetUserStatusResp{}, nil
}

type sessionConn struct {
	LongConn
	closes atomic.Int32
}

func (c *sessionConn) Close() error                         { c.closes.Add(1); return nil }
func (c *sessionConn) SetWriteDeadline(time.Duration) error { return nil }
func (c *sessionConn) WriteMessage(int, []byte) error       { return nil }
func makeRegistrationClient(ws *WsServer, token string, platform int) *Client {
	query := url.Values{"token": {token}, "sendID": {"owner"}, "platformID": {"2"}}
	return &Client{UserID: "owner", PlatformID: platform, token: token, w: new(sync.Mutex), conn: &sessionConn{}, longConnServer: ws, ctx: &UserConnContext{Req: &http.Request{URL: &url.URL{RawQuery: query.Encode()}}, RemoteAddr: token}}
}
func TestQueuedRevokedHandshakeCannotReplaceLiveSession(t *testing.T) {
	ws, _ := NewWsServer()
	ws.cache = &registrationCache{values: map[string]int{}}
	live := makeRegistrationClient(ws, "newer", 2)
	ws.clients.Set("owner", live)
	old := makeRegistrationClient(ws, "revoked", 2)
	ws.registerClient(old)
	if old.conn.(*sessionConn).closes.Load() != 1 {
		t.Fatal("revoked queued handshake not closed")
	}
	clients, _, _ := ws.clients.Get("owner", 2)
	if len(clients) != 1 || clients[0] != live || live.closed.Load() {
		t.Fatal("revoked handshake replaced live session")
	}
	ws.unregisterClient(old)
	if ws.onlineUserConnNum.Load() != 0 {
		t.Fatal("unregistered handshake altered counters")
	}
	t.Log("LUOBO_QUEUED_REVOKED_HANDSHAKE_REJECTED")
}
func TestExactLogoutWaitsForInFlightValidatedRegistration(t *testing.T) {
	testLogoutRegistration(t, false)
}
func TestPlatformLogoutWaitsForInFlightValidatedRegistration(t *testing.T) {
	testLogoutRegistration(t, true)
}
func testLogoutRegistration(t *testing.T, all bool) {
	saved := config.Config
	config.Config.Envs.Discovery = "fixture"
	config.Config.MultiLoginPolicy = constant.DefalutNotKick
	config.Config.Callback.CallbackUserOnline.Enable = false
	config.Config.Callback.CallbackUserOffline.Enable = false
	defer func() { config.Config = saved }()
	ws, _ := NewWsServer()
	gate := &registrationCache{values: map[string]int{"target": constant.NormalToken}, entered: make(chan struct{}), release: make(chan struct{})}
	ws.cache = gate
	ws.userClient = &rpcclient.UserRpcClient{Client: &statusFixture{}}
	target := makeRegistrationClient(ws, "target", 2)
	peer := makeRegistrationClient(ws, "other-platform", 5)
	ws.clients.Set("owner", peer)
	registered := make(chan struct{})
	go func() { ws.registerClient(target); close(registered) }()
	<-gate.entered
	// A logout has removed Redis status after handshake validation; gateway RPC
	// must wait until this in-flight registration is visible to its snapshot.
	hash := sha256.Sum256([]byte(target.token))
	done := make(chan error, 1)
	go func() {
		server := &Server{LongConnServer: ws}
		var err error
		if all {
			_, err = server.KickUserOffline(context.WithValue(context.Background(), constant.OpUserID, "owner"), &pg.KickUserOfflineReq{KickUserIDList: []string{"owner"}, PlatformID: 2})
		} else {
			_, err = server.KickTokenOffline(context.WithValue(context.Background(), constant.OpUserID, "owner"), &pg.KickTokenOfflineReq{UserID: "owner", PlatformID: 2, TokenHash: hex.EncodeToString(hash[:])})
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatal("kick passed registration critical section", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(gate.release)
	select {
	case <-registered:
	case <-time.After(time.Second):
		t.Fatal("registration did not finish")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("kick did not finish")
	}
	if target.conn.(*sessionConn).closes.Load() != 1 || !target.closed.Load() {
		t.Fatal("in-flight registration survived completed logout")
	}
	clients, ok := ws.clients.GetAll("owner")
	if !ok || len(clients) != 1 || clients[0] != peer || peer.closed.Load() {
		t.Fatal("other platform affected")
	}
	t.Log("LUOBO_LOGOUT_REGISTRATION_SERIALIZATION_PASS")
}
