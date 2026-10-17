package namespaceinventory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func importSnapshotFixture(t *testing.T) (*pagedFixture, string, *ImportSnapshot) {
	t.Helper()
	source, dir := pageFixture(t), chunkDir(t)
	source.id.TenantAttested = true
	ctx := context.Background()
	if _, e := BeginResumableCapture(ctx, source, source.id.Binding, dir, chunkFixtureLimits(), ProgressBudget{Steps: 1, SourcePages: 1}); e != nil {
		t.Fatal(e)
	}
	completeProgress(t, source, dir)
	snapshot, e := OpenImportSnapshot(ctx, source, dir, source.id.Binding)
	if e != nil {
		t.Fatal(e)
	}
	return source, dir, snapshot
}
func TestImportSnapshotReadsExactRawReferencesWithoutSourceRecapture(t *testing.T) {
	source, _, snapshot := importSnapshotFixture(t)
	before := source.pageCalls
	after := ""
	rows := int64(0)
	var first ImportRawRow
	for n := 0; n < 1024; n++ {
		batch, next, done, e := snapshot.ReadRows(context.Background(), source, after, 2)
		if e != nil || len(batch) > 2 {
			t.Fatal("bounded raw custody read", e)
		}
		if len(batch) > 0 && rows == 0 {
			first = batch[0]
		}
		rows += int64(len(batch))
		after = next
		if done {
			break
		}
		if n == 1023 {
			t.Fatal("bounded cursor failed to finish")
		}
	}
	if rows != snapshot.Identity().UniqueRows || source.pageCalls != before || snapshot.Identity().Trust != State {
		t.Fatal("raw evidence lost, recaptured, or promoted to authority")
	}
	object, record, position := first.Reference()
	again, e := snapshot.ReadReference(context.Background(), source, first.Source(), object, record, position)
	if e != nil {
		t.Fatal(e)
	}
	if raw, ok := again.Mongo(); ok && len(raw.Raw) > 0 {
		raw.Raw[0] ^= 0xff
		if _, e = snapshot.ReadReference(context.Background(), source, first.Source(), object, record, position); e != nil {
			t.Fatal("caller mutated sealed custody")
		}
	}
	if _, e = snapshot.ReadReference(context.Background(), source, first.Source(), object, record, position+1); e == nil {
		t.Fatal("foreign slot accepted")
	}
}
func TestImportSnapshotRejectsIdentityDriftAndRawTamper(t *testing.T) {
	t.Run("restart", func(t *testing.T) {
		source, _, snapshot := importSnapshotFixture(t)
		source.id.MongoTopology++
		if _, _, _, e := snapshot.ReadRows(context.Background(), source, "", 1); e == nil {
			t.Fatal("source restart/topology drift accepted")
		}
	})
	t.Run("raw-tamper", func(t *testing.T) {
		source, dir, snapshot := importSnapshotFixture(t)
		rows, _, _, e := snapshot.ReadRows(context.Background(), source, "", 1)
		if e != nil || len(rows) != 1 {
			t.Fatal(e)
		}
		object, record, position := rows[0].Reference()
		entries, e := os.ReadDir(dir)
		if e != nil {
			t.Fatal(e)
		}
		var path string
		for _, entry := range entries {
			if entry.Name() == "pagebody-"+object+".json" {
				path = filepath.Join(dir, entry.Name())
			}
		}
		if path == "" {
			t.Fatal("expected original page body missing")
		}
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
		if e != nil {
			t.Fatal(e)
		}
		f.Write([]byte("{}\n"))
		f.Close()
		if _, e = snapshot.ReadReference(context.Background(), source, rows[0].Source(), object, record, position); e == nil {
			t.Fatal("tampered raw page accepted")
		}
	})
}
