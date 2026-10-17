package namespaceimport

import "context"

// The actual source-side decoder enumerates original typed row locators only.
// This concrete producer rederives every still-authorized ORIGINAL interval
// and complete SourceRefs from that row's full old-epoch receipt/admission/
// publication chain plus true visibility/forbid evidence. It returns separate
// intervals, never a min/max union, a future grant, a fresh current cut, or an
// interval guessed from parameters. A closed-empty source returns no segment;
// missing proof is an error, not an empty successful result. The verifier must
// independently reproduce the exact interval and evidence before a private
// page can be minted. Source trust is explicitly reviewed, not loaded from DB.
type OriginalHistorySegmentProducer interface {
	DeriveOriginalHistorySegments(context.Context, Header, Inventory, string, string, string, OriginalHistoryRecordReader) ([]OwnerHistorySegment, error)
}
