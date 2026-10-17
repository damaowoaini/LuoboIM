package namespaceimport

import "context"

// These are sealed RAW locators, not read grants. Implementations must bind
// their complete index to the same immutable raw snapshot and reject duplicate
// CID/seq or doc_id entries. A missing file/index is never sealed absence.
type OriginalHistorySequenceReader interface {
	ReadOriginalHistorySourceAtSequence(context.Context, string, int64) ([]byte, SourceRef, error)
	ReadOriginalHistoryMessageDocument(context.Context, string) ([]byte, SourceRef, error)
}

// A range can contain independently restricted notices and unresolved slots.
// Pagination is an I/O budget, not a history/owner/segment capacity limit.
// Each returned segment has its own original owner/sequence/source evidence;
// a later Inventory signature cannot turn an unresolved slot into permission.
type OriginalHistorySequencePending struct {
	Sequence int64
	Reason   string
	Evidence []SourceRef
}
type OriginalHistorySegmentPage struct {
	Segments []OwnerHistorySegment
	Pending  []OriginalHistorySequencePending
	Next     string
	Complete bool
}
type OriginalHistorySegmentPager interface {
	DeriveOriginalHistorySegmentPage(context.Context, Header, Inventory, string, string, string, OriginalHistoryRecordReader, string) (OriginalHistorySegmentPage, error)
}
