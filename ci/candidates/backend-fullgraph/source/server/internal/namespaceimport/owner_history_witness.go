package namespaceimport

import "context"

// A whole original interval is independently source-proven and carried as ONE
// signed page row. It never broadens OwnerWindow or folds a leave gap into a
// union. JSON/shape/root integrity alone does not grant a Store write/read.
type OwnerHistorySegmentWitness struct {
	Segment                OwnerHistorySegment `json:"segment"`
	HeaderSHA256           string              `json:"header_sha256"`
	InventoryBindingSHA256 string              `json:"inventory_binding_sha256"`
	Proof                  []MerkleSibling     `json:"proof"`
}

func (w OwnerHistorySegmentWitness) ValidateBinding(h Header, i Inventory) error {
	if w.HeaderSHA256 != h.SHA256() || w.InventoryBindingSHA256 != i.BindingSHA256() || i.HeaderSHA256 != h.SHA256() || i.OwnerHistorySegmentCount == 0 || w.Segment.SourceEpoch == h.Epoch {
		return ErrConflict
	}
	value, e := OwnerHistorySegmentSHA256(w.Segment)
	if e != nil {
		return e
	}
	if w.Segment.SourceKind == "member-notice-entitlement-v1" && w.Segment.Lower != w.Segment.Upper {
		return ErrInvalid
	}
	return verifySetMembership(value, w.Proof, i.OwnerHistorySegmentSetRootSHA256)
}
func (v VerifiedPage) OwnerHistorySegments() []OwnerHistorySegmentWitness {
	return clone(v.data.OwnerHistorySegments)
}

type OwnerHistorySegmentValidator interface {
	ValidateOwnerHistorySegment(context.Context, Header, Inventory, OwnerHistorySegment) error
}
