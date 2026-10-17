package namespaceinventory

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func originalIndexFixture(t *testing.T, finish bool) (*ImportWorkspace, *pagedFixture, *ImportSnapshot, ImportRawRow) {
	t.Helper()
	source := pageFixture(t)
	source.id.TenantAttested = true
	// The older collision classifier fixture intentionally repeats the same msg
	// _id with different bodies. A real Mongo inventory cannot contain that pair;
	// this exact-object test uses one physical row, not an impossible _id set.
	source.rows["msg"] = source.rows["msg"][:1]
	// This exact-id helper must actually provide an _id. pageFixture's message
	// model has only doc_id; without this local identity, chosen remained empty.
	var doc bson.M
	if e := bson.Unmarshal(source.rows["msg"][0].Raw, &doc); e != nil {
		t.Fatal(e)
	}
	doc["_id"] = "original-string-id"
	body, e := bson.Marshal(doc)
	if e != nil {
		t.Fatal(e)
	}
	source.rows["msg"][0].Raw = body
	rawdir := chunkDir(t)
	ctx := context.Background()
	if _, e := BeginResumableCapture(ctx, source, source.id.Binding, rawdir, chunkFixtureLimits(), ProgressBudget{Steps: 1, SourcePages: 1}); e != nil {
		t.Fatal(e)
	}
	completeProgress(t, source, rawdir)
	snapshot, e := OpenImportSnapshot(ctx, source, rawdir, source.id.Binding)
	if e != nil {
		t.Fatal(e)
	}
	w, e := OpenImportWorkspace(ctx, snapshot, source, filepath.Join(privateFixtureDir(t), "original-index"), jsonSHA("header"), jsonSHA("policy"), false)
	if e != nil {
		t.Fatal(e)
	}
	var chosen ImportRawRow
	if !finish {
		return w, source, snapshot, chosen
	}
	for n := 0; n < 128; n++ {
		st, sum, e := w.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		rows, next, done, e := snapshot.ReadRows(ctx, source, st.Cursor, 1)
		if e != nil {
			t.Fatal(e)
		}
		if len(rows) == 0 {
			if !done {
				t.Fatal("missing original row")
			}
			if _, e = w.CompleteOriginalRawIndex(ctx, sum); e != nil {
				t.Fatal(e)
			}
			return w, source, snapshot, chosen
		}
		if row, ok := rows[0].Mongo(); ok && row.Collection == "msg" {
			if _, ok := bson.Raw(row.Raw).Lookup("_id").StringValueOK(); ok {
				chosen = rows[0]
			}
		}
		changes, e := w.OriginalRawIndexChanges(ctx, rows[0])
		if e != nil || len(changes) > 4 {
			t.Fatal("bounded original index", e)
		}
		if _, _, e = w.Commit(ctx, sum, "raw", next, "", changes); e != nil {
			t.Fatal(e)
		}
	}
	t.Fatal("finite fixture did not finish")
	return nil, nil, nil, chosen
}
func TestOriginalHistoryIndexReadsSealedRowsAndNeverGuessesAbsence(t *testing.T) {
	ctx := context.Background()
	t.Run("incomplete original index cannot certify absence", func(t *testing.T) {
		w, _, _, _ := originalIndexFixture(t, false)
		if _, found, e := w.ReadOriginalMongoRecord(ctx, "msg", "missing"); e == nil || found {
			t.Fatal("unsealed absence accepted")
		}
		_, sum, e := w.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.CompleteOriginalRawIndex(ctx, sum); e == nil {
			t.Fatal("unconsumed raw rows sealed")
		}
	})
	t.Run("exact original row and absence without recapture", func(t *testing.T) {
		w, source, _, chosen := originalIndexFixture(t, true)
		raw, ok := chosen.Mongo()
		if !ok {
			t.Fatal("missing string-ID fixture")
		}
		id := bson.Raw(raw.Raw).Lookup("_id").StringValue()
		calls := source.pageCalls
		row, found, e := w.ReadOriginalMongoRecord(ctx, "msg", id)
		if e != nil || !found || row.Source() != chosen.Source() {
			t.Fatal("original lookup failed", e)
		}
		a, b, p := row.Reference()
		x, y, z := chosen.Reference()
		if a != x || b != y || p != z {
			t.Fatal("original reference replaced")
		}
		copy, _ := row.Mongo()
		copy.Raw[0] ^= 0xff
		if _, found, e = w.ReadOriginalMongoRecord(ctx, "msg", id); e != nil || !found {
			t.Fatal("returned BSON changed custody", e)
		}
		if _, found, e = w.ReadOriginalMongoRecord(ctx, "msg", "definitely-absent-original"); e != nil || found {
			t.Fatal("actual absent original misreported", e)
		}
		if source.pageCalls != calls {
			t.Fatal("original lookup recaptured source pages")
		}
	})
	t.Run("post-seal lookup root mutation is rejected", func(t *testing.T) {
		w, _, _, chosen := originalIndexFixture(t, true)
		raw, ok := chosen.Mongo()
		if !ok {
			t.Fatal("fixture")
		}
		id := bson.Raw(raw.Raw).Lookup("_id").StringValue()
		set, key := DerivedSetID("mongo-objects", "msg"), digest([]byte(id))
		entry, found, e := w.ReadEntry(ctx, set, key)
		if e != nil || !found {
			t.Fatal(e)
		}
		st, sum, e := w.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, _, e = w.Commit(ctx, sum, st.Phase, st.Cursor, st.Subcursor, []DerivedMutation{{SetID: set, Key: key, ExpectedSHA256: entry.SHA256, Value: []byte(`{"object_sha256":"wrong"}`)}}); e != nil {
			t.Fatal(e)
		}
		if _, _, e = w.ReadOriginalMongoRecord(ctx, "msg", id); e == nil {
			t.Fatal("changed original index accepted")
		}
	})
	t.Run("source restart rejects present and absence", func(t *testing.T) {
		w, source, _, chosen := originalIndexFixture(t, true)
		raw, _ := chosen.Mongo()
		id := bson.Raw(raw.Raw).Lookup("_id").StringValue()
		source.id.MongoTopology++
		for _, key := range []string{id, "missing"} {
			if _, _, e := w.ReadOriginalMongoRecord(ctx, "msg", key); e == nil {
				t.Fatal("restart renewed source")
			}
		}
	})
	t.Run("original raw body tamper rejected", func(t *testing.T) {
		w, _, snapshot, chosen := originalIndexFixture(t, true)
		raw, _ := chosen.Mongo()
		id := bson.Raw(raw.Raw).Lookup("_id").StringValue()
		object, _, _ := chosen.Reference()
		if e := os.WriteFile(filepath.Join(snapshot.ImportSnapshotPath(), "pagebody-"+object+".json"), []byte("{}\n"), 0600); e != nil {
			t.Fatal(e)
		}
		if _, _, e := w.ReadOriginalMongoRecord(ctx, "msg", id); e == nil {
			t.Fatal("raw custody tamper accepted")
		}
	})
	t.Run("unknown collection is not sealed absence", func(t *testing.T) {
		w, _, _, _ := originalIndexFixture(t, true)
		if _, found, e := w.ReadOriginalMongoRecord(ctx, "unknown-history", "missing"); e == nil || found {
			t.Fatal("unknown namespace guessed absent")
		}
	})
}
