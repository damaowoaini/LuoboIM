package namespaceimport

import "context"

// A held admission must be part of the source-signed prospective root AND pass
// actual sealed cut/head/complete-roster semantics before VerifiedPage exists.
// This exported wire shape alone cannot construct VerifiedPage or a Permit.
type ProspectiveOwnerWitness struct {
	Admission ProspectiveOwnerAdmission `json:"admission"`
	Proof     []MerkleSibling           `json:"proof"`
}
type ProspectiveValidator interface {
	ValidateProspectiveAdmission(context.Context, Header, Inventory, ProspectiveOwnerAdmission) error
}

func (w ProspectiveOwnerWitness) verify(h Header, i Inventory) error {
	a := w.Admission
	if a.Kind != "held-cut-roster-v1" || a.ValidateShape() != nil || a.HeaderSHA256 != h.SHA256() || a.InventoryBindingSHA256 != i.BindingSHA256() || a.CutSHA256 != HeldProspectiveCutSHA256(h, i) || i.ProspectiveCount == 0 || !validSHA(i.ProspectiveSetRootSHA256) {
		return ErrUntrusted
	}
	sum, e := ProspectiveAdmissionSHA256(a)
	if e != nil {
		return e
	}
	return verifySetMembership(sum, w.Proof, i.ProspectiveSetRootSHA256)
}
func (v VerifiedPage) ProspectiveAdmissions() []ProspectiveOwnerWitness {
	return clone(v.data.ProspectiveAdmissions)
}

// ValidateBinding rechecks immutable recorded evidence; it is not a writer capability.
func (w ProspectiveOwnerWitness) ValidateBinding(h Header, i Inventory) error { return w.verify(h, i) }

// The immutable cut is actual raw custody + persistent launcher exclusion.
// Observe nonce/time may change; neither moves the head or renews old payload.
func HeldProspectiveCutSHA256(h Header, i Inventory) string {
	if i.HeaderSHA256 != h.SHA256() || i.LeaseID == "" || !validSHA(i.RawRootSHA256) || !validSHA(i.MongoRootSHA256) || !validSHA(i.RedisRootSHA256) || !validSHA(i.HeldAdmissionGuardSHA256) {
		return ""
	}
	return digest([]string{"namespace-held-prospective-cut-v1", h.SHA256(), i.LeaseID, i.RawRootSHA256, i.MongoRootSHA256, i.RedisRootSHA256, i.HeldAdmissionGuardSHA256})
}
