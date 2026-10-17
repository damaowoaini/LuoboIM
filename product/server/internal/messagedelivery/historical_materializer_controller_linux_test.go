//go:build linux

package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

func TestReviewedOriginalHistoryEmptyTrustRetainsPendingWithoutInventedEpoch(t *testing.T) {
	ctx := context.Background()
	h := namespaceimport.Header{Tenant: "private-source-test", Database: "source_test", StorageInstance: "storage", ReplicaSet: "rs", MongoProcess: "mongo", MongoTopology: 1, RedisRunID: "redis", RedisDB: 15, KafkaClusterID: "kafka", Epoch: "11111111-1111-4111-8111-111111111111", MigrationID: "22222222-2222-4222-8222-222222222222", ActivationSHA256: hash([]byte("act")), DeploymentPolicySHA256: hash([]byte("policy"))}
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "selected.json")
	body, e := json.Marshal(originalHistoryTrustConfig{Kind: "reviewed-original-history-source-trust-v1", TargetHeaderSHA256: h.SHA256(), Epochs: []OriginalHistoryEpochTrust{}})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, body, 0600); e != nil {
		t.Fatal(e)
	}
	v, e := LoadOriginalHistoryVerifier(path, hash(body), h)
	if e != nil {
		t.Fatal(e)
	}
	i := namespaceimport.Inventory{HeaderSHA256: h.SHA256(), OriginalTrust: "UNTRUSTED", LeaseID: "private-lease", RawRootSHA256: hash([]byte("raw")), MongoRootSHA256: hash([]byte("mongo")), RedisRootSHA256: hash([]byte("redis"))}
	t.Run("explicit empty selection is exact config not a synthetic trusted epoch", func(t *testing.T) {
		if len(v.epochs) != 0 || v.checkConfig(h) != nil {
			t.Fatal("empty trust invented authority")
		}
		if _, e := LoadOriginalHistoryVerifier(path, hash([]byte("wrong")), h); e == nil {
			t.Fatal("wrong private source digest accepted")
		}
	})
	t.Run("unselected original locator returns explicit supplementation disposition", func(t *testing.T) {
		rows, e := v.DeriveOriginalHistorySegments(ctx, h, i, "33333333-3333-4333-8333-333333333333", "namespace-owner-live-history-v1", "actual-original-locator", originalOwnedSnapshot{})
		var pending *namespaceimport.OriginalHistoryProofPending
		if !errors.As(e, &pending) || pending.Reason != "source-trust-unavailable" || len(rows) != 0 {
			t.Fatal("missing trust became empty success or a grant", e)
		}
	})
	t.Run("private trust drift does not become a harmless missing epoch", func(t *testing.T) {
		if e = os.WriteFile(path, []byte("{}"), 0600); e != nil {
			t.Fatal(e)
		}
		rows, e := v.DeriveOriginalHistorySegments(ctx, h, i, "33333333-3333-4333-8333-333333333333", "namespace-owner-live-history-v1", "actual-original-locator", originalOwnedSnapshot{})
		var pending *namespaceimport.OriginalHistoryProofPending
		if e == nil || errors.As(e, &pending) || len(rows) != 0 {
			t.Fatal("source drift downgraded to missing proof", e)
		}
	})
}
