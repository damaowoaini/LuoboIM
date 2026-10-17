package namespaceinventory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestImportOutputDocumentKeepsPrivateCustodyAcrossReopenAndTamper(t *testing.T) {
	source, _, snapshot := importSnapshotFixture(t)
	ctx := context.Background()
	dir := filepath.Join(privateFixtureDir(t), "output")
	w, e := OpenImportWorkspace(ctx, snapshot, source, dir, jsonSHA("header"), jsonSHA("policy"), false)
	if e != nil {
		t.Fatal(e)
	}
	calls := source.pageCalls
	body := json.RawMessage(`{"fact":"synthetic-page-only"}`)
	sum, e := w.StoreOutputDocument(ctx, body, 1024)
	if e != nil {
		t.Fatal(e)
	}
	t.Run("exclusive same-body replay and reopen preserve SHA", func(t *testing.T) {
		again, e := w.StoreOutputDocument(ctx, body, 1024)
		if e != nil || again != sum {
			t.Fatal("immutable body replay changed", e)
		}
		next, e := OpenImportWorkspace(ctx, snapshot, source, dir, jsonSHA("header"), jsonSHA("policy"), true)
		if e != nil {
			t.Fatal(e)
		}
		got, e := next.ReadOutputDocument(ctx, sum, 1024)
		if e != nil || digest(got) != sum {
			t.Fatal("private output changed", e)
		}
		got[0] = '['
		if _, e = next.ReadOutputDocument(ctx, sum, 1024); e != nil {
			t.Fatal("caller mutated custody", e)
		}
	})
	t.Run("body budget rejects without advancing derived state", func(t *testing.T) {
		before, expected, e := w.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.StoreOutputDocument(ctx, body, 16); e == nil {
			t.Fatal("invalid private bound accepted")
		}
		after, sum, e := w.Checkpoint(ctx)
		if e != nil || sum != expected || before != after {
			t.Fatal("rejected document changed progress")
		}
	})
	t.Run("actual private bytes tamper and alias reject", func(t *testing.T) {
		path := (progressStore{dir}).objectPath("import-output", sum)
		original, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, []byte("{}\n"), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = w.ReadOutputDocument(ctx, sum, 1024); e == nil {
			t.Fatal("changed output accepted")
		}
		if e = os.WriteFile(path, original, 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.Remove(path); e != nil {
			t.Fatal(e)
		}
		if e = os.Symlink("DERIVED-HEAD", path); e != nil {
			t.Fatal(e)
		}
		if _, e = w.ReadOutputDocument(ctx, sum, 1024); e == nil {
			t.Fatal("output symlink accepted")
		}
	})
	if source.pageCalls != calls {
		t.Fatal("output documents recaptured source")
	}
}
