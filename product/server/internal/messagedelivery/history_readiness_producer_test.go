//go:build linux

package messagedelivery

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
)

// Pure source/signature shape only. Even the positive shape below grants no
// preparation/Ready: actual publication, Source, Hold and real graph are absent.
func readinessSelectionFixture(t *testing.T) (namespaceimport.Header, HistoryReadinessSelection) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	h := namespaceimport.Header{Tenant: "synthetic-readiness", Database: "synthetic_readiness", StorageInstance: "11111111-1111-4111-8111-111111111111", ReplicaSet: "synthetic-rs", MongoProcess: strings.Repeat("b", 24), MongoTopology: 1, RedisRunID: strings.Repeat("c", 40), RedisDB: 15, KafkaClusterID: "synthetic-cluster", Epoch: "22222222-2222-4222-8222-222222222222", ActivationSHA256: historyReadinessDigest("synthetic-activation"), MigrationID: "33333333-3333-4333-8333-333333333333"}
	c := namespaceimport.Config{Header: h, CaptureAgent: "synthetic-capture", CaptureKeyID: "capture-key", CapturePublicKey: key.Public().(ed25519.PublicKey)}
	c.Header.DeploymentPolicySHA256 = namespaceimport.DeploymentPolicySHA256(c)
	h = c.Header
	i := namespaceimport.Inventory{HeaderSHA256: h.SHA256(), LeaseID: historyReadinessDigest("lease"), CaptureID: "synthetic-capture-id", CaptureAgent: c.CaptureAgent, CapturedAt: time.Now().UTC(), OriginalTrust: "UNTRUSTED", KeyID: c.CaptureKeyID}
	raw, _ := json.Marshal(i)
	i.Signature = ed25519.Sign(key, raw)
	reqs := []ownedcontrol.HistoryReaderRequirement{}
	for _, role := range []string{"rpc-conversation", "rpc-msg"} {
		sum := historyReadinessDigest(role)
		reqs = append(reqs, ownedcontrol.HistoryReaderRequirement{Role: role, BinarySHA256: sum, SourceSHA256: sum, BuildReceiptSHA256: sum, BootstrapSHA256: sum, ConfigurationSHA256: sum, EnvironmentSHA256: sum, Services: []string{"synthetic." + role}, ReadContractSHA256: HistoryReaderContractSHA256()})
	}
	return h, HistoryReadinessSelection{ImportConfig: c, Inventory: i, ManifestSHA256: historyReadinessDigest("manifest"), Readers: reqs, PrivateSessionPath: filepath.Join(t.TempDir(), "session.json")}
}
func TestHistoryReadinessSelectionBindsOriginalSignatureButCannotMintReady(t *testing.T) {
	h, s := readinessSelectionFixture(t)
	t.Run("valid shape is not a private preparation", func(t *testing.T) {
		if e := validateReadinessSelection(h, s); e != nil {
			t.Fatal(e)
		}
		var writer *NamespaceImportWriter
		if p, e := writer.PrepareHistoryReadiness(context.Background(), nil, nil, s); e == nil || p != nil {
			t.Fatal("signed shape minted authority without actual source/hold/publication")
		}
	})
	t.Run("changed signed inventory", func(t *testing.T) {
		bad := s
		bad.Inventory.PageCount++
		if validateReadinessSelection(h, bad) == nil {
			t.Fatal("changed inventory accepted")
		}
	})
	t.Run("foreign capture key", func(t *testing.T) {
		bad := s
		seed := make([]byte, ed25519.SeedSize)
		seed[0] = 9
		bad.ImportConfig.CapturePublicKey = ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
		if validateReadinessSelection(h, bad) == nil {
			t.Fatal("foreign signing identity accepted")
		}
	})
	t.Run("reader source contract and exact roles", func(t *testing.T) {
		bad := s
		bad.Readers = append([]ownedcontrol.HistoryReaderRequirement(nil), s.Readers...)
		bad.Readers[0].ReadContractSHA256 = historyReadinessDigest("range-only-without-slot-ACL")
		if validateReadinessSelection(h, bad) == nil {
			t.Fatal("incomplete reader contract accepted")
		}
		bad.Readers[0] = s.Readers[0]
		bad.Readers[0].Role = "rpc-group"
		if validateReadinessSelection(h, bad) == nil {
			t.Fatal("unknown reader role accepted")
		}
	})
}

func TestHistoryReadinessPrivateOutputsNeverEnterOriginalCustody(t *testing.T) {
	top := t.TempDir()
	raw, derived, sibling := filepath.Join(top, "raw"), filepath.Join(top, "derived"), filepath.Join(top, "operator")
	for _, p := range []string{raw, derived, sibling} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	alias := filepath.Join(top, "alias-to-raw")
	if e := os.Symlink(raw, alias); e != nil {
		t.Fatal(e)
	}
	dirs := []string{raw, derived}
	for _, path := range []string{raw, filepath.Join(raw, "session.json"), filepath.Join(derived, "child.log"), filepath.Join(alias, "session.json")} {
		if !forbiddenHistoryCustodyPath(path, dirs) {
			t.Fatal("private readiness output entered original custody")
		}
	}
	if forbiddenHistoryCustodyPath(filepath.Join(sibling, "session.json"), dirs) {
		t.Fatal("independent sibling private output rejected")
	}
	for _, p := range dirs {
		rows, e := os.ReadDir(p)
		if e != nil || len(rows) != 0 {
			t.Fatal("rejected custody paths changed original directory", e)
		}
	}
}
