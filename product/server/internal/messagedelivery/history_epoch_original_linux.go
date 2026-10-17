//go:build linux

package messagedelivery

import (
	"context"
	"errors"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
)

func readOriginalEpochCutoverCustody(ctx context.Context, previous namespaceimport.Header, reader namespaceimport.OriginalHistoryRecordReader) (OriginalEpochCutoverCustody, error) {
	var out OriginalEpochCutoverCustody
	if ctx == nil || previous.Validate() != nil || reader == nil {
		return out, ErrInvalid
	}
	// Explicit previous Header identity selects its one permanent archive. An
	// absent archive falls back ONLY to the original fixed row for that exact
	// old epoch. Bad archive/shape never falls back to today's row or namespace.
	archive, ref, e := reader.ReadOriginalHistoryRecord(ctx, Receipts, historyEpochTransitionID(previous))
	if errors.Is(e, namespaceimport.ErrOriginalHistoryRecordAbsent) {
		raw, source, e := reader.ReadOriginalHistoryRecord(ctx, Sequences, NamespaceCutoverID)
		if e != nil {
			return out, e
		}
		if !originalEpochRefValid(source) {
			return out, ErrConflict
		}
		if _, e = decodeHistoryEpochCutover(previous, bson.Raw(raw)); e != nil {
			return out, e
		}
		out.CutoverRaw = append([]byte(nil), raw...)
		out.SourceRef = source
		return out, nil
	}
	if e != nil {
		return out, e
	}
	if !originalEpochRefValid(ref) {
		return out, ErrConflict
	}
	record, e := decodeHistoryEpochRecord(bson.Raw(archive), previous.SHA256())
	if e != nil {
		return out, e
	}
	original := record.OriginalDocuments[5]
	if _, e = decodeHistoryEpochCutover(previous, bson.Raw(original.Raw)); e != nil {
		return out, e
	}
	out = OriginalEpochCutoverCustody{CutoverRaw: append([]byte(nil), original.Raw...), SourceRef: ref, Archived: true, ArchiveRaw: append([]byte(nil), archive...), PreviousPreparationJSON: record.PreviousPreparationJSON, PreviousReadinessJSON: record.PreviousReadinessJSON, PreviousPreparationSHA256: record.PreviousPreparationSHA256, PreviousReadinessSHA256: record.PreviousReadinessSHA256}
	return out, nil
}
func originalEpochRefValid(ref namespaceimport.SourceRef) bool {
	return validSHA(ref.ObjectSHA256) && validSHA(ref.RecordSHA256) && identity(ref.Kind)
}
