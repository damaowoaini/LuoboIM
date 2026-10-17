//go:build darwin || linux

package startrpc

import (
	"context"
	"encoding/json"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestOwnedLegacyTransferIdentityBindsOriginalPrivateModeAndOnlyCompletedResources(t *testing.T) {
	for _, kind := range []string{"ack", "metrics", "unknown-kafka", "required-refused"} {
		t.Run(kind, func(t *testing.T) {
			input := ownedRPCInputFixture(t)
			input.boot.Role = "msgtransfer-legacy"
			if kind == "required-refused" {
				t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_V1", "required")
				t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_EPOCH", input.boot.InstanceID)
				t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_ACTIVATION_SHA256", strings.Repeat("b", 64))
				input.boot.Mode = "required"
				input.boot.Epoch = input.boot.InstanceID
				input.boot.ActivationSHA256 = strings.Repeat("b", 64)
				conf, env, err := OwnedRPCConfigurationHashes()
				if err != nil {
					t.Fatal(err)
				}
				input.boot.ConfigurationSHA256 = conf
				input.boot.EnvironmentSHA256 = env
			}
			raw, err := json.Marshal(input.boot)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, '\n')
			if err := os.WriteFile(input.sourcePath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(OwnedRPCBootstrapSHAEnv, ownedSHA(raw))
			owner, err := LoadOwnedServerIdentity("msgtransfer-legacy")
			if kind == "required-refused" {
				if err == nil || owner != nil {
					t.Fatal("legacy role took required-mode source ownership")
				}
				if _, err := os.Lstat(input.path); !os.IsNotExist(err) {
					t.Fatal("rejected required legacy role wrote ready")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := owner.CheckDatabase("changed-before-index"); err == nil {
				t.Fatal("legacy bootstrap original database check bypassed")
			}
			if err := owner.CheckDatabase(config.Config.Mongo.Database); err != nil {
				t.Fatal(err)
			}
			observer := &ownedmongo.Observer{}
			if err := owner.BindSourceObserver(observer); err != nil {
				t.Fatal(err)
			}
			steps := []string{"kafka-admission", "kafka-consumers", "kafka-producers"}
			if kind == "metrics" {
				steps = append(steps, "metrics")
			}
			steps = append(steps, "redis", "mongo", "discovery")
			for _, wrong := range [][]string{apiOwnedStopSteps(false), {"kafka-consumers", "kafka-admission", "kafka-producers", "redis", "mongo", "discovery"}, steps[:len(steps)-1]} {
				if err := owner.Announce(wrong); err == nil {
					t.Fatal("foreign/reordered/incomplete legacy steps granted ready")
				}
			}
			if err := owner.Drain(steps); err == nil {
				t.Fatal("legacy exclusion preceded real owner announcement")
			}
			if err := owner.Announce(steps); err != nil {
				t.Fatal(err)
			}
			if err := owner.Drain(steps[:len(steps)-1]); err == nil {
				t.Fatal("partial original resources became successful exclusion")
			}
			ready, _, err := ownedcontrol.ReadServerRuntimeAnnouncement(input.path, input.nonce)
			if err != nil || ready.Role != "msgtransfer-legacy" || ready.Mode != "legacy" || ready.Epoch != "" || ready.ActivationSHA256 != "" || ready.BootstrapSHA256 != ownedSHA(raw) || !reflect.DeepEqual(ready.OwnedStopStepNames, steps) {
				t.Fatal("legacy identity substituted new namespace or another owner", err)
			}
			if kind == "unknown-kafka" {
				parent, cancel := context.WithCancel(context.Background())
				defer cancel()
				ctx, err := ownedmongo.Bind(parent, observer)
				if err != nil {
					t.Fatal(err)
				}
				ownedsource.Attempt(ctx, ownedsource.KafkaProducer)(true)
				ownedsource.Attempt(ctx, ownedsource.KafkaProducer)(false)
				if err := owner.Drain(steps); err == nil {
					t.Fatal("later ACK cleared original unknown source")
				}
				if _, err := os.Lstat(input.boot.DrainPath); !os.IsNotExist(err) {
					t.Fatal("unknown original broker outcome got canonical drain")
				}
				body, err := os.ReadFile(input.boot.DrainPath + ".unconfirmed")
				if err != nil {
					t.Fatal(err)
				}
				var envelope struct {
					OwnerNonce  string                         `json:"ownerNonce"`
					Observation ownedcontrol.SourceObservation `json:"observation"`
				}
				if json.Unmarshal(body, &envelope) != nil || envelope.OwnerNonce != input.nonce || envelope.Observation.BootstrapSHA256 != ownedSHA(raw) || envelope.Observation.PolicySHA256 != ready.PolicySHA256 || !envelope.Observation.External.KafkaProducer.Unknown {
					t.Fatal("nonpermit custody lost original owner/source binding")
				}
				return
			}
			if err := owner.Drain(steps); err != nil {
				t.Fatal(err)
			}
			drain, _, err := ownedcontrol.ReadServerRuntimeDrain(input.boot.DrainPath, input.nonce)
			if err != nil || drain.Role != "msgtransfer-legacy" || drain.SourceOutcomeUnconfirmed || !reflect.DeepEqual(drain.JoinedAndClosed, steps) {
				t.Fatal("legacy actual complete steps lost", err)
			}
		})
	}
}
