//go:build darwin

package messagedelivery

import (
	"context"
	"errors"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
)

// Actual controlled epoch transition is a Linux-supervisor feature. Preserve
// unarchived exact-oldHeader custody here, but do not pretend Darwin owns or
// authenticates an archived Linux runtime graph. Common callers compile while
// an unsupported archive remains explicit; no new current-row fallback.
func readOriginalEpochCutoverCustody(ctx context.Context, previous namespaceimport.Header, reader namespaceimport.OriginalHistoryRecordReader) (OriginalEpochCutoverCustody, error) {
	var out OriginalEpochCutoverCustody
	if ctx == nil || previous.Validate() != nil || reader == nil {
		return out, ErrInvalid
	}
	_, _, e := reader.ReadOriginalHistoryRecord(ctx, Receipts, key("permanent", "namespace-epoch-transition", previous.SHA256()))
	if e == nil {
		return out, ErrUnprepared
	}
	if !errors.Is(e, namespaceimport.ErrOriginalHistoryRecordAbsent) {
		return out, e
	}
	raw, ref, e := reader.ReadOriginalHistoryRecord(ctx, Sequences, NamespaceCutoverID)
	if e != nil {
		return out, e
	}
	if !validSHA(ref.ObjectSHA256) || !validSHA(ref.RecordSHA256) || !identity(ref.Kind) {
		return out, ErrConflict
	}
	if _, e = decodeHistoryEpochCutover(previous, bson.Raw(raw)); e != nil {
		return out, e
	}
	out.CutoverRaw = append([]byte(nil), raw...)
	out.SourceRef = ref
	return out, nil
}
