package namespaceimport

import "math"

// This is a source proof, not a read grant. Held admissions enter only through
// the signed inventory + semantic source validator + private VerifiedPage.
// Actual-source admissions are minted only by the private same-TX receiver.
// Neither Lower nor a current roster supplies any imported historical range.
type ProspectiveOwnerAdmission struct {
	Kind                   string      `json:"kind"`
	CID                    string      `json:"cid"`
	OwnerID                string      `json:"owner_id"`
	OriginSHA256           string      `json:"origin_sha256"`
	SeedMaximum            int64       `json:"seed_maximum"`
	Lower                  int64       `json:"lower"`
	HeaderSHA256           string      `json:"header_sha256,omitempty"`
	InventoryBindingSHA256 string      `json:"inventory_binding_sha256,omitempty"`
	CutSHA256              string      `json:"cut_sha256,omitempty"`
	CutEvidence            []SourceRef `json:"cut_evidence,omitempty"`
	HeadEvidence           []SourceRef `json:"head_evidence,omitempty"`
	MemberEvidence         []SourceRef `json:"member_evidence,omitempty"`
	RosterSHA256           string      `json:"roster_sha256,omitempty"`
	RosterMembers          uint64      `json:"roster_members,omitempty"`
	SourceID               string      `json:"source_id,omitempty"`
	BranchID               string      `json:"branch_id,omitempty"`
	InputSHA256            string      `json:"input_sha256,omitempty"`
	ReceiptSHA256          string      `json:"receipt_sha256,omitempty"`
}

func (a ProspectiveOwnerAdmission) ValidateShape() error {
	if !identity(a.CID) || !identity(a.OwnerID) || !validSHA(a.OriginSHA256) || a.SeedMaximum < 0 || a.SeedMaximum >= math.MaxInt64-1 || a.Lower != a.SeedMaximum+1 {
		return ErrInvalid
	}
	switch a.Kind {
	case "held-cut-roster-v1":
		if !validSHA(a.HeaderSHA256) || !validSHA(a.InventoryBindingSHA256) || !validSHA(a.CutSHA256) || !refsValid(a.CutEvidence) || !refsValid(a.HeadEvidence) || !refsValid(a.MemberEvidence) || !validSHA(a.RosterSHA256) || a.RosterMembers == 0 || a.SourceID != "" || a.BranchID != "" || a.InputSHA256 != "" || a.ReceiptSHA256 != "" {
			return ErrInvalid
		}
	case "actual-positive-source-v1":
		if !uuidPattern.MatchString(a.SourceID) || !uuidPattern.MatchString(a.BranchID) || !validSHA(a.InputSHA256) || !validSHA(a.ReceiptSHA256) || a.HeaderSHA256 != "" || a.InventoryBindingSHA256 != "" || a.CutSHA256 != "" || len(a.CutEvidence) != 0 || len(a.HeadEvidence) != 0 || len(a.MemberEvidence) != 0 || a.RosterSHA256 != "" || a.RosterMembers != 0 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func ProspectiveAdmissionSHA256(a ProspectiveOwnerAdmission) (string, error) {
	if err := a.ValidateShape(); err != nil {
		return "", err
	}
	return digest(a), nil
}
