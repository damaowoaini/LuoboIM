//go:build linux

package messagedelivery

import (
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
)

// Typed preimages only. These rows do NOT mint the real retirement/hold/source
// capabilities. The production constructor still refuses all such bare rows.
func historyEpochTypedOriginalsFixture(t *testing.T) (namespaceimport.Header, historyEpochTransitionRecord) {
	t.Helper()
	h, _ := readinessSelectionFixture(t)
	r := historyEpochTransitionRecord{PreviousHeaderSHA256: h.SHA256()}
	for at, k := range historyEpochOriginalKeys() {
		var value any
		switch {
		case at < 3:
			value = bson.D{{Key: "_id", Value: SchemaID}, {Key: "version", Value: int32(1)}, {Key: "epoch", Value: h.Epoch}, {Key: "activation_sha256", Value: h.ActivationSHA256}, {Key: "legacy_writers_retired", Value: true}}
		case at < 5:
			value = bson.D{{Key: "_id", Value: MembershipSchemaID}, {Key: "version", Value: int32(1)}, {Key: "epoch", Value: h.Epoch}, {Key: "activation_sha256", Value: h.ActivationSHA256}, {Key: "legacy_message_writers_retired", Value: true}, {Key: "legacy_group_writers_retired", Value: true}, {Key: "owner_bounds_seeded", Value: true}, {Key: "readers_bound", Value: true}}
		default:
			value = NamespaceCutover{ID: NamespaceCutoverID, Kind: "immutable-namespace-cutover-v1", Database: h.Database, Epoch: h.Epoch, ActivationSHA256: h.ActivationSHA256, MigrationID: h.MigrationID, State: "all-inventoried-namespaces-permanently-claimed", ManifestSHA256: historyReadinessDigest("manifest-shape-only"), MongoInventorySHA256: historyReadinessDigest("mongo-shape-only"), RedisInventorySHA256: historyReadinessDigest("redis-shape-only"), DrainSHA256: historyReadinessDigest("drain-shape-only"), OldBinaryFenceSHA256: historyReadinessDigest("fence-shape-only"), RegisteredAt: time.Now().UTC()}
		}
		raw, e := bson.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		r.OriginalDocuments = append(r.OriginalDocuments, historyEpochOriginalDocument{k[0], k[1], raw, hash(raw)})
	}
	return h, r
}
func TestHistoryEpochMetadataPreimagesDoNotResetOrMintAuthority(t *testing.T) {
	t.Run("complete typed preimages still lack real capabilities", func(t *testing.T) {
		h, r := historyEpochTypedOriginalsFixture(t)
		if e := validateHistoryEpochOriginals(h, r); e != nil {
			t.Fatal(e)
		}
		if c, e := NewHistoricalEpochMetadataBootstrap(nil, nil, nil, namespaceimport.Config{}, "/not-a-capability"); e == nil || c != nil {
			t.Fatal("bare rows became a controlled migration")
		}
		if run, e := NewOwnedHistoricalEpochImport(HistoricalEpochImportInputs{Config: namespaceimport.Config{Header: h}}); e == nil || run != nil {
			t.Fatal("operator caller minted handles from a header")
		}
	})
	t.Run("changed raw custody rejected", func(t *testing.T) {
		h, r := historyEpochTypedOriginalsFixture(t)
		r.OriginalDocuments[0].SHA256 = historyReadinessDigest("other raw")
		if validateHistoryEpochOriginals(h, r) == nil {
			t.Fatal("changed source metadata accepted")
		}
	})
	t.Run("partial old readiness cannot be used as predecessor", func(t *testing.T) {
		h, r := historyEpochTypedOriginalsFixture(t)
		r.OriginalDocuments = r.OriginalDocuments[:5]
		if validateHistoryEpochOriginals(h, r) == nil {
			t.Fatal("missing original marker/cutover accepted")
		}
	})
	t.Run("foreign epoch and wrong BSON version are safe refusals", func(t *testing.T) {
		h, r := historyEpochTypedOriginalsFixture(t)
		other := h
		other.Epoch = "44444444-4444-4444-8444-444444444444"
		if validateHistoryEpochOriginals(other, r) == nil {
			t.Fatal("foreign epoch accepted")
		}
		var doc bson.D
		if bson.Unmarshal(r.OriginalDocuments[0].Raw, &doc) != nil {
			t.Fatal("fixture decode")
		}
		for i := range doc {
			if doc[i].Key == "version" {
				doc[i].Value = int64(1)
			}
		}
		raw, e := bson.Marshal(doc)
		if e != nil {
			t.Fatal(e)
		}
		r.OriginalDocuments[0].Raw = raw
		r.OriginalDocuments[0].SHA256 = hash(raw)
		if validateHistoryEpochOriginals(h, r) == nil {
			t.Fatal("noncanonical version type accepted")
		}
	})
}
