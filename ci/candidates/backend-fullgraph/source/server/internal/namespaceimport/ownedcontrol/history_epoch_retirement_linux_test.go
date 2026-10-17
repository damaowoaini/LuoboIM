//go:build linux

package ownedcontrol

import (
	"context"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"strings"
	"testing"
	"time"
)

// These are refusal/source-boundary controls, NOT a substitute for the actual
// canonical runtime-drain + whole-writer-fence controlled integration window.
func TestHistoryEpochRetirementCannotBeMintedFromDTOOrPreparedExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	t.Run("nil owner is never an actual process graph", func(t *testing.T) {
		var g *HistoryReaderLaunchGroup
		if cap, e := g.BeginEpochRetirement(ctx, namespaceImportHeaderForEpochTest(), sha([]byte("untrusted graph"))); e == nil || cap != nil {
			t.Fatal("DTO minted retirement")
		}
	})
	t.Run("empty retirement cannot authorize the target hold", func(t *testing.T) {
		h := namespaceImportHeaderForEpochTest()
		if (&VerifiedHistoryReaderRetirement{}).ValidateLocal(nil, h, h, sha([]byte("untrusted graph"))) == nil {
			t.Fatal("missing real hold/process/drain was accepted")
		}
	})
	t.Run("missing original cmd cannot advance a retirement", func(t *testing.T) {
		r := &VerifiedHistoryReaderRetirement{}
		if complete, e := r.Advance(ctx); e == nil || complete {
			t.Fatal("prepared or local exit became canonical runtime drain")
		}
		if len(r.Evidence()) != 0 {
			t.Fatal("failed retirement emitted evidence")
		}
	})
}

func namespaceImportHeaderForEpochTest() namespaceimport.Header {
	return namespaceimport.Header{Tenant: "epoch-test", Database: "epoch_test", StorageInstance: "11111111-1111-4111-8111-111111111111", ReplicaSet: "synthetic-rs", MongoProcess: strings.Repeat("a", 24), MongoTopology: 1, RedisRunID: strings.Repeat("b", 40), RedisDB: 15, KafkaClusterID: "synthetic-cluster", Epoch: "22222222-2222-4222-8222-222222222222", ActivationSHA256: strings.Repeat("c", 64), MigrationID: "33333333-3333-4333-8333-333333333333", DeploymentPolicySHA256: strings.Repeat("d", 64)}
}
