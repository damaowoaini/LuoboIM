package namespaceimport

import "math"

// One proven interval, never a union or an owner capacity limit. A new import
// must revalidate the original managed row and its complete receipt/admission
// chain from sealed source references before minting private VerifiedPage.
// SourceEpoch is the old epoch; this is not a new runtime admission capability.
const OriginalGroupSequenceScopeV1 = "original-group-sequence-scope-v1"

type OwnerHistorySegment struct {
	ScopeContract string      `json:"scope_contract,omitempty"`
	Kind          string      `json:"kind"`
	CID           string      `json:"cid"`
	OwnerID       string      `json:"owner_id"`
	OriginSHA256  string      `json:"origin_sha256"`
	Lower         int64       `json:"lower"`
	Upper         int64       `json:"upper"`
	SourceEpoch   string      `json:"source_epoch"`
	SourceKind    string      `json:"source_kind"`
	SourceID      string      `json:"source_id"`
	Evidence      []SourceRef `json:"evidence"`
}

func (a OwnerHistorySegment) ValidateShape() error {
	if a.ScopeContract != "" && a.ScopeContract != OriginalGroupSequenceScopeV1 || a.Kind != "preserved-owner-history-v1" || !identity(a.CID) || !identity(a.OwnerID) || !validSHA(a.OriginSHA256) || a.Lower < 1 || a.Upper < a.Lower || a.Upper >= math.MaxInt64 || !uuidPattern.MatchString(a.SourceEpoch) || a.SourceEpoch == "00000000-0000-0000-0000-000000000000" || !identity(a.SourceID) || !refsValid(a.Evidence) {
		return ErrInvalid
	}
	switch a.SourceKind {
	case "namespace-owner-prospective-v1", "namespace-owner-live-history-v1", "namespace-owner-history-admission-v1", "namespace-owner-imported-history-v1", "member-notice-entitlement-v1", "namespace-import-boundary-v1":
		return nil
	default:
		return ErrInvalid
	}
}
func OwnerHistorySegmentSHA256(a OwnerHistorySegment) (string, error) {
	if e := a.ValidateShape(); e != nil {
		return "", e
	}
	return digest(a), nil
}
