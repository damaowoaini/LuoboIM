package namespaceinventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

func TestImportDiskMerkleMatchesCanonicalRootsAndPrivateResume(t *testing.T) {
	for _, count := range []int{0, 1, 3, 64, 65} {
		t.Run(fmt.Sprintf("leaves%d", count), func(t *testing.T) {
			source, _, snapshot := importSnapshotFixture(t)
			ctx := context.Background()
			dir := filepath.Join(privateFixtureDir(t), "merkle")
			header, policy := jsonSHA("header"), jsonSHA("policy")
			w, e := OpenImportWorkspace(ctx, snapshot, source, dir, header, policy, false)
			if e != nil {
				t.Fatal(e)
			}
			set, tree := DerivedSetID("test-merkle", "all-original-members"), jsonSHA([]string{"tree", fmt.Sprint(count)})
			keys := []string{}
			for start := 0; start < count; start += 16 {
				changes := []DerivedMutation{}
				for n := start; n < min(start+16, count); n++ {
					key := jsonSHA([]string{"member", fmt.Sprint(n)})
					keys = append(keys, key)
					body, _ := json.Marshal(struct {
						Index int `json:"index"`
					}{n})
					changes = append(changes, DerivedMutation{SetID: set, Key: key, Value: body})
				}
				st, expected, e := w.Checkpoint(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if _, _, e = w.Commit(ctx, expected, st.Phase, st.Cursor, st.Subcursor, changes); e != nil {
					t.Fatal(e)
				}
			}
			state, e := w.BeginMerkle(ctx, tree, set, "set")
			if e != nil {
				t.Fatal(e)
			}
			for step := 0; state.Phase != "complete" && step < 128; step++ {
				if step == 1 {
					w, e = OpenImportWorkspace(ctx, snapshot, source, dir, header, policy, true)
					if e != nil {
						t.Fatal(e)
					}
				}
				state, e = w.StepMerkle(ctx, tree)
				if e != nil {
					t.Fatal(e)
				}
			}
			expected, e := namespaceimport.SortedSetRoot(keys)
			if e != nil || state.Phase != "complete" || state.RootSHA256 != expected {
				t.Fatal("disk root differs from actual canonical oracle", state, e)
			}
			if count > 0 {
				for _, key := range []string{keys[0], keys[count/2], keys[count-1]} {
					got, proof, e := w.MerkleProof(ctx, tree, key)
					sum := jsonSHA([]string{"namespace-import-set-leaf-v1", key})
					for _, s := range proof {
						if s.Left {
							sum = jsonSHA([]string{"namespace-import-set-node-v1", s.SHA256, sum})
						} else {
							sum = jsonSHA([]string{"namespace-import-set-node-v1", sum, s.SHA256})
						}
					}
					if e != nil || got != expected || sum != expected {
						t.Fatal("actual member proof failed", e)
					}
				}
			}
			if _, _, e = w.MerkleProof(ctx, tree, jsonSHA("foreign")); e == nil {
				t.Fatal("foreign leaf accepted")
			}
			// Changing input set identity after completion cannot reuse the old tree.
			st, before, e := w.Checkpoint(ctx)
			if e != nil {
				t.Fatal(e)
			}
			body := json.RawMessage(`{"changed":true}`)
			if _, _, e = w.Commit(ctx, before, st.Phase, st.Cursor, st.Subcursor, []DerivedMutation{{SetID: set, Key: jsonSHA("new-key"), Value: body}}); e != nil {
				t.Fatal(e)
			}
			if _, e = w.StepMerkle(ctx, tree); e == nil {
				t.Fatal("new derived input reused old Merkle authority")
			}
		})
	}

	// These fixtures use the actual private writer, Merkle leaf steps, persisted
	// journal and original per-key reader. They do not substitute a memory tree.
	parentsFixture := func(t *testing.T) (*ImportWorkspace, string, []string) {
		t.Helper()
		source, _, snapshot := importSnapshotFixture(t)
		ctx := context.Background()
		w, e := OpenImportWorkspace(ctx, snapshot, source, filepath.Join(privateFixtureDir(t), "parents"), jsonSHA("header"), jsonSHA("policy"), false)
		if e != nil {
			t.Fatal(e)
		}
		set, tree := DerivedSetID("parent-range", "original-members"), jsonSHA("parent-range-tree")
		keys := []string{}
		for start := 0; start < 65; start += 16 {
			changes := []DerivedMutation{}
			for n := start; n < min(start+16, 65); n++ {
				key := jsonSHA([]string{"original-parent-member", fmt.Sprint(n)})
				keys = append(keys, key)
				body, e := json.Marshal(struct{ Index int }{n})
				if e != nil {
					t.Fatal(e)
				}
				changes = append(changes, DerivedMutation{SetID: set, Key: key, Value: body})
			}
			st, expected, e := w.Checkpoint(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if _, _, e = w.Commit(ctx, expected, st.Phase, st.Cursor, st.Subcursor, changes); e != nil {
				t.Fatal(e)
			}
		}
		state, e := w.BeginMerkle(ctx, tree, set, "set")
		if e != nil {
			t.Fatal(e)
		}
		for n := 0; state.Phase == "leaves" && n < 4; n++ {
			state, e = w.StepMerkle(ctx, tree)
			if e != nil {
				t.Fatal(e)
			}
		}
		if state.Phase != "parents" || state.Level != 0 || state.Width != 65 || state.NextIndex != 0 {
			t.Fatal("actual leaf producer did not reach original parent checkpoint")
		}
		return w, tree, keys
	}
	t.Run("parents range matches per-key disk oracle across odd tail and reopen", func(t *testing.T) {
		w, tree, keys := parentsFixture(t)
		ctx := context.Background()
		original := make([]DerivedMerkleNode, 65)
		for n := range original {
			entry, found, e := w.ReadEntry(ctx, merkleNodeSet(tree, 0), merkleNodeKey(uint64(n)))
			if e != nil || !found || DecodeStrict(entry.Value, &original[n]) != nil || original[n].Index != uint64(n) {
				t.Fatal("original per-key disk oracle missing actual leaf", e)
			}
		}
		oracle := []DerivedMerkleNode{}
		for n := 0; n < len(original); n += 2 {
			sum := original[n].NodeSHA256
			if n+1 < len(original) {
				sum = jsonSHA([]string{"namespace-import-set-node-v1", sum, original[n+1].NodeSHA256})
			}
			oracle = append(oracle, DerivedMerkleNode{Index: uint64(n / 2), NodeSHA256: sum})
		}
		before, _, e := w.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		state, e := w.StepMerkle(ctx, tree)
		if e != nil || state.Level != 0 || state.Width != 65 || state.NextIndex != 60 {
			t.Fatal("first step did not consume exactly original sixty inputs", e)
		}
		after, _, e := w.Checkpoint(ctx)
		if e != nil || after.Sequence != before.Sequence+1 {
			t.Fatal("parent read introduced extra journal checkpoints", e)
		}
		for n := 0; n < 30; n++ {
			entry, found, e := w.ReadEntry(ctx, merkleNodeSet(tree, 1), merkleNodeKey(uint64(n)))
			var got DerivedMerkleNode
			if e != nil || !found || DecodeStrict(entry.Value, &got) != nil || got != oracle[n] {
				t.Fatal("parent differs from original per-key disk oracle", e)
			}
		}
		if _, found, e := w.ReadEntry(ctx, merkleNodeSet(tree, 1), merkleNodeKey(30)); e != nil || found {
			t.Fatal("first bounded step wrote the next parent", e)
		}
		w, e = OpenImportWorkspace(ctx, w.snapshot, w.live, w.dir, w.header, w.policy, true)
		if e != nil {
			t.Fatal(e)
		}
		state, e = w.StepMerkle(ctx, tree)
		if e != nil || state.Level != 1 || state.Width != 33 || state.NextIndex != 0 {
			t.Fatal("reopen did not consume exact final five inputs", e)
		}
		for n := 30; n < len(oracle); n++ {
			entry, found, e := w.ReadEntry(ctx, merkleNodeSet(tree, 1), merkleNodeKey(uint64(n)))
			var got DerivedMerkleNode
			if e != nil || !found || DecodeStrict(entry.Value, &got) != nil || got != oracle[n] {
				t.Fatal("odd tail differs from original disk oracle", e)
			}
		}
		if oracle[32].NodeSHA256 != original[64].NodeSHA256 {
			t.Fatal("odd tail was duplicated instead of carried")
		}
		for n := 0; state.Phase != "complete" && n < 128; n++ {
			state, e = w.StepMerkle(ctx, tree)
			if e != nil {
				t.Fatal(e)
			}
		}
		want, e := namespaceimport.SortedSetRoot(keys)
		if e != nil || state.Phase != "complete" || state.RootSHA256 != want {
			t.Fatal("reopened parent range changed the canonical root", e)
		}
	})
	t.Run("parents range rejects middle tamper cancellation source change and stale head", func(t *testing.T) {
		w, tree, _ := parentsFixture(t)
		ctx := context.Background()
		before, expected, e := w.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		unchanged := func(want DerivedCheckpoint, sum string) {
			t.Helper()
			after, got, e := w.Checkpoint(ctx)
			if e != nil || got != sum || after != want {
				t.Fatal("failed parent read advanced original head", e)
			}
		}
		entry, found, e := w.ReadEntry(ctx, merkleNodeSet(tree, 0), merkleNodeKey(45))
		if e != nil || !found {
			t.Fatal("middle real private node missing", e)
		}
		path := (progressStore{w.dir}).objectPath("derived-value", entry.SHA256)
		body, e := ReadPrivateDocument(path, (64<<10)+1)
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			if e := os.Chmod(path, 0600); e != nil {
				t.Error(e)
			}
			if e := os.WriteFile(path, body, 0600); e != nil {
				t.Error(e)
			}
		}()
		if e = os.Chmod(path, 0644); e != nil {
			t.Fatal(e)
		}
		if _, e = w.StepMerkle(ctx, tree); e == nil {
			t.Fatal("middle nonprivate node was skipped")
		}
		if e = os.Chmod(path, 0600); e != nil {
			t.Fatal(e)
		}
		unchanged(before, expected)
		if e = os.WriteFile(path, []byte(`{"index":45,"node_sha256":"changed"}`), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = w.StepMerkle(ctx, tree); e == nil {
			t.Fatal("middle actual body hash was not rechecked")
		}
		if e = os.WriteFile(path, body, 0600); e != nil {
			t.Fatal(e)
		}
		unchanged(before, expected)
		// A scoped original cancellation remains cancellation. Do not reinterpret
		// the ordinary snapshot Identity error normalization as a new contract.
		e = w.WithSnapshotReadScope(ctx, func(scoped context.Context) error {
			cancelled, cancel := context.WithCancel(scoped)
			cancel()
			_, e := w.StepMerkle(cancelled, tree)
			return e
		})
		if !errors.Is(e, context.Canceled) {
			t.Fatal("parent step ignored original scoped cancellation", e)
		}
		unchanged(before, expected)
		source := w.live.(*pagedFixture)
		originalIdentity := source.id
		defer func() { source.id = originalIdentity }()
		e = w.WithSnapshotReadScope(ctx, func(scoped context.Context) error {
			source.id.MongoTopology++
			_, e := w.StepMerkle(scoped, tree)
			return e
		})
		source.id = originalIdentity
		if e == nil {
			t.Fatal("parent scope renewed changed source authority")
		}
		unchanged(before, expected)
		// Keep a valid body SHA but duplicate another typed index at the middle
		// key. Exact range identity must not be inferred from count alone.
		var duplicate DerivedMerkleNode
		if DecodeStrict(entry.Value, &duplicate) != nil {
			t.Fatal("original typed middle node")
		}
		duplicate.Index++
		badBody, e := json.Marshal(duplicate)
		if e != nil {
			t.Fatal(e)
		}
		badState, badSHA, e := w.Commit(ctx, expected, before.Phase, before.Cursor, before.Subcursor, []DerivedMutation{{SetID: merkleNodeSet(tree, 0), Key: merkleNodeKey(45), ExpectedSHA256: entry.SHA256, Value: badBody}})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.StepMerkle(ctx, tree); e == nil {
			t.Fatal("valid hashed body duplicated a parent input index")
		}
		unchanged(badState, badSHA)
		badEntry, found, e := w.ReadEntry(ctx, merkleNodeSet(tree, 0), merkleNodeKey(45))
		if e != nil || !found {
			t.Fatal(e)
		}
		before, expected, e = w.Commit(ctx, badSHA, badState.Phase, badState.Cursor, badState.Subcursor, []DerivedMutation{{SetID: merkleNodeSet(tree, 0), Key: merkleNodeKey(45), ExpectedSHA256: badEntry.SHA256, Value: entry.Value}})
		if e != nil {
			t.Fatal("original typed index could not be restored by exact CAS", e)
		}
		// A real intervening checkpoint makes the retained parent-read request
		// stale; it cannot silently enroll the new head under the old expected SHA.
		state, _, found, e := w.MerkleState(ctx, tree)
		if e != nil || !found {
			t.Fatal(e)
		}
		next, nextSHA, e := w.Commit(ctx, expected, before.Phase, before.Cursor, before.Subcursor, nil)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.readMerkleParentRange(ctx, expected, state); !errors.Is(e, ErrChanged) {
			t.Fatal("parent reader adopted a different original head", e)
		}
		unchanged(next, nextSHA)
		if _, e = w.StepMerkle(ctx, tree); e != nil {
			t.Fatal("failed invocation left a reusable cache or damaged the private journal", e)
		}
	})

	leafFixture := func(t *testing.T, bodies []json.RawMessage) (*ImportWorkspace, string, string, []string) {
		t.Helper()
		source, _, snapshot := importSnapshotFixture(t)
		ctx := context.Background()
		w, e := OpenImportWorkspace(ctx, snapshot, source, filepath.Join(privateFixtureDir(t), "leaf-batch"), jsonSHA("header"), jsonSHA("policy"), false)
		if e != nil {
			t.Fatal(e)
		}
		set, tree := DerivedSetID("leaf-batch", "actual-values"), jsonSHA("leaf-batch-tree")
		keys := []string{}
		for start := 0; start < len(bodies); start += 16 {
			changes := []DerivedMutation{}
			for n := start; n < min(start+16, len(bodies)); n++ {
				key := merkleNodeKey(uint64(n))
				keys = append(keys, key)
				changes = append(changes, DerivedMutation{SetID: set, Key: key, Value: bodies[n]})
			}
			st, expected, e := w.Checkpoint(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if _, _, e = w.Commit(ctx, expected, st.Phase, st.Cursor, st.Subcursor, changes); e != nil {
				t.Fatal(e)
			}
		}
		if state, e := w.BeginMerkle(ctx, tree, set, "set"); e != nil || state.Phase != "leaves" {
			t.Fatal("actual leaf job not prepared", e)
		}
		return w, tree, set, keys
	}
	finishLeafRoot := func(t *testing.T, w *ImportWorkspace, tree string, state DerivedMerkleState, keys []string) {
		t.Helper()
		for n := 0; state.Phase != "complete" && n < 128; n++ {
			var e error
			state, e = w.StepMerkle(context.Background(), tree)
			if e != nil {
				t.Fatal(e)
			}
		}
		want, e := namespaceimport.SortedSetRoot(keys)
		if e != nil || state.Phase != "complete" || state.RootSHA256 != want {
			t.Fatal("actual leaf traversal changed canonical root", e)
		}
	}
	t.Run("leaves batch matches original cursor thirty and EOF across reopen", func(t *testing.T) {
		for _, count := range []int{30, 31} {
			bodies := []json.RawMessage{}
			for n := 0; n < count; n++ {
				bodies = append(bodies, json.RawMessage(fmt.Sprintf(`{"leaf":%d}`, n)))
			}
			w, tree, set, keys := leafFixture(t, bodies)
			ctx := context.Background()
			oracle, cursor, done, total, e := w.ReadEntries(ctx, set, "", 30)
			if e != nil || len(oracle) != 30 || done || total != uint64(count) || cursor != keys[29] {
				t.Fatal("original disk oracle did not retain full-thirty boundary", e)
			}
			before, _, e := w.Checkpoint(ctx)
			if e != nil {
				t.Fatal(e)
			}
			state, e := w.StepMerkle(ctx, tree)
			if e != nil || state.Phase != "leaves" || state.LeavesBuilt != 30 || state.Cursor != cursor {
				t.Fatal("exactly thirty incorrectly claimed EOF", e)
			}
			after, _, e := w.Checkpoint(ctx)
			if e != nil || after.Sequence != before.Sequence+1 {
				t.Fatal("leaf read advanced extra private checkpoints", e)
			}
			w, e = OpenImportWorkspace(ctx, w.snapshot, w.live, w.dir, w.header, w.policy, true)
			if e != nil {
				t.Fatal(e)
			}
			oracle, cursor, done, total, e = w.ReadEntries(ctx, set, cursor, 30)
			if e != nil || !done || len(oracle) != count-30 || total != uint64(count) {
				t.Fatal("original EOF oracle changed after reopen", e)
			}
			state, e = w.StepMerkle(ctx, tree)
			if e != nil || state.Phase != "parents" || state.LeavesBuilt != uint64(count) || state.Cursor != cursor {
				t.Fatal("reopened leaf step changed true EOF or cursor", e)
			}
			finishLeafRoot(t, w, tree, state, keys)
		}
	})
	t.Run("leaves byte boundary checks excluded body before cursor or head advances", func(t *testing.T) {
		// Arbitrary JSON strings, booleans and arrays remain valid leaf values.
		// Each private large body is exactly64KiB after the original writer's LF.
		large := json.RawMessage(`"` + strings.Repeat("x", (64<<10)-3) + `"`)
		w, tree, set, keys := leafFixture(t, []json.RawMessage{large, large, large, large, json.RawMessage(`true`), json.RawMessage(`[1,2]`)})
		ctx := context.Background()
		before, expected, e := w.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		unchanged := func() {
			t.Helper()
			after, got, e := w.Checkpoint(ctx)
			if e != nil || after != before || got != expected {
				t.Fatal("excluded bad body advanced original head", e)
			}
		}
		excluded, found, e := w.ReadEntry(ctx, set, keys[4])
		if e != nil || !found {
			t.Fatal("actual excluded candidate missing", e)
		}
		path := (progressStore{w.dir}).objectPath("derived-value", excluded.SHA256)
		body, e := ReadPrivateDocument(path, (64<<10)+1)
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			if e := os.Chmod(path, 0600); e != nil {
				t.Error(e)
			}
			if e := os.WriteFile(path, body, 0600); e != nil {
				t.Error(e)
			}
		}()
		if e = os.Chmod(path, 0644); e != nil {
			t.Fatal(e)
		}
		if _, e = w.StepMerkle(ctx, tree); e == nil {
			t.Fatal("byte-excluded nonprivate body was skipped")
		}
		if e = os.Chmod(path, 0600); e != nil {
			t.Fatal(e)
		}
		unchanged()
		if e = os.WriteFile(path, []byte(`false`), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = w.StepMerkle(ctx, tree); e == nil {
			t.Fatal("byte-excluded body hash was not rechecked")
		}
		if e = os.WriteFile(path, body, 0600); e != nil {
			t.Fatal(e)
		}
		unchanged()
		oracle, cursor, done, total, e := w.ReadEntries(ctx, set, "", 30)
		if e != nil || len(oracle) != 4 || done || total != 6 || cursor != keys[3] {
			t.Fatal("original byte-boundary disk oracle differs", e)
		}
		bytesRead := 0
		for _, entry := range oracle {
			bytesRead += len(entry.Value)
		}
		if bytesRead != 256<<10 {
			t.Fatal("fixture did not exercise exact byte boundary")
		}
		state, e := w.StepMerkle(ctx, tree)
		if e != nil || state.Phase != "leaves" || state.LeavesBuilt != 4 || state.Cursor != cursor {
			t.Fatal("byte-excluded candidate advanced the leaf cursor", e)
		}
		w, e = OpenImportWorkspace(ctx, w.snapshot, w.live, w.dir, w.header, w.policy, true)
		if e != nil {
			t.Fatal(e)
		}
		oracle, cursor, done, total, e = w.ReadEntries(ctx, set, cursor, 30)
		if e != nil || len(oracle) != 2 || !done || total != 6 {
			t.Fatal("restored excluded values missing after reopen", e)
		}
		state, e = w.StepMerkle(ctx, tree)
		if e != nil || state.Phase != "parents" || state.LeavesBuilt != 6 || state.Cursor != cursor {
			t.Fatal("reopened byte-limited batch changed original EOF", e)
		}
		finishLeafRoot(t, w, tree, state, keys)
	})
}
