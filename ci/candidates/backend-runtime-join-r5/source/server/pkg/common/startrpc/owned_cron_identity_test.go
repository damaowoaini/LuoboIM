package startrpc

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
)

func TestOwnedCronIdentityBindsActualPrivateBootstrapAndOnlyItsCompleteFixedSteps(t *testing.T) {
	input := ownedRPCInputFixture(t)
	input.boot.Role = "crontask"
	raw, err := json.Marshal(input.boot)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(input.sourcePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(OwnedRPCBootstrapSHAEnv, ownedSHA(raw))
	owner, err := LoadOwnedServerIdentity("crontask")
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.CheckDatabase(config.Config.Mongo.Database); err != nil {
		t.Fatal(err)
	}
	source := &ownedmongo.Observer{}
	if err := owner.BindSourceObserver(source); err != nil {
		t.Fatal(err)
	}
	for _, wrong := range [][]string{apiOwnedStopSteps(false), {"cron-jobs", "kafka-producers", "redis", "mongo", "discovery"}, {"cron-admission", "cron-jobs", "redis", "kafka-producers", "mongo", "discovery"}} {
		if err := owner.Announce(wrong); err == nil {
			t.Fatal("incomplete/foreign role steps authorized cron ready")
		}
	}
	if _, err := os.Lstat(input.path); !os.IsNotExist(err) {
		t.Fatal("rejected steps wrote ready")
	}
	steps := []string{"cron-admission", "cron-jobs", "kafka-producers", "redis", "mongo", "discovery"}
	if err := owner.Announce(steps); err != nil {
		t.Fatal(err)
	}
	if err := owner.BindSourceObserver(&ownedmongo.Observer{}); err == nil {
		t.Fatal("ready owner replaced original source observer")
	}
	if err := owner.Drain(steps[:len(steps)-1]); err == nil {
		t.Fatal("incomplete jobs/resources authorized cron drain")
	}
	if err := owner.Drain(steps); err != nil {
		t.Fatal(err)
	}
	ready, _, err := ownedcontrol.ReadServerRuntimeAnnouncement(input.path, input.nonce)
	if err != nil {
		t.Fatal(err)
	}
	drain, _, err := ownedcontrol.ReadServerRuntimeDrain(input.boot.DrainPath, input.nonce)
	if err != nil || ready.Role != "crontask" || drain.Role != "crontask" || ready.BootstrapSHA256 != owner.input.rawSHA || !reflect.DeepEqual(drain.JoinedAndClosed, steps) || drain.SourceOutcomeUnconfirmed {
		t.Fatal("cron identity did not bind exact original private owner/steps", err)
	}
}
