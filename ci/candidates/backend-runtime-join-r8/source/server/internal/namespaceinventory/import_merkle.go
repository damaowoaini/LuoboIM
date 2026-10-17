package namespaceinventory

import (
	"context"
	"encoding/json"
	"fmt"
)

// A disk Merkle job authenticates one immutable derived set. Every invocation
// advances at most30 leaves/parents. Restart resumes the exact level/index;
// finalization never reloads a whole owner's/CID's set or a complete history.
// This is private integrity/progress only, not a writer/read/Ready capability.
type DerivedMerkleState struct {
	TreeID      string `json:"tree_id"`
	SetID       string `json:"set_id"`
	SetRoot     string `json:"set_root"`
	Domain      string `json:"domain"`
	Count       uint64 `json:"count"`
	Cursor      string `json:"cursor"`
	Level       uint64 `json:"level"`
	Width       uint64 `json:"width"`
	NextIndex   uint64 `json:"next_index"`
	LeavesBuilt uint64 `json:"leaves_built"`
	Phase       string `json:"phase"`
	RootSHA256  string `json:"root_sha256,omitempty"`
}
type DerivedMerkleNode struct {
	Index       uint64 `json:"index"`
	ValueSHA256 string `json:"value_sha256,omitempty"`
	NodeSHA256  string `json:"node_sha256"`
}
type DerivedMerklePosition struct {
	Index uint64 `json:"index"`
}
type DerivedMerkleSibling struct {
	SHA256 string `json:"sha256"`
	Left   bool   `json:"left"`
}

func merkleNodeSet(tree string, level uint64) string {
	return DerivedSetID("merkle-nodes", fmt.Sprintf("%s:%d", tree, level))
}
func merkleNodeKey(index uint64) string { return fmt.Sprintf("%064x", index) }
func merkleDomains(domain string) (leaf, node string, ok bool) {
	switch domain {
	case "set":
		return "namespace-import-set-leaf-v1", "namespace-import-set-node-v1", true
	case "page":
		return "namespace-import-page-leaf-v1", "namespace-import-page-node-v1", true
	}
	return "", "", false
}
func (w *ImportWorkspace) DerivedSetIdentity(ctx context.Context, setID string) (root string, count uint64, resultErr error) {
	if e := w.check(ctx); e != nil {
		return "", 0, e
	}
	if !hexText(setID, 64) {
		return "", 0, ErrInvalid
	}
	lock, e := lockJournal(w.dir)
	if e != nil {
		return "", 0, e
	}
	defer func() {
		if lock.Close() != nil {
			root = ""
			count = 0
			resultErr = ErrPrivate
		}
	}()
	state, _, e := w.load()
	if e != nil {
		return "", 0, e
	}
	var set derivedSet
	ok, e := (progressStore{w.dir}).get(ctx, state.SetsRoot, setID, &set)
	if e != nil {
		return "", 0, e
	}
	if !ok {
		return "", 0, nil
	}
	if set.Count == 0 || !hexText(set.Root, 64) {
		return "", 0, ErrChanged
	}
	return set.Root, set.Count, nil
}
func (w *ImportWorkspace) MerkleState(ctx context.Context, tree string) (value DerivedMerkleState, entry DerivedEntry, found bool, e error) {
	if w == nil || w.snapshot == nil || !hexText(tree, 64) {
		return value, entry, false, ErrInvalid
	}
	entry, found, e = w.ReadEntry(ctx, DerivedSetID("merkle-jobs", w.snapshot.Fingerprint()), tree)
	if e != nil || !found {
		return value, entry, found, e
	}
	if DecodeStrict(entry.Value, &value) != nil || value.TreeID != tree || !hexText(value.SetID, 64) || value.Level > 64 {
		return value, entry, false, ErrChanged
	}
	return value, entry, true, nil
}
func merkleMutation(set, key string, prior DerivedEntry, value any) (DerivedMutation, error) {
	b, e := marshalImportValue(value)
	if e != nil {
		return DerivedMutation{}, e
	}
	return DerivedMutation{SetID: set, Key: key, ExpectedSHA256: prior.SHA256, Value: b}, nil
}
func (w *ImportWorkspace) BeginMerkle(ctx context.Context, tree, setID, domain string) (DerivedMerkleState, error) {
	if !hexText(tree, 64) || !hexText(setID, 64) {
		return DerivedMerkleState{}, ErrInvalid
	}
	if _, _, ok := merkleDomains(domain); !ok {
		return DerivedMerkleState{}, ErrInvalid
	}
	st, expected, e := w.Checkpoint(ctx)
	if e != nil {
		return DerivedMerkleState{}, e
	}
	root, count, e := w.DerivedSetIdentity(ctx, setID)
	if e != nil {
		return DerivedMerkleState{}, e
	}
	old, _, found, e := w.MerkleState(ctx, tree)
	if e != nil {
		return old, e
	}
	if found {
		if old.SetID != setID || old.SetRoot != root || old.Count != count || old.Domain != domain {
			return old, ErrChanged
		}
		return old, nil
	}
	value := DerivedMerkleState{TreeID: tree, SetID: setID, SetRoot: root, Domain: domain, Count: count, Phase: "leaves"}
	if count == 0 {
		if domain != "set" {
			return value, ErrInvalid
		}
		value.Phase = "complete"
		value.RootSHA256 = jsonSHA([]string{"namespace-import-empty-set-v1"})
	}
	change, e := merkleMutation(DerivedSetID("merkle-jobs", w.snapshot.Fingerprint()), tree, DerivedEntry{}, value)
	if e != nil {
		return value, e
	}
	_, _, e = w.Commit(ctx, expected, st.Phase, st.Cursor, st.Subcursor, []DerivedMutation{change})
	return value, e
}
func marshalImportValue(v any) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil || len(b) > 64<<10 {
		return nil, ErrLimit
	}
	return b, nil
}

