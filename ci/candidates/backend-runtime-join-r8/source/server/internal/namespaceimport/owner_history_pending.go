package namespaceimport

// A source-side disposition, never a grant or a terminal empty history result.
// Only these finite missing-proof reasons may be reported without raw errors.
// Existing original refs remain available for an operator to supply the exact
// missing old trust/witness; unrelated known origins need not be quarantined.
type OriginalHistoryProofPending struct {
	Sequence int64
	Reason   string
	Evidence []SourceRef
}

func (e *OriginalHistoryProofPending) Error() string {
	if e != nil {
		switch e.Reason {
		case "source-trust-unavailable", "original-chain-unavailable", "boundary-witness-unavailable", "original-slot-scope-unavailable":
			return "original history proof pending: " + e.Reason
		}
	}
	return "original history proof pending"
}
func (e *OriginalHistoryProofPending) Unwrap() error { return ErrUntrusted }
