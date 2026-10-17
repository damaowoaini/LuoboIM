package namespaceimport

import (
	"context"
	"errors"
	"sort"
)

// OriginalWindows is the complete raw audit set, not an authorization union.
// EffectiveOwners is the count of UNIQUE owners for which the original history
// policy/event order/floors prove ONE final effective boundary. If the policy
// cannot collapse historical records without filling leave gaps, quarantine the
// CID; this protocol does not introduce multiple effective reader intervals.
type OriginCoverage struct {
	HeaderSHA256                   string      `json:"header_sha256"`
	InventoryBindingSHA256         string      `json:"inventory_binding_sha256"`
	CID                            string      `json:"cid"`
	OriginSHA256                   string      `json:"origin_sha256"`
	Domain                         string      `json:"domain"`
	OriginalWindows                uint64      `json:"original_windows"`
	EffectiveOwners                uint64      `json:"effective_owners"`
	OriginalWindowSetRootSHA256    string      `json:"original_window_set_root_sha256"`
	OwnerSetRootSHA256             string      `json:"owner_set_root_sha256"`
	EffectiveBoundarySetRootSHA256 string      `json:"effective_boundary_set_root_sha256"`
	SourceCoverageRootSHA256       string      `json:"source_coverage_root_sha256"`
	HistoryPolicySHA256            string      `json:"history_policy_sha256"`
	Evidence                       []SourceRef `json:"evidence"`
}
type EffectiveOwnerBoundary struct {
	Window                         OwnerWindow `json:"window"`
	HistoryPolicySHA256            string      `json:"history_policy_sha256"`
	OriginalOwnerWindowsRootSHA256 string      `json:"original_owner_windows_root_sha256"`
	FloorEvidence                  []SourceRef `json:"floor_evidence"`
}

// CID and owner are already exact original principals. No delimiter parsing or
// current membership is used by these canonical keys/hashes.
func EffectiveOwnerKey(cid, owner string) string {
	return digest([]string{"namespace-effective-owner-v1", cid, owner})
}
func EffectiveBoundarySHA256(boundary EffectiveOwnerBoundary) (string, error) {
	w := boundary.Window
	if !identity(w.CID) || !identity(w.OwnerID) || !validSHA(w.OriginSHA256) || w.Lower < 0 || w.Lower >= 9223372036854775807 || w.Upper < 0 || w.Upper >= 9223372036854775807 || !w.Closed && w.Upper != 0 || !refsValid(w.Evidence) || !refsValid(boundary.FloorEvidence) || !validSHA(boundary.HistoryPolicySHA256) || !validSHA(boundary.OriginalOwnerWindowsRootSHA256) {
		return "", ErrInvalid
	}
	return digest(boundary), nil
}

// SortedSetRoot is a deterministic audit helper for one bounded unit or test.
// The production importer builds the same sorted Merkle tree on private disk;
// it must never materialize a whole large group's owners in a single page.
// Duplicate values are rejected, not counted twice. Empty is explicit and can
// be accepted only when the source validator proves the empty original set.
func SortedSetRoot(values []string) (string, error) {
	values = append([]string(nil), values...)
	sort.Strings(values)
	for i, v := range values {
		if !validSHA(v) || i > 0 && values[i-1] == v {
			return "", ErrConflict
		}
	}
	if len(values) == 0 {
		return digest([]string{"namespace-import-empty-set-v1"}), nil
	}
	for i, v := range values {
		values[i] = digest([]string{"namespace-import-set-leaf-v1", v})
	}
	for len(values) > 1 {
		next := make([]string, 0, (len(values)+1)/2)
		for i := 0; i < len(values); i += 2 {
			if i+1 == len(values) {
				next = append(next, values[i])
			} else {
				next = append(next, digest([]string{"namespace-import-set-node-v1", values[i], values[i+1]}))
			}
		}
		values = next
	}
	return values[0], nil
}
func (c OriginCoverage) ValidateBinding(header Header, inventory Inventory, origin NamespaceOriginDescriptor) error {
	if c.HeaderSHA256 != header.SHA256() || c.InventoryBindingSHA256 != inventory.BindingSHA256() || c.CID != origin.CID || c.OriginSHA256 != origin.SHA256 || c.Domain != origin.Kind || !validSHA(c.OriginalWindowSetRootSHA256) || !validSHA(c.OwnerSetRootSHA256) || !validSHA(c.EffectiveBoundarySetRootSHA256) || !validSHA(c.SourceCoverageRootSHA256) || !validSHA(c.HistoryPolicySHA256) || !refsValid(c.Evidence) {
		return ErrConflict
	}
	return nil
}