// StepMerkle preserves the input set identity, original snapshot and every
// private object's SHA. It never memoizes a decoded object across invocations.
func (w *ImportWorkspace) StepMerkle(ctx context.Context, tree string) (DerivedMerkleState, error) {
	checkpoint, expected, e := w.Checkpoint(ctx)
	if e != nil {
		return DerivedMerkleState{}, e
	}
	value, prior, found, e := w.MerkleState(ctx, tree)
	if e != nil || !found {
		return value, ErrChanged
	}
	root, count, e := w.DerivedSetIdentity(ctx, value.SetID)
	if e != nil || root != value.SetRoot || count != value.Count {
		return value, ErrChanged
	}
	if value.Phase == "complete" {
		if !hexText(value.RootSHA256, 64) {
			return value, ErrChanged
		}
		return value, nil
	}
	leafDomain, nodeDomain, ok := merkleDomains(value.Domain)
	if !ok {
		return value, ErrChanged
	}
	changes := []DerivedMutation{}
	if value.Phase == "leaves" {
		entries, next, done, total, e := w.ReadEntries(ctx, value.SetID, value.Cursor, 30)
		if e != nil || total != value.Count {
			return value, ErrChanged
		}
		for _, entry := range entries {
			if value.LeavesBuilt == ^uint64(0) {
				return value, ErrLimit
			}
			node := DerivedMerkleNode{Index: value.LeavesBuilt, ValueSHA256: entry.Key, NodeSHA256: jsonSHA([]string{leafDomain, entry.Key})}
			a, e := merkleMutation(merkleNodeSet(tree, 0), merkleNodeKey(node.Index), DerivedEntry{}, node)
			if e != nil {
				return value, e
			}
			changes = append(changes, a)
			a, e = merkleMutation(DerivedSetID("merkle-positions", tree), entry.Key, DerivedEntry{}, DerivedMerklePosition{node.Index})
			if e != nil {
				return value, e
			}
			changes = append(changes, a)
			value.LeavesBuilt++
		}
		value.Cursor = next
		if done {
			if value.LeavesBuilt != value.Count {
				return value, ErrChanged
			}
			value.Phase = "parents"
			value.Level = 0
			value.Width = value.Count
			value.NextIndex = 0
		}
	} else if value.Phase == "parents" {
		if value.Width < 1 || value.NextIndex > value.Width || value.NextIndex%2 != 0 {
			return value, ErrChanged
		}
		if value.Width == 1 {
			var node DerivedMerkleNode
			entry, ok, e := w.ReadEntry(ctx, merkleNodeSet(tree, value.Level), merkleNodeKey(0))
			if e != nil || !ok || DecodeStrict(entry.Value, &node) != nil || node.Index != 0 || !hexText(node.NodeSHA256, 64) {
				return value, ErrChanged
			}
			value.Phase = "complete"
			value.RootSHA256 = node.NodeSHA256
		} else {
			for n := 0; n < 30 && value.NextIndex < value.Width; n++ {
				leftIndex := value.NextIndex
				var left DerivedMerkleNode
				entry, ok, e := w.ReadEntry(ctx, merkleNodeSet(tree, value.Level), merkleNodeKey(leftIndex))
				if e != nil || !ok || DecodeStrict(entry.Value, &left) != nil || left.Index != leftIndex || !hexText(left.NodeSHA256, 64) {
					return value, ErrChanged
				}
				sum := left.NodeSHA256
				if leftIndex+1 < value.Width {
					var right DerivedMerkleNode
					entry, ok, e = w.ReadEntry(ctx, merkleNodeSet(tree, value.Level), merkleNodeKey(leftIndex+1))
					if e != nil || !ok || DecodeStrict(entry.Value, &right) != nil || right.Index != leftIndex+1 || !hexText(right.NodeSHA256, 64) {
						return value, ErrChanged
					}
					sum = jsonSHA([]string{nodeDomain, sum, right.NodeSHA256})
				}
				node := DerivedMerkleNode{Index: leftIndex / 2, NodeSHA256: sum}
				change, e := merkleMutation(merkleNodeSet(tree, value.Level+1), merkleNodeKey(node.Index), DerivedEntry{}, node)
				if e != nil {
					return value, e
				}
				changes = append(changes, change)
				value.NextIndex += 2
			}
			if value.NextIndex >= value.Width {
				value.Width = value.Width/2 + value.Width%2
				value.Level++
				value.NextIndex = 0
				if value.Level > 64 {
					return value, ErrLimit
				}
			}
		}
	} else {
		return value, ErrChanged
	}
	change, e := merkleMutation(DerivedSetID("merkle-jobs", w.snapshot.Fingerprint()), tree, prior, value)
	if e != nil {
		return value, e
	}
	changes = append(changes, change)
	_, _, e = w.Commit(ctx, expected, checkpoint.Phase, checkpoint.Cursor, checkpoint.Subcursor, changes)
	return value, e
}

