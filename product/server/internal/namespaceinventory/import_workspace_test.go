package namespaceinventory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	t.Run("batched commit persists every final set and omits intermediate indexes", func(t *testing.T) {
		fresh, e := OpenImportWorkspace(ctx, snapshot, source, filepath.Join(privateFixtureDir(t), "batched"), header, policy, false)
		if e != nil {
			t.Fatal(e)
		}
		oracleDir := filepath.Join(privateFixtureDir(t), "old-index-writer")
		if e = os.Mkdir(oracleDir, 0700); e != nil {
			t.Fatal(e)
		}
		oracle := progressStore{oracleDir}
		sets := map[string]derivedSet{}
		changes := []DerivedMutation{}
		oracleRoot := ""
		for n := 0; n < 16; n++ {
			setID := DerivedSetID("batch-oracle", string(rune('a'+n%2)))
			key := jsonSHA(n)
			raw, marshalErr := json.Marshal(struct {
				Row int `json:"row"`
			}{n})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			changes = append(changes, DerivedMutation{SetID: setID, Key: key, Value: raw})
			// The old non-batch immutable writer is the canonical disk oracle,
			// not a copy of the new Commit/flush implementation.
			valueSHA, saveErr := oracle.save("derived-value", json.RawMessage(raw))
			if saveErr != nil {
				t.Fatal(saveErr)
			}
			set := sets[setID]
			set.Root, e = oracle.put(ctx, set.Root, key, derivedValue{valueSHA})
			if e != nil {
				t.Fatal(e)
			}
			set.Count++
			sets[setID] = set
			oracleRoot, e = oracle.put(ctx, oracleRoot, setID, set)
			if e != nil {
				t.Fatal(e)
			}
		}
		before, expected, e := fresh.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		after, headSHA, e := fresh.Commit(ctx, expected, before.Phase, before.Cursor, before.Subcursor, changes)
		if e != nil || after.SetsRoot != oracleRoot || after.Sequence != before.Sequence+1 {
			t.Fatal("batch changed original COW root/checkpoint", e)
		}
		countIndexes := func(dir string) int {
			entries, e := os.ReadDir(dir)
			if e != nil {
				t.Fatal(e)
			}
			count := 0
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "index-") {
					count++
				}
			}
			return count
		}
		if countIndexes(fresh.dir) >= countIndexes(oracleDir) {
			t.Fatal("unused intermediate index objects were persisted")
		}
		reopened, e := OpenImportWorkspace(ctx, snapshot, source, fresh.dir, header, policy, true)
		if e != nil {
			t.Fatal(e)
		}
		actual, actualSHA, e := reopened.Checkpoint(ctx)
		if e != nil || actual != after || actualSHA != headSHA {
			t.Fatal("batch head not durable across reopen", e)
		}
		for _, change := range changes {
			entry, found, e := reopened.ReadEntry(ctx, change.SetID, change.Key)
			var expectedValue, actualValue struct {
				Row int `json:"row"`
			}
			if e != nil || !found || DecodeStrict(change.Value, &expectedValue) != nil || DecodeStrict(entry.Value, &actualValue) != nil || actualValue != expectedValue {
				t.Fatal("a final touched set tree or private value was not durable", e)
			}
		}
	})
	t.Run("batch final index flush failure never publishes an unresolved root", func(t *testing.T) {
		fresh, e := OpenImportWorkspace(ctx, snapshot, source, filepath.Join(privateFixtureDir(t), "flush-failure"), header, policy, false)
		if e != nil {
			t.Fatal(e)
		}
		before, expected, e := fresh.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		oracleDir := filepath.Join(privateFixtureDir(t), "flush-oracle")
		if e = os.Mkdir(oracleDir, 0700); e != nil {
			t.Fatal(e)
		}
		oracle := progressStore{oracleDir}
		setID, key, raw := DerivedSetID("batch-failure", "set"), jsonSHA("batch-key"), json.RawMessage(`{"batch":true}`)
		valueSHA, e := oracle.save("derived-value", raw)
		if e != nil {
			t.Fatal(e)
		}
		setRoot, e := oracle.put(ctx, "", key, derivedValue{valueSHA})
		if e != nil {
			t.Fatal(e)
		}
		registryRoot, e := oracle.put(ctx, "", setID, derivedSet{Root: setRoot, Count: 1})
		if e != nil {
			t.Fatal(e)
		}
		wrong, e := os.OpenFile((progressStore{fresh.dir}).objectPath("index", registryRoot), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		_, writeErr := wrong.WriteString("{}\n")
		syncErr, closeErr := wrong.Sync(), wrong.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			t.Fatal("actual private index fault could not be established", writeErr, syncErr, closeErr)
		}
		if _, _, e = fresh.Commit(ctx, expected, before.Phase, before.Cursor, before.Subcursor, []DerivedMutation{{SetID: setID, Key: key, Value: raw}}); !errors.Is(e, ErrPrivate) {
			t.Fatal("real final index flush failure was hidden", e)
		}
		actual, actualSHA, e := fresh.Checkpoint(ctx)
		if e != nil || actual != before || actualSHA != expected {
			t.Fatal("failed index flush advanced original head", e)
		}
		if _, found, e := fresh.ReadEntry(ctx, setID, key); e != nil || found {
			t.Fatal("unpublished staged root became visible", e)
		}
	})

	t.Run("coalesced descriptors match sequential disk oracle for interleaved updates", func(t *testing.T) {
		fresh, e := OpenImportWorkspace(ctx, snapshot, source, filepath.Join(privateFixtureDir(t), "descriptor-updates"), header, policy, false)
		if e != nil {
			t.Fatal(e)
		}
		oracleDir := filepath.Join(privateFixtureDir(t), "sequential-descriptor-oracle")
		if e := os.Mkdir(oracleDir, 0700); e != nil {
			t.Fatal(e)
		}
		oracle, oracleRoot := progressStore{oracleDir}, ""
		// Execute the original disk get/put after EVERY mutation, without the
		// new descriptor map or the private batch context. This independent COW
		// oracle authenticates present counts, overwrites and final root bytes.
		applyOracle := func(changes []DerivedMutation) {
			for _, change := range changes {
				var set derivedSet
				_, err := oracle.get(ctx, oracleRoot, change.SetID, &set)
				if err != nil {
					t.Fatal(err)
				}
				var prior derivedValue
				exists, err := oracle.get(ctx, set.Root, change.Key, &prior)
				if err != nil || exists && prior.SHA256 != change.ExpectedSHA256 || !exists && change.ExpectedSHA256 != "" {
					t.Fatal("old disk oracle CAS precondition", err)
				}
				valueSHA, err := oracle.save("derived-value", json.RawMessage(change.Value))
				if err != nil {
					t.Fatal(err)
				}
				set.Root, err = oracle.put(ctx, set.Root, change.Key, derivedValue{valueSHA})
				if err != nil {
					t.Fatal(err)
				}
				if !exists {
					set.Count++
				}
				oracleRoot, err = oracle.put(ctx, oracleRoot, change.SetID, set)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		setIDs := []string{DerivedSetID("descriptor-oracle", "nodes"), DerivedSetID("descriptor-oracle", "positions"), DerivedSetID("descriptor-oracle", "job")}
		seed := []DerivedMutation{}
		for n, setID := range setIDs {
			key := jsonSHA(0)
			if n == 2 {
				key = jsonSHA("job")
			}
			raw, err := json.Marshal(struct {
				Phase string `json:"phase"`
				Row   int    `json:"row"`
			}{"seed", n})
			if err != nil {
				t.Fatal(err)
			}
			seed = append(seed, DerivedMutation{SetID: setID, Key: key, Value: raw})
		}
		checkpoint, expected, e := fresh.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		applyOracle(seed)
		checkpoint, expected, e = fresh.Commit(ctx, expected, checkpoint.Phase, checkpoint.Cursor, checkpoint.Subcursor, seed)
		if e != nil || checkpoint.SetsRoot != oracleRoot {
			t.Fatal("seed differed from original disk writer", e)
		}
		changes := []DerivedMutation{}
		for n := 0; n < 60; n++ {
			change := DerivedMutation{SetID: setIDs[n%2], Key: jsonSHA(n / 2)}
			if n < 2 {
				prior, found, err := fresh.ReadEntry(ctx, change.SetID, change.Key)
				if err != nil || !found {
					t.Fatal(err)
				}
				change.ExpectedSHA256 = prior.SHA256
			}
			change.Value, e = json.Marshal(struct {
				Phase string `json:"phase"`
				Row   int    `json:"row"`
			}{"updated", n})
			if e != nil {
				t.Fatal(e)
			}
			changes = append(changes, change)
		}
		job, found, e := fresh.ReadEntry(ctx, setIDs[2], jsonSHA("job"))
		if e != nil || !found {
			t.Fatal(e)
		}
		changes = append(changes, DerivedMutation{SetID: setIDs[2], Key: job.Key, ExpectedSHA256: job.SHA256, Value: json.RawMessage(`{"phase":"updated-job"}`)})
		applyOracle(changes)
		after, headSHA, e := fresh.Commit(ctx, expected, checkpoint.Phase, checkpoint.Cursor, checkpoint.Subcursor, changes)
		if e != nil || after.Sequence != checkpoint.Sequence+1 || after.SetsRoot != oracleRoot {
			t.Fatal("coalescing changed exact sequential root or checkpoint", e)
		}
		reopened, e := OpenImportWorkspace(ctx, snapshot, source, fresh.dir, header, policy, true)
		if e != nil {
			t.Fatal(e)
		}
		actual, actualSHA, e := reopened.Checkpoint(ctx)
		if e != nil || actual != after || actualSHA != headSHA {
			t.Fatal("coalesced registry not durable across reopen", e)
		}
		for n, setID := range setIDs {
			_, count, err := reopened.DerivedSetIdentity(ctx, setID)
			want := uint64(30)
			if n == 2 {
				want = 1
			}
			if err != nil || count != want {
				t.Fatal("overwrite changed factual set count", n, count, err)
			}
		}
		for _, change := range changes {
			entry, found, err := reopened.ReadEntry(ctx, change.SetID, change.Key)
			want := digest(append(append([]byte(nil), change.Value...), '\n'))
			if err != nil || !found || entry.SHA256 != want {
				t.Fatal("final value/CAS identity changed", err)
			}
		}
	})
	t.Run("later mutation private write failure retains earlier raw and original head", func(t *testing.T) {
		fresh, e := OpenImportWorkspace(ctx, snapshot, source, filepath.Join(privateFixtureDir(t), "descriptor-later-failure"), header, policy, false)
		if e != nil {
			t.Fatal(e)
		}
		before, expected, e := fresh.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		good, bad := json.RawMessage(`{"first":"durable-orphan-only"}`), json.RawMessage(`{"second":"must-fail"}`)
		goodSHA := digest(append(append([]byte(nil), good...), '\n'))
		badSHA := digest(append(append([]byte(nil), bad...), '\n'))
		store := progressStore{fresh.dir}
		fault, e := os.OpenFile(store.objectPath("derived-value", badSHA), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		_, writeErr := fault.WriteString("{}\n")
		syncErr, closeErr := fault.Sync(), fault.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			t.Fatal("real late immutable body fault failed", writeErr, syncErr, closeErr)
		}
		firstSet, firstKey := DerivedSetID("descriptor-failure", "first"), jsonSHA("first")
		changes := []DerivedMutation{{SetID: firstSet, Key: firstKey, Value: good}, {SetID: DerivedSetID("descriptor-failure", "second"), Key: jsonSHA("second"), Value: bad}}
		if _, _, e = fresh.Commit(ctx, expected, before.Phase, before.Cursor, before.Subcursor, changes); !errors.Is(e, ErrPrivate) {
			t.Fatal("later actual private save failure was hidden", e)
		}
		written, e := ReadPrivateDocument(store.objectPath("derived-value", goodSHA), 64<<10)
		if e != nil || digest(written) != goodSHA {
			t.Fatal("first mutation did not reach its original immediate durable writer", e)
		}
		reopened, e := OpenImportWorkspace(ctx, snapshot, source, fresh.dir, header, policy, true)
		if e != nil {
			t.Fatal(e)
		}
		actual, actualSHA, e := reopened.Checkpoint(ctx)
		if e != nil || actual != before || actualSHA != expected {
			t.Fatal("later failure published partial registry/head", e)
		}
		if _, found, e := reopened.ReadEntry(ctx, firstSet, firstKey); e != nil || found {
			t.Fatal("unpublished first mutation became readable", e)
		}
	})
	if source.pageCalls != calls {
		t.Fatal("derived resume recaptured business rows")
	}
}
