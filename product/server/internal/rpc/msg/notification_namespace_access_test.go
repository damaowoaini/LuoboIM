package msg

import (
	"context"
	"errors"
	"testing"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
)

type managedNotificationAccessFixture struct {
	controller.CommonMsgDatabase
	allowed    bool
	err        error
	owner, cid string
}

func (f *managedNotificationAccessFixture) ManagedNamespaceHistoryAccess(_ context.Context, owner, cid string) (bool, bool, error) {
	f.owner, f.cid = owner, cid
	return true, f.allowed, f.err
}

func TestManagedNotificationRPCUsesTypedOriginBeforeLegacyNameRules(t *testing.T) {
	for _, name := range []string{"underscore-original-participant", "group-name-collision-nonowner", "metadata-unavailable"} {
		t.Run(name, func(t *testing.T) {
			f := &managedNotificationAccessFixture{allowed: name == "underscore-original-participant"}
			if name == "metadata-unavailable" {
				f.err = errors.New("synthetic permanent origin read unavailable")
			}
			m := &msgServer{MsgDatabase: f} // No legacy cache: any fallback is a test failure.
			var legacy map[string]struct{}
			allowed, err := m.notificationHistoryAllowed(context.Background(), "owner_with_underscore", "n_owner_with_underscore_peer", &legacy)
			if allowed != f.allowed || !errors.Is(err, f.err) || legacy != nil || f.owner != "owner_with_underscore" || f.cid != "n_owner_with_underscore_peer" {
				t.Fatal("managed authority fell back to a guessed name", err)
			}
		})
	}
}