var ErrCoverageIncomplete = errors.New("namespace import original owner coverage incomplete; quarantine required")

// VerifiedCoverage is minted only by a source implementation that rederives the
// entire set from held raw custody + original policy, never from Counts/SHA or
// an operator's assertion. It is not a runtime read grant or activation marker.
type VerifiedCoverage struct {
	value             OriginCoverage
	sourceSnapshotSHA string
}

func (c VerifiedCoverage) Value() OriginCoverage        { return clone(c.value) }
func (c VerifiedCoverage) SourceSnapshotSHA256() string { return c.sourceSnapshotSHA }

// Each coverage is individually proven against the source-signed inventory
// root. Repeating a witness across pages does not create another coverage row.
type OriginCoverageWitness struct {
	Coverage OriginCoverage  `json:"coverage"`
	Proof    []MerkleSibling `json:"proof"`
}
type EffectiveBoundaryWitness struct {
	Boundary      EffectiveOwnerBoundary `json:"boundary"`
	Coverage      OriginCoverageWitness  `json:"coverage"`
	OwnerProof    []MerkleSibling        `json:"owner_proof"`
	BoundaryProof []MerkleSibling        `json:"boundary_proof"`
}
type CoverageValidator interface {
	ValidateCoverage(context.Context, Header, Inventory, OriginRecord, OriginCoverage) error
	ValidateEffectiveBoundary(context.Context, Header, Inventory, EffectiveOwnerBoundary, OriginCoverage) error
}

func verifySetMembership(leaf string, proof []MerkleSibling, root string) error {
	if !validSHA(leaf) || !validSHA(root) || len(proof) > 64 {
		return ErrInvalid
	}
	value := digest([]string{"namespace-import-set-leaf-v1", leaf})
	for _, s := range proof {
		if !validSHA(s.SHA256) {
			return ErrInvalid
		}
		if s.Left {
			value = digest([]string{"namespace-import-set-node-v1", s.SHA256, value})
		} else {
			value = digest([]string{"namespace-import-set-node-v1", value, s.SHA256})
		}
	}
	if value != root {
		return ErrConflict
	}
	return nil
}
func (w OriginCoverageWitness) verify(h Header, i Inventory) error {
	c := w.Coverage
	if c.HeaderSHA256 != h.SHA256() || c.InventoryBindingSHA256 != i.BindingSHA256() || !identity(c.CID) || !validSHA(c.OriginSHA256) || c.Domain != "group" && c.Domain != "single" && c.Domain != "server-notification" || !validSHA(c.OriginalWindowSetRootSHA256) || !validSHA(c.OwnerSetRootSHA256) || !validSHA(c.EffectiveBoundarySetRootSHA256) || !validSHA(c.SourceCoverageRootSHA256) || !validSHA(c.HistoryPolicySHA256) || !refsValid(c.Evidence) {
		return ErrConflict
	}
	return verifySetMembership(digest(c), w.Proof, i.CoverageSetRootSHA256)
}
func (w EffectiveBoundaryWitness) verify(h Header, i Inventory) error {
	if e := w.Coverage.verify(h, i); e != nil {
		return e
	}
	c := w.Coverage.Coverage
	b := w.Boundary
	value, e := EffectiveBoundarySHA256(b)
	if e != nil {
		return e
	}
	if b.Window.CID != c.CID || b.Window.OriginSHA256 != c.OriginSHA256 || b.HistoryPolicySHA256 != c.HistoryPolicySHA256 || c.EffectiveOwners == 0 {
		return ErrConflict
	}
	if e = verifySetMembership(EffectiveOwnerKey(b.Window.CID, b.Window.OwnerID), w.OwnerProof, c.OwnerSetRootSHA256); e != nil {
		return e
	}
	return verifySetMembership(value, w.BoundaryProof, c.EffectiveBoundarySetRootSHA256)
}
func CoverageSHA256(c OriginCoverage) string { return digest(c) }
