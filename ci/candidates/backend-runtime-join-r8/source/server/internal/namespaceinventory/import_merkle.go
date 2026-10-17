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
	return w.readMerkleParentRangeInner(ctx, value, set)
}

// The caller owns the original journal lock and has authenticated this set.
func (w *ImportWorkspace) readMerkleParentRangeInner(ctx context.Context, value DerivedMerkleState, set derivedSet) (nodes []DerivedMerkleNode, resultErr error) {
	store := progressStore{w.dir}
	take := min(uint64(60), value.Width-value.NextIndex)
	first, last := merkleNodeKey(value.NextIndex), merkleNodeKey(value.NextIndex+(take-1))
	var e error
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

// Only one bounded leaf-step traversal is shared. Values remain arbitrary
// JSON; even the over-byte candidate retains its actual private read and SHA.
func (w *ImportWorkspace) readMerkleLeafBatch(ctx context.Context, expected string, value DerivedMerkleState) (keys []string, next string, done bool, resultErr error) {
	if e := w.check(ctx); e != nil {
		return nil, "", false, e
	}
	if !hexText(expected, 64) || !hexText(value.SetID, 64) || !hexText(value.SetRoot, 64) || value.Count == 0 || value.Phase != "leaves" || value.Cursor != "" && !hexText(value.Cursor, 64) {
		return nil, "", false, ErrChanged
	}
	lock, e := lockJournal(w.dir)
	if e != nil {
		return nil, "", false, e
	}
	defer func() {
		if e := w.check(ctx); e != nil {
			keys, next, done = nil, "", false
			resultErr = errors.Join(resultErr, e)
		}
		if lock.Close() != nil {
			keys, next, done = nil, "", false
			resultErr = ErrPrivate
		}
	}()
	state, actual, e := w.load()
	if e != nil || actual != expected {
		return nil, "", false, ErrChanged
	}
	store := progressStore{w.dir}
	var set derivedSet
	present, e := store.get(ctx, state.SetsRoot, value.SetID, &set)
	if e != nil || !present || set.Root != value.SetRoot || set.Count != value.Count {
		return nil, "", false, ErrChanged
	}
	return w.readMerkleLeafBatchInner(ctx, value, set)
}

// The caller owns the same original journal lock; no reader capability escapes.
func (w *ImportWorkspace) readMerkleLeafBatchInner(ctx context.Context, value DerivedMerkleState, set derivedSet) (keys []string, next string, done bool, resultErr error) {
	store := progressStore{w.dir}
	var e error
	next = value.Cursor
	bytesRead, visited, stopped := 0, 0, false
	// A producer trie has no empty child subtrees. Thirty result paths, one
	// over-byte candidate and one cursor-boundary path each have <=64 ancestors.
	// A malicious tree with extra empty subtrees cannot extend this disk walk.
	const maximumNodes = 1 + 64*(30+2)
	var visit func(string, string) error
	visit = func(h, prefix string) error {
		if stopped {
			return nil
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if len(prefix) > 64 {
			return ErrChanged
		}
		if value.Cursor != "" && prefix+strings.Repeat("f", 64-len(prefix)) <= value.Cursor {
			return nil
		}
		if visited >= maximumNodes {
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
			if item.Key <= value.Cursor {
				continue
			}
			if item.Key <= next {
				return ErrChanged
			}
			var ref derivedValue
			if DecodeStrict(item.Value, &ref) != nil || !hexText(ref.SHA256, 64) {
				return ErrChanged
			}
			body, e := ReadPrivateDocument(store.objectPath("derived-value", ref.SHA256), (64<<10)+1)
			if e != nil || digest(body) != ref.SHA256 || !json.Valid(body) {
				return ErrChanged
			}
			// Exactly like ReadEntries, authenticate the excluded candidate BEFORE
			// checking the byte budget, without moving the cursor or claiming EOF.
			if bytesRead+len(body) > 256<<10 {
				stopped = true
				return nil
			}
			bytesRead += len(body)
			keys = append(keys, item.Key)
			next = item.Key
			if len(keys) == 30 {
				stopped = true
				return nil
			}
		}
		for _, child := range node.Children {
			if e := visit(child.SHA, prefix+child.Digit); e != nil {
				return e
			}
		}
		return nil
	}
	if e = visit(set.Root, ""); e != nil {
		return nil, "", false, e
	}
	return keys, next, !stopped, nil
}

// This is only one invocation's authenticated read result, never a permit or
// a reusable read scope. Commit still checks the original head after unlock.
type merkleStepInput struct {
	checkpoint DerivedCheckpoint
	expected   string
	value      DerivedMerkleState
	prior      DerivedEntry
	keys       []string
	next       string
	done       bool
	nodes      []DerivedMerkleNode
}

// Read the head, actual job ref/body and input descriptor once under the same
// journal lock as this step's bounded traversal. Generic readers stay unchanged.
func (w *ImportWorkspace) readMerkleStep(ctx context.Context, tree string) (input merkleStepInput, resultErr error) {
	if e := w.check(ctx); e != nil {
		return input, e
	}
	lock, e := lockJournal(w.dir)
	if e != nil {
		return input, e
	}
	defer func() {
		if e := w.check(ctx); e != nil {
			input.keys, input.next, input.done, input.nodes = nil, "", false, nil
			resultErr = errors.Join(resultErr, e)
		}
		if lock.Close() != nil {
			input.keys, input.next, input.done, input.nodes = nil, "", false, nil
			resultErr = ErrPrivate
		}
	}()
	input.checkpoint, input.expected, e = w.load()
	if e != nil {
		return input, e
	}
	if !hexText(tree, 64) {
		return input, ErrChanged
	}
	store := progressStore{w.dir}
	var jobs derivedSet
	found, e := store.get(ctx, input.checkpoint.SetsRoot, DerivedSetID("merkle-jobs", w.snapshot.Fingerprint()), &jobs)
	if e != nil || !found {
		return input, ErrChanged
	}
	var ref derivedValue
	found, e = store.get(ctx, jobs.Root, tree, &ref)
	if e != nil || !found {
		return input, ErrChanged
	}
	// The actual ReadEntry private/hash/JSON boundary is retained, not replaced
	// by the job's typed fields or a descriptor SHA supplied by its caller.
	body, e := ReadPrivateDocument(store.objectPath("derived-value", ref.SHA256), (64<<10)+1)
	if e != nil || digest(body) != ref.SHA256 || !json.Valid(body) {
		return input, ErrChanged
	}
	input.prior = DerivedEntry{tree, ref.SHA256, append(json.RawMessage(nil), body...)}
	if DecodeStrict(input.prior.Value, &input.value) != nil || input.value.TreeID != tree || !hexText(input.value.SetID, 64) || input.value.Level > 64 {
		return input, ErrChanged
	}
	value := input.value
	var set derivedSet
	present, e := store.get(ctx, input.checkpoint.SetsRoot, value.SetID, &set)
	if e != nil || present && (set.Count == 0 || !hexText(set.Root, 64)) {
		return input, ErrChanged
	}
	// An absent set keeps the original factual empty identity ("",0). It does
	// not become an open source, a new counter or a newly granted member set.
	if !present {
		set = derivedSet{}
	}
	if set.Root != value.SetRoot || set.Count != value.Count {
		return input, ErrChanged
	}
	if value.Phase == "complete" {
		if !hexText(value.RootSHA256, 64) {
			return input, ErrChanged
		}
		return input, nil
	}
	if _, _, ok := merkleDomains(value.Domain); !ok {
		return input, ErrChanged
	}
	switch value.Phase {
	case "leaves":
		if !hexText(value.SetRoot, 64) || value.Count == 0 || value.Cursor != "" && !hexText(value.Cursor, 64) {
			return input, ErrChanged
		}
		input.keys, input.next, input.done, e = w.readMerkleLeafBatchInner(ctx, value, set)
	case "parents":
		if value.Width < 1 || value.NextIndex > value.Width || value.NextIndex%2 != 0 {
			return input, ErrChanged
		}
		if value.Width == 1 || value.NextIndex < value.Width {
			if value.NextIndex >= value.Width {
				return input, ErrChanged
			}
			take := min(uint64(60), value.Width-value.NextIndex)
			if take == 0 || value.NextIndex > ^uint64(0)-(take-1) {
				return input, ErrLimit
			}
			var parents derivedSet
			present, e = store.get(ctx, input.checkpoint.SetsRoot, merkleNodeSet(tree, value.Level), &parents)
			if e != nil || !present || parents.Count != value.Width || !hexText(parents.Root, 64) {
				return input, ErrChanged
			}
			input.nodes, e = w.readMerkleParentRangeInner(ctx, value, parents)
		}
	default:
		return input, ErrChanged
	}
	if e != nil {
		input.keys, input.next, input.done, input.nodes = nil, "", false, nil
		return input, ErrChanged
	}
	return input, nil
}

// StepMerkle preserves the input set identity, original snapshot and every
// private object's SHA. It never memoizes a decoded object across invocations.
func (w *ImportWorkspace) StepMerkle(ctx context.Context, tree string) (DerivedMerkleState, error) {
	input, e := w.readMerkleStep(ctx, tree)
	value, prior := input.value, input.prior
	if e != nil {
		return value, e
	}
	checkpoint, expected := input.checkpoint, input.expected
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
		keys, next, done := input.keys, input.next, input.done
		for _, key := range keys {
			if value.LeavesBuilt == ^uint64(0) {
				return value, ErrLimit
			}
			node := DerivedMerkleNode{Index: value.LeavesBuilt, ValueSHA256: key, NodeSHA256: jsonSHA([]string{leafDomain, key})}
			a, e := merkleMutation(merkleNodeSet(tree, 0), merkleNodeKey(node.Index), DerivedEntry{}, node)
			if e != nil {
				return value, e
			}
			changes = append(changes, a)
			a, e = merkleMutation(DerivedSetID("merkle-positions", tree), key, DerivedEntry{}, DerivedMerklePosition{node.Index})
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
			nodes := input.nodes
			if len(nodes) != 1 {
				return value, ErrChanged
			}
			value.Phase = "complete"
			value.RootSHA256 = nodes[0].NodeSHA256
		} else {
			nodes := input.nodes
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
