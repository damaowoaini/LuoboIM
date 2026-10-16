package admin

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/proto/admin"
	"github.com/OpenIMSDK/tools/errs"
	"testing"
)

func TestNormalTokenIssuerStopsBeforeSigningOrCachingUnownedAccount(t *testing.T) {
	calls := 0
	s := &adminServer{Ownership: func(_ context.Context, id string) error {
		calls++
		if id != "pending-owner" {
			t.Fatal("wrong ownership subject")
		}
		return errs.ErrNoPermission.Wrap("synthetic unconfirmed ownership")
	}}
	// Neither token policy nor cache is installed. An unconfirmed account must
	// stop before touching either dependency, even through the final private RPC.
	resp, e := s.CreateToken(context.Background(), &admin.CreateTokenReq{UserID: "pending-owner", UserType: constant.NormalUser})
	if resp != nil || !errs.ErrNoPermission.Is(e) || calls != 1 {
		t.Fatal("normal token minted without ownership")
	}
}
