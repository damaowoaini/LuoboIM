package namespaceinventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// One parent step reads only its exact contiguous <=60 node keys from one
// original checkpoint. A single bounded traversal shares prefix work, not raw
// bytes or a cache. Every visited index and value keeps the original private
// read/hash/strict validation. No context or object is reused by another step.
func (w *ImportWorkspace) readMerkleParentRange(ctx context.Context, expected string, value DerivedMerkleState) (nodes []DerivedMerkleNode, resultErr error) {
	if e := w.check(ctx); e != nil {
		return nil, e
	}
	if !hexText(expected, 64) || !hexText(value.TreeID, 64) || value.Phase != "parents" || value.Level > 64 || value.Width == 0 || value.NextIndex >= value.Width || value.NextIndex%2 != 0 {
		return nil, ErrChanged
	}
	take := min(uint64(60), value.Width-value.NextIndex)
	if take == 0 || value.NextIndex > ^uint64(0)-(take-1) {
		return nil, ErrLimit
	}
	first, last := merkleNodeKey(value.NextIndex), merkleNodeKey(value.NextIndex+(take-1))
	lock, e := lockJournal(w.dir)
	if e != nil {
		return nil, e
	}
	defer func() {
		if e := w.check(ctx); e != nil {
			nodes = nil
			resultErr = errors.Join(resultErr, e)
		}
		if lock.Close() != nil {
			nodes = nil
			resultErr = ErrPrivate
		}
	}()
	state, actual, e := w.load()
	if e != nil || actual != expected {
		return nil, ErrChanged
	}
	store := progressStore{w.dir}
	var set derivedSet
	present, e := store.get(ctx, state.SetsRoot, merkleNodeSet(value.TreeID, value.Level), &set)
	if e != nil || !present || set.Count != value.Width || !hexText(set.Root, 64) {
		return nil, ErrChanged
	}
	// Valid radix prefixes partition the requested interval. Even a malformed
	// sparse tree cannot turn these <=60 keys into an unbounded disk walk.
	visited, maxVisited := uint64(0), 1+64*take
	var visit func(string, string) error
	visit = func(h, prefix string) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if len(prefix) > 64 {
			return ErrChanged
		}
		if prefix+strings.Repeat("f", 64-len(prefix)) < first || prefix+strings.Repeat("0", 64-len(prefix)) > last {
			return nil
		}
		if visited >= maxVisited {
			return ErrLimit
		}
		visited++
		node, e := store.node(h, prefix)
		if e != nil {
			return e
		}
		for _, item := range node.Items {
			if e := ctx.Err(); e != nil {
				return e
			}
			if item.Key < first || item.Key > last {
				continue
			}
			offset := uint64(len(nodes))
			if offset >= take || value.NextIndex > ^uint64(0)-offset || item.Key != merkleNodeKey(value.NextIndex+offset) {
				return ErrChanged
			}
			var ref derivedValue
			if DecodeStrict(item.Value, &ref) != nil || !hexText(ref.SHA256, 64) {
				return ErrChanged
			}
			body, e := ReadPrivateDocument(store.objectPath("derived-value", ref.SHA256), (64<<10)+1)
			var parent DerivedMerkleNode
			if e != nil || digest(body) != ref.SHA256 || !json.Valid(body) || DecodeStrict(body, &parent) != nil || parent.Index != value.NextIndex+offset || !hexText(parent.NodeSHA256, 64) {
				return ErrChanged
			}
			nodes = append(nodes, parent)
		}
		for _, child := range node.Children {
			if e := visit(child.SHA, prefix+child.Digit); e != nil {
				return e
			}
		}
		return nil
	}
	if e = visit(set.Root, ""); e != nil {
		return nil, e
	}
	if uint64(len(nodes)) != take {
		return nil, ErrChanged
	}
	return nodes, nil
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
			nodes, e := w.readMerkleParentRange(ctx, expected, value)
			if e != nil || len(nodes) != 1 {
				return value, ErrChanged
			}
			value.Phase = "complete"
			value.RootSHA256 = nodes[0].NodeSHA256
		} else {
			var nodes []DerivedMerkleNode
			if value.NextIndex < value.Width {
				nodes, e = w.readMerkleParentRange(ctx, expected, value)
				if e != nil {
					return value, ErrChanged
				}
			}
			for at := 0; at < len(nodes); {
				leftIndex := value.NextIndex
				if nodes[at].Index != leftIndex {
					return value, ErrChanged
				}
				sum := nodes[at].NodeSHA256
				at++
				// Comparing before addition keeps the right sibling index in range.
				if leftIndex < value.Width-1 {
					if leftIndex == ^uint64(0) || at >= len(nodes) || nodes[at].Index != leftIndex+1 {
						return value, ErrChanged
					}
					sum = jsonSHA([]string{nodeDomain, sum, nodes[at].NodeSHA256})
					at++
				}
				if value.NextIndex > ^uint64(0)-2 {
					return value, ErrLimit
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

// MerkleProof reads O(log N) actual sibling objects for ONE exact member.
// Only this synchronous proof shares its original checkpoint/journal lock. All
// actual index/value bodies remain fresh private reads; no parsed disk node or
// context survives the invocation. Generic readers and all write paths stay as is.
func (w *ImportWorkspace) MerkleProof(ctx context.Context, tree, key string) (root string, proof []DerivedMerkleSibling, resultErr error) {
	// Match the original MerkleState boundary and its public proof error class.
	if w == nil || w.snapshot == nil || !hexText(tree, 64) {
		return "", nil, ErrChanged
	}
	levelLimit := false
	read := func(readCtx context.Context) (readErr error) {
		lock, e := lockJournal(w.dir)
		if e != nil {
			return ErrChanged
		}
		defer func() {
			if lock.Close() != nil {
				levelLimit = false
				root, proof, readErr = "", nil, ErrChanged
			}
		}()
		state, expected, e := w.load()
		if e != nil {
			return ErrChanged
		}
		store := progressStore{w.dir}
		// This is the original ReadEntry object boundary against the SAME head.
		// Each call still rereads/hash/strict-checks its actual radix path/ref and
		// private value. There is no per-path or per-object decode cache.
		entryAt := func(setID, member string) (DerivedEntry, bool, error) {
			if e := w.check(readCtx); e != nil || !hexText(setID, 64) || !hexText(member, 64) {
				return DerivedEntry{}, false, ErrChanged
			}
			var set derivedSet
			found, e := store.get(readCtx, state.SetsRoot, setID, &set)
			if e != nil || !found {
				return DerivedEntry{}, false, e
			}
			var ref derivedValue
			found, e = store.get(readCtx, set.Root, member, &ref)
			if e != nil || !found {
				return DerivedEntry{}, false, e
			}
			body, e := ReadPrivateDocument(store.objectPath("derived-value", ref.SHA256), (64<<10)+1)
			if e != nil || digest(body) != ref.SHA256 || !json.Valid(body) {
				return DerivedEntry{}, false, ErrChanged
			}
			return DerivedEntry{member, ref.SHA256, append(json.RawMessage(nil), body...)}, true, nil
		}
		entry, found, e := entryAt(DerivedSetID("merkle-jobs", w.snapshot.Fingerprint()), tree)
		var value DerivedMerkleState
		if e != nil || !found || DecodeStrict(entry.Value, &value) != nil || value.TreeID != tree || !hexText(value.SetID, 64) || value.Level > 64 || value.Phase != "complete" {
			return ErrChanged
		}
		if e := w.check(readCtx); e != nil {
			return ErrChanged
		}
		var set derivedSet
		present, e := store.get(readCtx, state.SetsRoot, value.SetID, &set)
		if e != nil || present && (set.Count == 0 || !hexText(set.Root, 64)) {
			return ErrChanged
		}
		if !present {
			set = derivedSet{}
		}
		if set.Root != value.SetRoot || set.Count != value.Count {
			return ErrChanged
		}
		n := set.Count
		entry, exists, e := entryAt(DerivedSetID("merkle-positions", tree), key)
		var pos DerivedMerklePosition
		if e != nil || !exists || DecodeStrict(entry.Value, &pos) != nil || pos.Index >= n {
			return ErrChanged
		}
		index, width, level := pos.Index, n, uint64(0)
		leafDomain, nodeDomain, ok := merkleDomains(value.Domain)
		if !ok {
			return ErrChanged
		}
		sum := jsonSHA([]string{leafDomain, key})
		for width > 1 {
			if level >= 64 {
				levelLimit = true
				return ErrLimit
			}
			sibling := index ^ 1
			if sibling < width {
				var node DerivedMerkleNode
				entry, exists, e = entryAt(merkleNodeSet(tree, level), merkleNodeKey(sibling))
				if e != nil || !exists || DecodeStrict(entry.Value, &node) != nil || node.Index != sibling || !hexText(node.NodeSHA256, 64) {
					return ErrChanged
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
			return ErrChanged
		}
		// Even an out-of-contract edit which ignores the original lock cannot
		// substitute a different checkpoint during this successful proof.
		_, after, e := w.load()
		if e != nil || after != expected {
			return ErrChanged
		}
		root = sum
		return nil
	}
	if scoped, e := checkWorkspaceReadScope(ctx, w); scoped {
		if e != nil {
			return "", nil, ErrChanged
		}
		resultErr = read(ctx)
	} else if !sameReadScopeSource(w.live, w.live) {
		// Ordinary PageSource also permits value receivers. Keep its original
		// first fresh boundary BEFORE lock/load; each entry's w.check below
		// still delegates to checkFresh because this context has no scope.
		if e := w.checkFresh(ctx); e != nil {
			return "", nil, ErrChanged
		}
		resultErr = read(ctx)
	} else {
		// The same existing capability gives unscoped callers the two fresh
		// snapshot checks; Capture's current scope is never nested or escaped.
		resultErr = w.WithSnapshotReadScope(ctx, read)
	}
	if resultErr != nil {
		root, proof = "", nil
		// Preserve the original proof's ErrChanged/64-level ErrLimit contract.
		if levelLimit {
			resultErr = ErrLimit
		} else {
			resultErr = ErrChanged
		}
	}
	return
}
