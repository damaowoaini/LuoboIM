package push

import (
	"context"
	"fmt"
	pbpush "github.com/OpenIMSDK/protocol/push"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/getui"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	http2 "github.com/openimsdk/open-im-server/v3/pkg/common/http"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type ownedGetuiCacheFixture struct{ cache.MsgModel }

func (ownedGetuiCacheFixture) GetGetuiToken(context.Context) (string, error) { return "short", nil }
func TestOwnedActualGetuiHTTPAndMalformedACKStayWithOriginalOwnerWithoutReplay(t *testing.T) {
	for _, kind := range []string{"ack", "bad-json", "server-error"} {
		t.Run(kind, func(t *testing.T) {
			prior := config.Config
			defer func() { config.Config = prior }()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if kind == "server-error" {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
				if kind == "bad-json" {
					fmt.Fprint(w, "not-json")
				} else {
					fmt.Fprint(w, `{"code":0}`)
				}
			}))
			defer server.Close()
			config.Config.Push.GeTui.PushUrl = server.URL
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			original, later := &ownedsource.Observer{}, &ownedsource.Observer{}
			ctx, err := ownedsource.Bind(parent, original)
			if err != nil {
				t.Fatal(err)
			}
			transport, err := http2.NewOwnedPushHTTP(ctx, cancel)
			if err != nil {
				t.Fatal(err)
			}
			defer transport.Close()
			foreign, stop := context.WithCancel(context.Background())
			defer stop()
			foreign, err = ownedsource.Bind(foreign, later)
			if err != nil {
				t.Fatal(err)
			}
			provider := newOwnedProvider(getui.NewClient(ownedGetuiCacheFixture{}), original, cancel)
			err = provider.Push(transport.Bind(foreign), []string{"actual-recipient"}, "title", "content", &offlinepush.Opts{Signal: &offlinepush.Signal{ClientMsgID: "original-intent"}})
			if requests.Load() != 1 || (err == nil) != (kind == "ack") || original.Snapshot().Unconfirmed() != (kind != "ack") || later.Snapshot().PushDelivery.Started != 0 {
				t.Fatal("actual provider/wire outcome differs or changed owner")
			}
			if kind != "ack" {
				_ = provider.Push(transport.Bind(foreign), []string{"actual-recipient"}, "title", "content", &offlinepush.Opts{Signal: &offlinepush.Signal{ClientMsgID: "original-intent"}})
				if requests.Load() != 1 {
					t.Fatal("unknown original request physically replayed")
				}
			}
			if err := provider.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := provider.Push(transport.Bind(foreign), []string{"actual-recipient"}, "title", "content", &offlinepush.Opts{Signal: &offlinepush.Signal{ClientMsgID: "original-intent"}}); err == nil || requests.Load() != 1 {
				t.Fatal("closed actual provider accepted a late call")
			}

		})
	}
}

type ownedTokenDatabaseFixture struct {
	controller.PushDatabase
	calls int
	owner *ownedsource.Observer
}

func (d *ownedTokenDatabaseFixture) DelFcmToken(ctx context.Context, user string, platform int) error {
	if user != "original-user" || platform != 2 || ownedsource.FromContext(ctx) != d.owner {
		return errOwnedPush
	}
	d.calls++
	return nil
}
func TestOwnedRequiredPushRetainsExactTokenRPCWhileRawDeliveryAndLateOwnerAreRefused(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := &ownedsource.Observer{}
	ctx, err := ownedsource.Bind(parent, owner)
	if err != nil {
		t.Fatal(err)
	}
	database := &ownedTokenDatabaseFixture{owner: owner}
	service := &ownedRequiredPushServer{original: &pushServer{pusher: &Pusher{database: database}}, binding: &ownedPushBinding{ctx: ctx, fail: cancel}}
	if response, err := service.DelUserPushToken(context.Background(), &pbpush.DelUserPushTokenReq{UserID: "original-user", PlatformID: 2}); err != nil || response == nil || database.calls != 1 {
		t.Fatal("required mode lost the original token RPC")
	}
	if _, err := service.PushMsg(context.Background(), &pbpush.PushMsgReq{}); err == nil || database.calls != 1 || owner.Snapshot().PushDelivery.Started != 0 {
		t.Fatal("required mode dispatched raw push")
	}
	cancel()
	if _, err := service.DelUserPushToken(context.Background(), &pbpush.DelUserPushTokenReq{UserID: "original-user", PlatformID: 2}); err == nil || database.calls != 1 {
		t.Fatal("retired owner dispatched a late token mutation")
	}
}
