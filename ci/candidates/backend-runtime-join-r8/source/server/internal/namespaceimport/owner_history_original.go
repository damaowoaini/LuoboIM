package namespaceimport

import (
	"context"
	"errors"
)

// Only a completed sealed source index may report this sentinel. A missing
// file, incomplete index, custody/identity drift or unknown collection is NOT
// original absence. Absence is factual inventory, not history authorization.
var ErrOriginalHistoryRecordAbsent = errors.New("original sealed history record absent")

// collection/id name the exact original typed _id. Implementations must read
// current private custody/RawRoot on every call, without live business queries,
// recapture, default rows or a process-global decoded cache. Returned BSON is
// caller-owned; SourceRef always names the actual original record.
type OriginalHistoryRecordReader interface {
	ReadOriginalHistoryRecord(context.Context, string, string) ([]byte, SourceRef, error)
}

// Concrete implementations interpret the ORIGINAL selected epoch's complete
// typed receipt/admission/page/publication chain and actual visibility/forbid
// state using reader. Keys come from explicit reviewed source trust, never DB
// self-assertion. This pure port does not write, activate or mint a Permit.
type OriginalHistorySegmentVerifier interface {
	VerifyOriginalHistorySegment(context.Context, Header, Inventory, OwnerHistorySegment, OriginalHistoryRecordReader) error
}
