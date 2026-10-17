//go:build linux || darwin

package messagedelivery

import (
	"context"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
)

// Custody only. This never authenticates an epoch/key, grants history or makes
// Ready. The original-history verifier MUST additionally bind its explicitly
// reviewed old Header/public key/signed Inventory/accepted Manifest and exact
// cutover SHA. The actual SourceRef points to the archive WHOLE RECORD when
// archived; it is never forged to pretend an embedded document was a raw row.
type OriginalEpochCutoverCustody struct {
	CutoverRaw                []byte
	SourceRef                 namespaceimport.SourceRef
	Archived                  bool
	ArchiveRaw                []byte
	PreviousPreparationJSON   string
	PreviousReadinessJSON     string
	PreviousPreparationSHA256 string
	PreviousReadinessSHA256   string
}

// The common verifier can call this without importing a Linux-only symbol.
// Linux's actual owned operator implements archived custody. Darwin preserves
// its original fixed-row contract and explicitly refuses unsupported archives;
// neither platform substitutes today's epoch for an explicitly selected one.
func ReadOriginalEpochCutoverCustody(ctx context.Context, previous namespaceimport.Header, reader namespaceimport.OriginalHistoryRecordReader) (OriginalEpochCutoverCustody, error) {
	return readOriginalEpochCutoverCustody(ctx, previous, reader)
}

func decodeHistoryEpochCutover(h namespaceimport.Header, raw bson.Raw) (NamespaceCutover, error) {
	var cut NamespaceCutover
	if !exactFields(raw, []string{"_id", "kind", "database", "epoch", "activation_sha256", "migration_id", "state", "manifest_sha256", "mongo_inventory_sha256", "redis_inventory_sha256", "drain_sha256", "old_binary_fence_sha256"}, []string{"namespace_count", "claimed_count"}, nil, "registered_at") || bson.Unmarshal(raw, &cut) != nil || cut.ID != NamespaceCutoverID || cut.Kind != "immutable-namespace-cutover-v1" || cut.Database != h.Database || cut.Epoch != h.Epoch || cut.ActivationSHA256 != h.ActivationSHA256 || cut.MigrationID != h.MigrationID || cut.State != "all-inventoried-namespaces-permanently-claimed" || cut.NamespaceCount < 0 || cut.NamespaceCount != cut.ClaimedCount || cut.RegisteredAt.IsZero() {
		return cut, ErrConflict
	}
	for _, value := range []string{cut.ManifestSHA256, cut.MongoInventorySHA256, cut.RedisInventorySHA256, cut.DrainSHA256, cut.OldBinaryFenceSHA256} {
		if !validSHA(value) {
			return cut, ErrConflict
		}
	}
	return cut, nil
}
