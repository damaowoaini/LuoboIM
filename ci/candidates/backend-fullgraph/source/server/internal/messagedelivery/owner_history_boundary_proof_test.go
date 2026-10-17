package messagedelivery

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

// These check the ACTUAL signature/Merkle verifier, not a substitute writer or
// a declaration that synthetic data attests to historical ownership. Real
// ownership derivation remains the held source-policy responsibility.
func TestOriginalOwnerBoundaryRequiresOldSignatureAndExactWitness(t *testing.T) {
	for _, name := range []string{"exact-old-proof", "changed-old-range", "changed-owner", "changed-signed-root", "wrong-old-public-key"} {
		t.Run(name, func(t *testing.T) {
			public, private, e := ed25519.GenerateKey(rand.Reader)
			if e != nil {
				t.Fatal(e)
			}
			d, e := DescribeNamespace(namespaceMessage("owner", "peer", constant.SingleChatType, true, true))
			if e != nil {
				t.Fatal(e)
			}
			h := namespaceimport.Header{Tenant: "owned-proof", Database: "owned-history", StorageInstance: "owned-store", ReplicaSet: "owned-rs", MongoProcess: "owned-mongo", MongoTopology: 1, RedisRunID: "owned-redis", RedisDB: 1, KafkaClusterID: "owned-kafka", Epoch: "d1111111-1111-4111-8111-111111111111", ActivationSHA256: hash([]byte("old activation")), MigrationID: "e1111111-1111-4111-8111-111111111111", DeploymentPolicySHA256: hash([]byte("old policy"))}
			refs := []namespaceimport.SourceRef{{Kind: "mongo", ObjectSHA256: hash([]byte("old object")), RecordSHA256: hash([]byte("old record")), Position: 1}}
			boundary := namespaceimport.EffectiveOwnerBoundary{Window: namespaceimport.OwnerWindow{CID: d.CID, OwnerID: "owner", OriginSHA256: d.SHA256, Lower: 2, Upper: 5, Closed: true, Evidence: refs}, HistoryPolicySHA256: hash([]byte("reviewed old ownership policy")), OriginalOwnerWindowsRootSHA256: hash([]byte("old owner windows")), FloorEvidence: refs}
			boundarySHA, e := namespaceimport.EffectiveBoundarySHA256(boundary)
			if e != nil {
				t.Fatal(e)
			}
			ownerRoot, e := namespaceimport.SortedSetRoot([]string{namespaceimport.EffectiveOwnerKey(d.CID, "owner")})
			if e != nil {
				t.Fatal(e)
			}
			boundaryRoot, e := namespaceimport.SortedSetRoot([]string{boundarySHA})
			if e != nil {
				t.Fatal(e)
			}
			i := namespaceimport.Inventory{HeaderSHA256: h.SHA256(), LeaseID: "old-private-lease", CaptureID: "old-capture", CaptureAgent: "reviewed-old-agent", CapturedAt: time.Now().UTC().Add(-time.Hour), OriginalTrust: "UNTRUSTED", MongoRootSHA256: hash([]byte("old mongo")), RedisRootSHA256: hash([]byte("old redis")), RawRootSHA256: hash([]byte("old raw")), PagesRootSHA256: hash([]byte("old pages")), CoverageCount: 1, PageCount: 1, Counts: namespaceimport.Counts{Origins: 1, Windows: 1}, KeyID: "reviewed-old-key"}
			coverage := namespaceimport.OriginCoverage{CID: d.CID, OriginSHA256: d.SHA256, Domain: d.Kind, HeaderSHA256: h.SHA256(), InventoryBindingSHA256: i.BindingSHA256(), OriginalWindows: 1, EffectiveOwners: 1, OriginalWindowSetRootSHA256: hash([]byte("old original windows")), OwnerSetRootSHA256: ownerRoot, EffectiveBoundarySetRootSHA256: boundaryRoot, SourceCoverageRootSHA256: hash([]byte("old source coverage")), HistoryPolicySHA256: boundary.HistoryPolicySHA256, Evidence: refs}
			i.CoverageSetRootSHA256, e = namespaceimport.SortedSetRoot([]string{namespaceimport.CoverageSHA256(coverage)})
			if e != nil {
				t.Fatal(e)
			}
			unsigned, e := json.Marshal(i)
			if e != nil {
				t.Fatal(e)
			}
			i.Signature = ed25519.Sign(private, unsigned)
			trust := OriginalHistoryEpochTrust{Epoch: h.Epoch, ActivationSHA256: h.ActivationSHA256, Database: h.Database, ProducerSourceSHA256: hash([]byte("reviewed original source")), CutoverSHA256: hash([]byte("old known publication")), Import: &OriginalHistoryImportTrust{Header: h, Inventory: i, ManifestSHA256: hash([]byte("old manifest")), PublicKey: public}}
			witness := namespaceimport.EffectiveBoundaryWitness{Boundary: boundary, Coverage: namespaceimport.OriginCoverageWitness{Coverage: coverage}}
			switch name {
			case "changed-old-range":
				witness.Boundary.Window.Lower = 1
			case "changed-owner":
				witness.Boundary.Window.OwnerID = "foreign"
			case "changed-signed-root":
				trust.Import.Inventory.CoverageSetRootSHA256 = hash([]byte("changed"))
			case "wrong-old-public-key":
				other, _, e := ed25519.GenerateKey(rand.Reader)
				if e != nil {
					t.Fatal(e)
				}
				trust.Import.PublicKey = other
			}
			trustErr := validateOriginalImportTrust(trust)
			witnessErr := verifyOriginalBoundaryWitness(h, i, witness, d, "owner")
			if name == "exact-old-proof" {
				if trustErr != nil || witnessErr != nil {
					t.Fatal("exact signed original witness", trustErr, witnessErr)
				}
			} else if trustErr == nil && witnessErr == nil {
				t.Fatal("changed historical proof accepted", name)
			}
		})
	}
}
