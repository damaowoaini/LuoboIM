package startrpc

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
)

func ownedAPIIdentityFixture(t *testing.T) *OwnedServerIdentity {
	t.Helper()
	input := ownedRPCInputFixture(t) // the existing actual private-file bootstrap fixture, not Ready/seed
	input.boot.Role = "api"
	raw, err := json.Marshal(input.boot)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(input.sourcePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(OwnedRPCBootstrapSHAEnv, ownedSHA(raw))
	owner, err := LoadOwnedServerIdentity("api")
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.BindSourceObserver(&ownedmongo.Observer{}); err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestOwnedAPIServerIdentityBindsRealPrivateBootstrapAndKeepsUnconfirmedSourceSticky(t *testing.T) {
	owner := ownedAPIIdentityFixture(t)
	if err := owner.CheckDatabase("another-database"); err == nil {
		t.Fatal("environment-selected database replaced original bootstrap database")
	}
	if err := owner.CheckDatabase("owned-rpc-fixture"); err != nil {
		t.Fatal("exact original bootstrap database was refused", err)
	}
	steps := apiOwnedStopSteps(false)
	if err := owner.Drain(steps); err == nil {
		t.Fatal("drain before actual owner announcement accepted")
	}
	if _, err := LoadOwnedServerIdentity("msggateway"); err == nil {
		t.Fatal("unfinished role implicitly enabled")
	}
	actualDatabase := config.Config.Mongo.Database
	config.Config.Mongo.Database = "changed-after-construction"
	if err := owner.Announce(steps); err == nil {
		t.Fatal("ready accepted current config different from original bootstrap database")
	}
	config.Config.Mongo.Database = actualDatabase
	if err := owner.Announce(steps); err != nil {
		t.Fatal(err)
	}
	ready, _, err := ownedcontrol.ReadServerRuntimeAnnouncement(owner.input.path, owner.input.nonce)
	if err != nil || ready.Role != "api" || ready.Mode != "legacy" || ready.Epoch != "" || ready.ActivationSHA256 != "" || ready.BootstrapSHA256 != owner.input.rawSHA {
		t.Fatal("API identity relabeled legacy epoch/owner", err)
	}
	if err := owner.RecordUnconfirmed(ownedcontrol.SourceObservation{SourceOutcomeUnconfirmed: true, Started: 1, Failed: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(owner.input.boot.DrainPath + ".unconfirmed"); err != nil {
		t.Fatal("unknown source custody observation missing", err)
	}
	if err := owner.Drain(steps); err == nil {
		t.Fatal("later request erased unknown source and wrote exclusion drain")
	}
	if _, err := os.Lstat(owner.input.boot.DrainPath); !os.IsNotExist(err) {
		t.Fatal("canonical drain exists for unconfirmed source", err)
	}
}
