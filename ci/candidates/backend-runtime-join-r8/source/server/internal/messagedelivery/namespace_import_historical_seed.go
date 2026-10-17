package messagedelivery

import (
	"context"
	"errors"
	"math"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Private migration write ONLY. A real missing old Mongo counter remains an
// absence in the original record/receipt; creating destination H is the import
// result, not a claim that old Mongo observed H or 0. Global Seed and the public
// InitializeSequence known-both-plane contract remain byte-for-byte unchanged.
func (w *NamespaceImportWriter) initializeHistoricalCounter(ctx context.Context, permit *namespaceimport.Permit, page namespaceimport.VerifiedPage, record namespaceimport.OriginRecord, maximum int64) error {
	if e := w.requireImportPage(ctx, permit, page); e != nil {
		return e
	}
	expected, e := record.EffectiveSeedMaximum()
	if e != nil || expected != maximum || maximum < 0 || maximum >= math.MaxInt64 || !validSHA(record.SeedEvidenceSHA256) {
		return ErrConflict
	}
	matches := 0
	for _, actual := range page.Data().Origins {
		if directHistoryDigest(actual) == directHistoryDigest(record) {
			matches++
		}
	}
	if matches != 1 {
		return ErrConflict
	}
	if record.MongoHead.State == namespaceimport.LegacyCounterNotAllocatedHere {
		sum, e := namespaceimport.SourceSeedEvidenceSHA256(w.header, page.Inventory(), record)
		if e != nil || sum != record.SeedEvidenceSHA256 || record.MongoHead.LegacyAbsence == nil || record.MongoHead.LegacyAbsence.ValidateBinding(w.header, page.Inventory(), record.Origin.CID) != nil {
			return ErrConflict
		}
	}
	// The real immutable namespace claim is already staged in this same page TX.
	d, _, e := w.store.namespaceOwner(ctx, record.Origin.CID)
	if e != nil {
		return e
	}
	if d.Kind != record.Origin.Kind || d.SHA256 != record.Origin.SHA256 || d.GroupID != record.Origin.GroupID {
		return ErrConflict
	}
	// The permanent identity may have been created in an older epoch. Its old
	// claim seed is immutable and is NOT this new epoch's verified import seed.
	// The private page + exact source record, not that old seed, authorize H.
	row := counter{ID: key(w.header.Epoch, "sequence", record.Origin.CID), Epoch: w.header.Epoch, CID: record.Origin.CID, Maximum: maximum, SeedMaximum: maximum, SeedEvidenceSHA: record.SeedEvidenceSHA256, Revision: 1}
	var raw bson.Raw
	e = w.store.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": row.ID}).Decode(&raw)
	if errors.Is(e, mongo.ErrNoDocuments) {
		_, e = w.store.db.Collection(Sequences).InsertOne(ctx, row)
		return e
	}
	if e != nil {
		return e
	}
	var old counter
	if !exactFields(raw, []string{"_id", "epoch", "conversation_id", "seed_evidence_sha256"}, []string{"maximum", "revision", "seed_maximum"}, nil, "") || bson.Unmarshal(raw, &old) != nil || old.ID != row.ID || old.Epoch != row.Epoch || old.CID != row.CID || old.Maximum != maximum || old.SeedMaximum != maximum || old.SeedEvidenceSHA != row.SeedEvidenceSHA || old.Revision < 1 || old.Revision >= math.MaxInt64 {
		return ErrConflict
	}
	return nil
}
