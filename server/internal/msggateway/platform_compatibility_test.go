package msggateway

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	pg "github.com/OpenIMSDK/protocol/msggateway"
	"github.com/openimsdk/open-im-server/v3/pkg/common/rpcmiddleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestEveryRegisteredPlatformIncludingAdminCanBeRevokedThroughRPCValidation(t *testing.T) {
	for platform := range constant.PlatformID2Name {
		request := &pg.KickUserOfflineReq{PlatformID: int32(platform), KickUserIDList: []string{"synthetic-user"}}
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(constant.OperationID, "synthetic-credential-revoke", constant.OpUserID, "synthetic-service"))
		called := false
		_, err := rpcmiddleware.Server(ctx, request, &grpc.UnaryServerInfo{FullMethod: "/OpenIMServer.msggateway.MsgGateway/KickUserOffline"}, func(context.Context, interface{}) (interface{}, error) {
			called = true
			return &pg.KickUserOfflineResp{}, nil
		})
		if err != nil || !called {
			t.Fatal("registered platform revocation was rejected before gateway handler", platform, err)
		}
		if err := (&pg.MultiTerminalLoginCheckReq{PlatformID: int32(platform), UserID: "synthetic-user", Token: "synthetic-fixture-token"}).Check(); err != nil {
			t.Fatal("protocol platform rejected by multi-terminal validation", platform, err)
		}
	}
	for _, platform := range []int32{-1, 0, 11, 999} {
		if (&pg.KickUserOfflineReq{PlatformID: platform, KickUserIDList: []string{"synthetic-user"}}).Check() == nil || (&pg.MultiTerminalLoginCheckReq{PlatformID: platform, UserID: "synthetic-user", Token: "synthetic-fixture-token"}).Check() == nil {
			t.Fatal("unknown platform accepted", platform)
		}
	}
}
