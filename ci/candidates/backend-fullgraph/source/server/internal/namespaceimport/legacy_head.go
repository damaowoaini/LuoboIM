package namespaceimport

import (
	"context"
	"math"
)

const LegacyCounterNotAllocatedHere = "legacy-counter-not-allocated-here"

// A complete sealed collection inventory may prove that a reviewed legacy
// Redis-only allocator NEVER allocated a Mongo counter here. This is not an
// empty namespace or an observed Mongo head=0. The private source validator
// must rederive the absence, exact reviewed allocator identity and held fence.
// No caller-selected digest, signed JSON alone or visible message max suffices.
type LegacyCounterAbsence struct {
	CID                   string `json:"cid"`
	Collection            string `json:"collection"`
	SnapshotSHA256        string `json:"snapshot_sha256"`
	RawRootSHA256         string `json:"raw_root_sha256"`
	RowsRootSHA256        string `json:"rows_root_sha256"`
	CollectionRootSHA256  string `json:"collection_root_sha256"`
	CollectionRows        uint64 `json:"collection_rows"`
	AllocatorSourceSHA256 string `json:"allocator_source_sha256"`
	AdmissionGuardSHA256  string `json:"admission_guard_sha256"`
}

// ValidateBinding is shape/integrity only. Controller requires a separate
// actual source validator and a private held Permit before any Store write.
func (a LegacyCounterAbsence) ValidateBinding(h Header, i Inventory, cid string) error {
	if h.Validate() != nil || !identity(cid) || a.CID != cid || a.Collection != "message_delivery_sequences_v1" || !validSHA(a.SnapshotSHA256) || a.RawRootSHA256 != i.RawRootSHA256 || !validSHA(a.RawRootSHA256) || !validSHA(a.RowsRootSHA256) || !validSHA(a.CollectionRootSHA256) || !validSHA(a.AllocatorSourceSHA256) || !validSHA(a.AdmissionGuardSHA256) || a.AdmissionGuardSHA256 != i.HeldAdmissionGuardSHA256 || i.HeaderSHA256 != h.SHA256() {
		return ErrConflict
	}
	return nil
}

type LegacyHeadValidator interface {
	ValidateLegacyHeadAbsence(context.Context, Header, Inventory, OriginRecord) error
}

func knownHeadShape(h Head) bool {
	return (h.State == "present" || h.State == "proven-empty") && h.LegacyAbsence == nil && h.Value >= 0 && h.Value < math.MaxInt64 && (h.State != "proven-empty" || h.Value == 0) && refsValid(h.Evidence)
}

// EffectiveSeedMaximum preserves both original plane states. A legacy absent
// Mongo plane contributes NO numeric value; at least the Redis plane must be
// actually known. This does not change global Seed.maximum/InitializeSequence.
func (r OriginRecord) EffectiveSeedMaximum() (int64, error) {
	if !knownHeadShape(r.RedisHead) {
		return 0, ErrInvalid
	}
	if r.MongoHead.State == LegacyCounterNotAllocatedHere {
		if r.MongoHead.Value != 0 || len(r.MongoHead.Evidence) != 0 || r.MongoHead.LegacyAbsence == nil {
			return 0, ErrInvalid
		}
		return r.RedisHead.Value, nil
	}
	if !knownHeadShape(r.MongoHead) {
		return 0, ErrInvalid
	}
	return max(r.MongoHead.Value, r.RedisHead.Value), nil
}
func originHeadsValid(h Header, i Inventory, r OriginRecord) bool {
	if _, e := r.EffectiveSeedMaximum(); e != nil {
		return false
	}
	if r.MongoHead.State == LegacyCounterNotAllocatedHere {
		return r.MongoHead.LegacyAbsence.ValidateBinding(h, i, r.Origin.CID) == nil
	}
	return true
}

// SourceSeedEvidenceSHA256 binds the original absence/heads and source refs,
// not a fabricated pair of known booleans. It deliberately omits the seed SHA
// field itself and the later page root, avoiding circular digests.
func SourceSeedEvidenceSHA256(h Header, i Inventory, r OriginRecord) (string, error) {
	if !originValid(r.Origin) || !originHeadsValid(h, i, r) || !refsValid(r.Evidence) {
		return "", ErrInvalid
	}
	return digest(struct {
		Domain                               string
		HeaderSHA256, InventoryBindingSHA256 string
		Origin                               NamespaceOriginDescriptor
		MongoHead, RedisHead                 Head
		Evidence                             []SourceRef
	}{"namespace-original-heads-v2", h.SHA256(), i.BindingSHA256(), r.Origin, r.MongoHead, r.RedisHead, r.Evidence}), nil
}
