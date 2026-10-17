package namespaceinventory

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
}
