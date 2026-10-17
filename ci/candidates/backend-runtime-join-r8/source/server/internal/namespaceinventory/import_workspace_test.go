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
	dir := filepath.Join(privateFixtureDir(t), "derived")
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
	t.Run("same invocation scope rejects foreign workspace source and retired context", func(t *testing.T) {
		other, e := OpenImportWorkspace(ctx, snapshot, source, filepath.Join(privateFixtureDir(t), "other-derived"), header, policy, false)
		if e != nil {
			t.Fatal(e)
		}
		var escaped context.Context
		if e = w.WithSnapshotReadScope(ctx, func(scoped context.Context) error {
			escaped = scoped
			if _, _, e := other.Checkpoint(scoped); !errors.Is(e, ErrInvalid) {
				t.Fatal("scope crossed workspace", e)
			}
			if _, _, e := w.Checkpoint(scoped); e != nil {
				t.Fatal(e)
			}
			replacement := *source
			w.live = &replacement
			_, _, foreignErr := w.Checkpoint(scoped)
			w.live = source
			if !errors.Is(foreignErr, ErrInvalid) {
				t.Fatal("scope adopted another source object", foreignErr)
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		if _, _, e := w.Checkpoint(escaped); !errors.Is(e, ErrInvalid) {
			t.Fatal("retired context reused snapshot validation", e)
		}
		if _, _, e := w.Checkpoint(ctx); e != nil {
			t.Fatal("ordinary fresh path changed", e)
		}
	})
	t.Run("scope source change still fails actual commit and final snapshot check", func(t *testing.T) {
		before, expected, e := w.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		original := source.id
		defer func() { source.id = original }()
		e = w.WithSnapshotReadScope(ctx, func(scoped context.Context) error {
			source.id.MongoTopology++
			if _, _, e := w.Commit(scoped, expected, before.Phase, before.Cursor, before.Subcursor, nil); e == nil {
				t.Fatal("commit borrowed scope after actual source changed")
			}
			return nil
		})
		if e == nil {
			t.Fatal("scope end accepted changed original source")
		}
		source.id = original
		if after, actual, e := w.Checkpoint(ctx); e != nil || actual != expected || after.Sequence != before.Sequence {
			t.Fatal("rejected commit advanced checkpoint", e)
		}
	})
	t.Run("scope private file tamper cannot bypass commit or final recheck", func(t *testing.T) {
		before, expected, e := w.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		sessionPath := filepath.Join(snapshot.dir, "session.json")
		defer func() {
			if e := os.Chmod(sessionPath, 0600); e != nil {
				t.Error(e)
			}
		}()
		e = w.WithSnapshotReadScope(ctx, func(scoped context.Context) error {
			if e := os.Chmod(sessionPath, 0644); e != nil {
				t.Fatal(e)
			}
			if _, _, e := w.Commit(scoped, expected, before.Phase, before.Cursor, before.Subcursor, nil); e == nil {
				t.Fatal("commit borrowed scope after private custody tamper")
			}
			return nil
		})
		if e == nil {
			t.Fatal("scope end accepted nonprivate original session")
		}
		if e := os.Chmod(sessionPath, 0600); e != nil {
			t.Fatal(e)
		}
		if after, actual, e := w.Checkpoint(ctx); e != nil || actual != expected || after.Sequence != before.Sequence {
			t.Fatal("private tamper advanced checkpoint", e)
		}
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
