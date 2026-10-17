//go:build linux

package messagedelivery

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"go.mongodb.org/mongo-driver/mongo"
)

// The real launcher capability is used. A bare hold has no actual Agent
// exclusion proof and must fail before any Mongo operation can happen.
func TestUnsupportedCustodyPreparationRejectsNilOrBareAdmissionHold(t *testing.T) {
	client, e := mongo.NewClient()
	if e != nil {
		t.Fatal(e)
	}
	p := custodyPolicyFixture("synthetic_owned_fixture", uuid.NewString(), hash([]byte("synthetic activation")), hash([]byte("synthetic cluster")), "owned-group")
	path, pin := writeCustodyPolicyFixture(t, p)
	c, e := LoadOwnedUnsupportedKafkaCustody(context.Background(), client.Database(p.Database), p.Epoch, p.ActivationSHA256, p.ClusterConfigurationSHA256, p.ConsumerGroup, path, pin)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"nil-hold", "real-launcher-without-exclusion"} {
		t.Run(name, func(t *testing.T) {
			var hold *ownedcontrol.AdmissionHold
			if name == "real-launcher-without-exclusion" {
				root := t.TempDir()
				if e := os.Chmod(root, 0700); e != nil {
					t.Fatal(e)
				}
				launcher, e := ownedcontrol.OpenOwnedLauncher(root, p.HeaderSHA256)
				if e != nil {
					t.Fatal(e)
				}
				defer launcher.Close()
				hold, e = launcher.Hold(context.Background())
				if e != nil {
					t.Fatal(e)
				}
			}
			if e := c.PrepareModifierCustody(context.Background(), hold, p); !errors.Is(e, ErrUnprepared) {
				t.Fatal("unexcluded metadata bootstrap touched a database or accepted authority", e)
			}
		})
	}
}