// MerkleProof reads O(log N) actual sibling objects for ONE exact member. It
// never accepts an arbitrary index or a root supplied by an operator JSON file.
func (w *ImportWorkspace) MerkleProof(ctx context.Context, tree, key string) (root string, proof []DerivedMerkleSibling, resultErr error) {
	value, _, found, e := w.MerkleState(ctx, tree)
	if e != nil || !found || value.Phase != "complete" {
		return "", nil, ErrChanged
	}
	r, n, e := w.DerivedSetIdentity(ctx, value.SetID)
	if e != nil || r != value.SetRoot || n != value.Count {
		return "", nil, ErrChanged
	}
	entry, exists, e := w.ReadEntry(ctx, DerivedSetID("merkle-positions", tree), key)
	var pos DerivedMerklePosition
	if e != nil || !exists || DecodeStrict(entry.Value, &pos) != nil || pos.Index >= n {
		return "", nil, ErrChanged
	}
	index, width, level := pos.Index, n, uint64(0)
	leafDomain, nodeDomain, ok := merkleDomains(value.Domain)
	if !ok {
		return "", nil, ErrChanged
	}
	sum := jsonSHA([]string{leafDomain, key})
	for width > 1 {
		if level >= 64 {
			return "", nil, ErrLimit
		}
		sibling := index ^ 1
		if sibling < width {
			var node DerivedMerkleNode
			entry, exists, e = w.ReadEntry(ctx, merkleNodeSet(tree, level), merkleNodeKey(sibling))
			if e != nil || !exists || DecodeStrict(entry.Value, &node) != nil || node.Index != sibling || !hexText(node.NodeSHA256, 64) {
				return "", nil, ErrChanged
			}
			left := sibling < index
			proof = append(proof, DerivedMerkleSibling{node.NodeSHA256, left})
			if left {
				sum = jsonSHA([]string{nodeDomain, node.NodeSHA256, sum})
			} else {
				sum = jsonSHA([]string{nodeDomain, sum, node.NodeSHA256})
			}
		}
		index /= 2
		width = width/2 + width%2
		level++
	}
	if sum != value.RootSHA256 {
		return "", nil, ErrChanged
	}
	return sum, proof, nil
}
