package msggateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	pb "github.com/OpenIMSDK/protocol/msggateway"
)

type sessionKickFixture struct {
	LongConnServer
	clients []*Client
	kicked  []*Client
}

type closeConnFixture struct {
	LongConn
	closes atomic.Int32
}

func (c *closeConnFixture) Close() error { c.closes.Add(1); return nil }

type closeServerFixture struct {
	LongConnServer
	unregisters atomic.Int32
}

func (s *closeServerFixture) UnRegister(*Client) { s.unregisters.Add(1) }
func TestConcurrentTokenKickAndSocketReadFailureUnregisterOnlyOnce(t *testing.T) {
	connection := &closeConnFixture{}
	server := &closeServerFixture{}
	client := &Client{w: new(sync.Mutex), conn: connection, longConnServer: server}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); client.close() }()
	}
	wg.Wait()
	if connection.closes.Load() != 1 || server.unregisters.Load() != 1 {
		t.Fatal("simultaneous logout/read failure closed or unregistered twice")
	}
	if err := client.writeBinaryMsg(Resp{}); err != ErrConnClosed {
		t.Fatal("closed connection falsely acknowledged push", err)
	}
}

func (f *sessionKickFixture) GetUserPlatformCons(string, int) ([]*Client, bool, bool) {
	return f.clients, true, true
}
func (f *sessionKickFixture) KickUserConn(c *Client) error {
	f.kicked = append(f.kicked, c)
	return nil
}
func TestExactTokenLogoutDoesNotKickLaterSamePlatformOrOtherSessions(t *testing.T) {
	fixture := &sessionKickFixture{}
	old := &Client{UserID: "owner", PlatformID: 2, token: "synthetic-old-token", longConnServer: fixture}
	newer := &Client{UserID: "owner", PlatformID: 2, token: "synthetic-new-token", longConnServer: fixture}
	otherPlatform := &Client{UserID: "owner", PlatformID: 5, token: "synthetic-old-token", longConnServer: fixture}
	foreign := &Client{UserID: "peer", PlatformID: 2, token: "synthetic-old-token", longConnServer: fixture}
	fixture.clients = []*Client{old, newer, otherPlatform, foreign, nil}
	hash := sha256.Sum256([]byte(old.token))
	request := &pb.KickTokenOfflineReq{UserID: "owner", PlatformID: 2, TokenHash: hex.EncodeToString(hash[:])}
	server := &Server{LongConnServer: fixture}
	if _, err := server.KickTokenOffline(context.WithValue(context.Background(), constant.OpUserID, "owner"), request); err != nil || len(fixture.kicked) != 1 || fixture.kicked[0] != old {
		t.Fatal("exact logout kicked another session", err)
	}
	fixture.kicked = nil
	if _, err := server.KickTokenOffline(context.WithValue(context.Background(), constant.OpUserID, "peer"), request); err == nil || len(fixture.kicked) != 0 {
		t.Fatal("foreign actor kicked session")
	}
	request.TokenHash = "invalid"
	if _, err := server.KickTokenOffline(context.WithValue(context.Background(), constant.OpUserID, "owner"), request); err == nil {
		t.Fatal("invalid digest accepted")
	}
}

func TestLegacyPlatformKickCannotActForForeignUser(t *testing.T) {
	fixture := &sessionKickFixture{}
	server := &Server{LongConnServer: fixture}
	request := &pb.KickUserOfflineReq{KickUserIDList: []string{"owner", "foreign"}, PlatformID: 2}
	if _, err := server.KickUserOffline(context.WithValue(context.Background(), constant.OpUserID, "owner"), request); err == nil {
		t.Fatal("foreign platform kick accepted")
	}
	if len(fixture.kicked) != 0 {
		t.Fatal("partially kicked before validating complete owner list")
	}
	if _, err := server.MultiTerminalLoginCheck(context.WithValue(context.Background(), constant.OpUserID, "foreign"), &pb.MultiTerminalLoginCheckReq{UserID: "owner", PlatformID: 2, Token: "synthetic"}); err == nil {
		t.Fatal("foreign multi-terminal check accepted")
	}
}
