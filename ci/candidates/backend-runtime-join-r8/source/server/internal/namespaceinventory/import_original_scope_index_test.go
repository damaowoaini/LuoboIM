package namespaceinventory

import (
	"context"
	"path/filepath"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

// Real private capture, journal, raw-index seal and lookup code. Synthetic raw
// rows are locators only, never a message owner/source authorization fixture.
func originalScopeIndexFixture(t *testing.T, duplicate, finish bool) (*ImportWorkspace, *pagedFixture, *ImportSnapshot, error) {
	t.Helper()
	source := pageFixture(t)
	source.id.TenantAttested = true
	epoch := "41111111-1111-4111-8111-111111111111"
	branch := "51111111-1111-4111-8111-111111111111"
	effect := func(id string) MongoRow {
		return rawRow(t, "message_delivery_effects_v1", id, bson.M{"_id": jsonSHA([]string{epoch, "branch", id}), "epoch": epoch, "branch_id": id, "source_id": "61111111-1111-4111-8111-111111111111", "conversation_id": "sg_group", "seq": int64(2), "persist": true})
	}
	source.rows["msg"] = []MongoRow{rawRow(t, "msg", "original-doc", bson.M{"_id": "actual-physical-document", "doc_id": "sg_group:0", "msgs": bson.A{}})}
	source.rows["message_delivery_effects_v1"] = []MongoRow{effect(branch)}
	if duplicate {
		source.rows["message_delivery_effects_v1"] = append(source.rows["message_delivery_effects_v1"], effect("71111111-1111-4111-8111-111111111111"))
	}
	ctx := context.Background()
	rawdir := chunkDir(t)
	if _, e := BeginResumableCapture(ctx, source, source.id.Binding, rawdir, chunkFixtureLimits(), ProgressBudget{Steps: 1, SourcePages: 1}); e != nil {
		return nil, source, nil, e
	}
	completeProgress(t, source, rawdir)
	snapshot, e := OpenImportSnapshot(ctx, source, rawdir, source.id.Binding)
	if e != nil {
		return nil, source, nil, e
	}
	w, e := OpenImportWorkspace(ctx, snapshot, source, filepath.Join(t.TempDir(), "scoped-original-index"), jsonSHA("header"), jsonSHA("policy"), false)
	if e != nil {
		return nil, source, snapshot, e
	}
	if !finish {
		return w, source, snapshot, nil
	}
	for n := 0; n < 128; n++ {
		st, sum, e := w.Checkpoint(ctx)
		if e != nil {
			return w, source, snapshot, e
		}
		rows, next, done, e := snapshot.ReadRows(ctx, source, st.Cursor, 1)
		if e != nil {
			return w, source, snapshot, e
		}
		if len(rows) == 0 {
			if !done {
				t.Fatal("source cursor missing raw row")
			}
			_, e = w.CompleteOriginalRawIndex(ctx, sum)
			return w, source, snapshot, e
		}
		changes, e := w.OriginalRawIndexChanges(ctx, rows[0])
		if e != nil {
			return w, source, snapshot, e
		}
		if len(changes) > 4 {
			t.Fatal("one source row exceeded bounded index mutation budget")
		}
		if _, _, e = w.Commit(ctx, sum, "raw", next, "", changes); e != nil {
			return w, source, snapshot, e
		}
	}
	t.Fatal("finite fixture did not finish")
	return w, source, snapshot, ErrChanged
}
func TestOriginalHistorySequenceIndexBindsWholeSealedSourceAndRejectsConflicts(t *testing.T) {
	ctx := context.Background()
	t.Run("actual-sealed-sequence-and-document", func(t *testing.T) {
		w, source, _, e := originalScopeIndexFixture(t, false, true)
		if e != nil {
			t.Fatal(e)
		}
		calls := source.pageCalls
		row, found, e := w.ReadOriginalHistorySourceAtSequence(ctx, "sg_group", 2)
		if e != nil || !found {
			t.Fatal("original sequence locator missing", e)
		}
		raw, ok := row.Mongo()
		if !ok || raw.Collection != "message_delivery_effects_v1" || bson.Raw(raw.Raw).Lookup("seq").Int64() != 2 {
			t.Fatal("wrong actual source")
		}
		doc, found, e := w.ReadOriginalHistoryMessageDocument(ctx, "sg_group:0")
		if e != nil || !found {
			t.Fatal("original doc_id locator missing", e)
		}
		original, _ := doc.Mongo()
		if bson.Raw(original.Raw).Lookup("_id").StringValue() != "actual-physical-document" {
			t.Fatal("doc_id was forged into physical _id")
		}
		if _, found, e = w.ReadOriginalHistorySourceAtSequence(ctx, "sg_group", 1); e != nil || found {
			t.Fatal("complete original sequence absence misreported", e)
		}
		if source.pageCalls != calls {
			t.Fatal("lookup recaptured live source")
		}
	})
	t.Run("unsealed-index-does-not-claim-absence", func(t *testing.T) {
		w, _, _, e := originalScopeIndexFixture(t, false, false)
		if e != nil {
			t.Fatal(e)
		}
		if _, found, e := w.ReadOriginalHistorySourceAtSequence(ctx, "sg_group", 2); e == nil || found {
			t.Fatal("unsealed sequence lookup authorized absence")
		}
	})
	t.Run("two-real-branches-cannot-own-one-sequence", func(t *testing.T) {
		if _, _, _, e := originalScopeIndexFixture(t, true, true); e == nil {
			t.Fatal("duplicate source coordinate overwrote original index")
		}
	})
	t.Run("post-seal-index-mutation-cannot-select-another-source", func(t *testing.T) {
		w, _, snapshot, e := originalScopeIndexFixture(t, false, true)
		if e != nil {
			t.Fatal(e)
		}
		set := DerivedSetID("original-history-sequences", snapshot.Fingerprint())
		key := jsonSHA([]any{"sg_group", int64(2)})
		entry, found, e := w.ReadEntry(ctx, set, key)
		if e != nil || !found {
			t.Fatal(e)
		}
		st, sum, e := w.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, _, e = w.Commit(ctx, sum, st.Phase, st.Cursor, st.Subcursor, []DerivedMutation{{SetID: set, Key: key, ExpectedSHA256: entry.SHA256, Value: []byte(`{"kind":"foreign"}`)}}); e != nil {
			t.Fatal(e)
		}
		if _, _, e = w.ReadOriginalHistorySourceAtSequence(ctx, "sg_group", 2); e == nil {
			t.Fatal("changed index became a source witness")
		}
	})
}
