package namespaceinventory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestImportWorkspaceResumesBoundedDerivedCASWithoutSourceRecapture(t *testing.T) {
	source, _, snapshot := importSnapshotFixture(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "derived")
	header, policy := jsonSHA("header"), jsonSHA("policy")
	w, e := OpenImportWorkspace(ctx, snapshot, source, dir, header, policy, false)
	if e != nil {
		t.Fatal(e)
	}
	calls := source.pageCalls
	st, expected, e := w.Checkpoint(ctx)
	if e != nil {
		t.Fatal(e)
	}
	set, key := DerivedSetID("test", "original-custody"), jsonSHA("value")
	raw, _ := json.Marshal(map[string]string{"fact": "not-a-grant"})
	next, sum, e := w.Commit(ctx, expected, "raw", key, "", []DerivedMutation{{SetID: set, Key: key, Value: raw}})
	if e != nil || next.Sequence != st.Sequence+1 {
		t.Fatal(e)
	}
	t.Run("reopen exact checkpoint and private body", func(t *testing.T) {
		reopened, e := OpenImportWorkspace(ctx, snapshot, source, dir, header, policy, true)
		if e != nil {
			t.Fatal(e)
		}
		again, sha, e := reopened.Checkpoint(ctx)
		if e != nil || sha != sum || again.SetsRoot != next.SetsRoot {
			t.Fatal("progress reset", e)
		}
		value, ok, e := reopened.ReadEntry(ctx, set, key)
		var got map[string]string
		if e != nil || !ok || DecodeStrict(value.Value, &got) != nil || got["fact"] != "not-a-grant" {
			t.Fatal(e)
		}
	})
	t.Run("stale expected state and missing value CAS rejected", func(t *testing.T) {
		if _, _, e := w.Commit(ctx, expected, "raw", key, "", nil); e == nil {
			t.Fatal("stale root accepted")
		}
		if _, _, e := w.Commit(ctx, sum, "raw", key, "", []DerivedMutation{{SetID: set, Key: key, Value: raw}}); e == nil {
			t.Fatal("overwrote previous value without exact SHA")
		}
	})
	t.Run("actual source restart rejects derived read", func(t *testing.T) {
		source.id.MongoTopology++
		if _, _, e := w.ReadEntry(ctx, set, key); e == nil {
			t.Fatal("restart renewed derived state")
		}
		source.id.MongoTopology--
	})
	if source.pageCalls != calls {
		t.Fatal("derived resume recaptured business rows")
	}
}
