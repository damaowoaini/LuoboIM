package auth

import (
	"context"
	"errors"
	"testing"

	pbauth "github.com/OpenIMSDK/protocol/auth"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"google.golang.org/grpc"
)

type logoutDBStub struct {
	controller.AuthDatabase
	revoked bool
	err     error
}

func (d *logoutDBStub) RevokeTokens(context.Context, string, int) error {
	d.revoked = true
	return d.err
}

type gatewayDiscoveryStub struct {
	discoveryregistry.SvcDiscoveryRegistry
	db *logoutDBStub
}

func (d gatewayDiscoveryStub) GetConns(context.Context, string, ...grpc.DialOption) ([]*grpc.ClientConn, error) {
	if !d.db.revoked {
		panic("gateway kick before token revocation")
	}
	return nil, nil
}

func TestLogoutSecurityRevokesOfflineTokens(t *testing.T) {
	oldAdmins := config.Config.IMAdmin.UserID
	defer func() { config.Config.IMAdmin.UserID = oldAdmins }()
	config.Config.IMAdmin.UserID = []string{"admin"}
	ctx := context.WithValue(context.Background(), constant.OpUserID, "admin")
	db := &logoutDBStub{}
	s := &authServer{authDatabase: db, RegisterCenter: gatewayDiscoveryStub{db: db}}
	if _, err := s.ForceLogout(ctx, &pbauth.ForceLogoutReq{UserID: "u1", PlatformID: 2}); err != nil || !db.revoked {
		t.Fatalf("offline token not revoked: %v", err)
	}
	db.revoked = false
	if _, err := s.ForceLogout(context.WithValue(ctx, constant.OpUserID, "attacker"), &pbauth.ForceLogoutReq{UserID: "u1", PlatformID: 2}); err == nil || db.revoked {
		t.Fatal("ordinary user forced another account offline")
	}
	db.err = errors.New("Redis unavailable")
	if _, err := s.ForceLogout(ctx, &pbauth.ForceLogoutReq{UserID: "u1", PlatformID: 2}); err == nil {
		t.Fatal("failed revocation reported success")
	}
}
