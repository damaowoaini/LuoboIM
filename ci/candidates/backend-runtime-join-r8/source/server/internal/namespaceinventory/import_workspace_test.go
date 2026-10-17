package namespaceinventory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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
	lockPath := filepath.Join(dir, "lock")
	lockBefore, e := ReadPrivateDocument(lockPath, 1024)
	var lockHeader struct {
		Version int `json:"version"`
	}
	if e != nil || DecodeStrict(lockBefore, &lockHeader) != nil || lockHeader.Version != 1 {
		t.Fatal("fresh workspace did not retain its admitted private lock", e)
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
	t.Run("resume never creates a missing private lock", func(t *testing.T) {
		if e := os.Remove(lockPath); e != nil {
			t.Fatal(e)
		}
		defer func() {
			if e := os.WriteFile(lockPath, lockBefore, 0600); e != nil {
				t.Error(e)
			}
		}()
		if _, e := OpenImportWorkspace(ctx, snapshot, source, dir, header, policy, true); !errors.Is(e, ErrPrivate) {
			t.Fatal("resume repaired or accepted a missing lock", e)
		}
		if _, e := os.Lstat(lockPath); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("failed resume created a lock", e)
		}
	})
	t.Run("resume rejects a public lock without changing its mode", func(t *testing.T) {
		if e := os.Chmod(lockPath, 0644); e != nil {
			t.Fatal(e)
		}
		defer func() {
			if e := os.Chmod(lockPath, 0600); e != nil {
				t.Error(e)
			}
		}()
		if _, e := OpenImportWorkspace(ctx, snapshot, source, dir, header, policy, true); !errors.Is(e, ErrPrivate) {
			t.Fatal("unsafe lock custody accepted", e)
		}
		info, e := os.Lstat(lockPath)
		if e != nil || info.Mode().Perm() != 0644 {
			t.Fatal("resume silently changed lock custody", e)
		}
	})
	if source.pageCalls != calls {
		t.Fatal("derived resume recaptured business rows")
	}
}
