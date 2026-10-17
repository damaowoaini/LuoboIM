//go:build linux

package messagedelivery

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
)

// Finite sealed-reader seam exercises the real custody getter/strict codec.
// These synthetic preimages do not constitute operator exclusion, publication,
// source trust, a process-retirement capability or a production reader grant.
type epochCustodyTestReader struct {
	rows   map[string][]byte
	calls  []string
	badRef bool
}

func (r *epochCustodyTestReader) ReadOriginalHistoryRecord(_ context.Context, collection, id string) ([]byte, namespaceimport.SourceRef, error) {
	key := collection + "/" + id
	r.calls = append(r.calls, key)
	raw, ok := r.rows[key]
	if !ok {
		return nil, namespaceimport.SourceRef{}, namespaceimport.ErrOriginalHistoryRecordAbsent
	}
	ref := namespaceimport.SourceRef{ObjectSHA256: historyReadinessDigest("sealed object"), RecordSHA256: hash(raw), Kind: "mongo", Position: 3}
	if r.badRef {
		ref.ObjectSHA256 = ""
	}
	return append([]byte(nil), raw...), ref, nil
}
func epochCustodyArchiveFixture(t *testing.T) (namespaceimport.Header, historyEpochTransitionRecord, []byte) {
	t.Helper()
	h, r := historyEpochTypedOriginalsFixture(t)
	next := h
	next.Epoch = "44444444-4444-4444-8444-444444444444"
	next.ActivationSHA256 = historyReadinessDigest("next activation")
	next.MigrationID = "55555555-5555-4555-8555-555555555555"
	oldJSON, _ := json.Marshal(h)
	newJSON, _ := json.Marshal(next)
	prep := historyReadinessPreparationRecord{ID: historyPreparationID(h), Kind: "history-readiness-preparation-v1", Epoch: h.Epoch, HeaderJSON: string(oldJSON), HeaderSHA256: h.SHA256()}
	prepJSON, _ := json.Marshal(prep)
	ready := historyReadinessPublication{ID: historyReadinessID(h), Kind: "history-readiness-publication-v1", Epoch: h.Epoch, HeaderSHA256: h.SHA256(), PreparationSHA256: historyReadinessDigest(prep)}
	readyJSON, _ := json.Marshal(ready)
	r.ID = historyEpochTransitionID(h)
	r.Kind = "namespace-epoch-transition-v1"
	r.PreviousHeaderJSON = string(oldJSON)
	r.NextHeaderJSON = string(newJSON)
	r.NextHeaderSHA256 = next.SHA256()
	r.PreviousPreparationJSON = string(prepJSON)
	r.PreviousPreparationSHA256 = historyReadinessDigest(prep)
	r.PreviousReadinessJSON = string(readyJSON)
	r.PreviousReadinessSHA256 = historyReadinessDigest(ready)
	r.RetirementJSON = "[]"
	r.RetirementSHA256 = hash([]byte(r.RetirementJSON))
	r.SessionSHA256 = historyReadinessDigest("session")
	r.TargetLeaseID = historyReadinessDigest("lease")
	r.TargetAdmissionSHA256 = historyReadinessDigest("hold")
	raw, e := bson.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	return h, r, raw
}
func TestOriginalEpochCutoverCustodyNeverBorrowsCurrentEpochOrInventsTrust(t *testing.T) {
	t.Run("original fixed row before any transition", func(t *testing.T) {
		h, r, _ := epochCustodyArchiveFixture(t)
		raw := r.OriginalDocuments[5].Raw
		reader := &epochCustodyTestReader{rows: map[string][]byte{Sequences + "/" + NamespaceCutoverID: raw}}
		out, e := ReadOriginalEpochCutoverCustody(context.Background(), h, reader)
		if e != nil || out.Archived || hash(out.CutoverRaw) != hash(raw) || len(reader.calls) != 2 {
			t.Fatal("original fixed custody lost", e)
		}
		out.CutoverRaw[0] ^= 1
		if hash(reader.rows[Sequences+"/"+NamespaceCutoverID]) != hash(raw) {
			t.Fatal("returned bytes alias sealed raw")
		}
	})
	t.Run("archived preimage survives a different active epoch", func(t *testing.T) {
		h, r, archive := epochCustodyArchiveFixture(t)
		key := Receipts + "/" + historyEpochTransitionID(h)
		reader := &epochCustodyTestReader{rows: map[string][]byte{key: archive, Sequences + "/" + NamespaceCutoverID: []byte("foreign current row must not be read")}}
		out, e := ReadOriginalEpochCutoverCustody(context.Background(), h, reader)
		if e != nil || !out.Archived || len(reader.calls) != 1 || out.SourceRef.RecordSHA256 != hash(archive) || hash(out.CutoverRaw) != r.OriginalDocuments[5].SHA256 || hash(out.ArchiveRaw) != hash(archive) {
			t.Fatal("archive lost exact whole-record custody", e)
		}
		out.ArchiveRaw[0] ^= 1
		if hash(reader.rows[key]) != hash(archive) {
			t.Fatal("archive aliases source")
		}
	})
	t.Run("corrupt archive cannot fall back", func(t *testing.T) {
		h, r, archive := epochCustodyArchiveFixture(t)
		archive[0] ^= 1
		reader := &epochCustodyTestReader{rows: map[string][]byte{Receipts + "/" + historyEpochTransitionID(h): archive, Sequences + "/" + NamespaceCutoverID: r.OriginalDocuments[5].Raw}}
		if _, e := ReadOriginalEpochCutoverCustody(context.Background(), h, reader); e == nil || len(reader.calls) != 1 {
			t.Fatal("bad archive silently fell back")
		}
	})
	t.Run("foreign selected old header cannot use current cutover", func(t *testing.T) {
		h, r, _ := epochCustodyArchiveFixture(t)
		other := h
		other.Epoch = "66666666-6666-4666-8666-666666666666"
		reader := &epochCustodyTestReader{rows: map[string][]byte{Sequences + "/" + NamespaceCutoverID: r.OriginalDocuments[5].Raw}}
		if _, e := ReadOriginalEpochCutoverCustody(context.Background(), other, reader); e == nil {
			t.Fatal("another epoch's current cutover became historical proof")
		}
	})
	t.Run("missing raw source identity rejected", func(t *testing.T) {
		h, r, archive := epochCustodyArchiveFixture(t)
		for _, archived := range []bool{false, true} {
			rows := map[string][]byte{Sequences + "/" + NamespaceCutoverID: r.OriginalDocuments[5].Raw}
			if archived {
				rows[Receipts+"/"+historyEpochTransitionID(h)] = archive
			}
			reader := &epochCustodyTestReader{rows: rows, badRef: true}
			if _, e := ReadOriginalEpochCutoverCustody(context.Background(), h, reader); e == nil {
				t.Fatal("missing sealed source identity accepted")
			}
		}
	})
}
