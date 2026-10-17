package namespaceinventory

import (
	"context"
	"encoding/json"
	"sort"
)

// These are checkpoint/memory quanta, never total-history limits. The original
// immutable writer, per-file fsync, directory fsync and head replacement remain
// the durability boundary. No cache survives the locked Resume invocation.
const progressCheckpointSteps = 32
const progressIndexBatchBytes = 2 << 20
const progressIndexBatchNodes = 256

type progressIndexBatchKey struct{}
type progressIndexBatch struct {
	dir      string
	pending  map[string][]byte
	bytes    int
	maxBytes int
	maxNodes int
	closed   bool
}

func newProgressIndexContext(ctx context.Context, dir string) (context.Context, *progressIndexBatch) {
	b := &progressIndexBatch{dir: dir, pending: map[string][]byte{}, maxBytes: progressIndexBatchBytes, maxNodes: progressIndexBatchNodes}
	return context.WithValue(ctx, progressIndexBatchKey{}, b), b
}
func (b *progressIndexBatch) close() {
	b.pending, b.bytes, b.closed = nil, 0, true
}
func (s progressStore) indexBatch(ctx context.Context) *progressIndexBatch {
	b, _ := ctx.Value(progressIndexBatchKey{}).(*progressIndexBatch)
	if b == nil || b.closed || b.dir != s.dir {
		return nil
	}
	return b
}
func (s progressStore) nodeAt(ctx context.Context, h, prefix string) (progressNode, error) {
	if b := s.indexBatch(ctx); b != nil {
		if raw, ok := b.pending[h]; ok {
			var n progressNode
			if digest(raw) != h || decodeProgressIndexNodeStrict(raw, &n) != nil {
				return n, ErrInvalid
			}
			return checkedProgressNode(n, prefix)
		}
	}
	// Existing on-disk nodes are still freshly private-read/hash/strict checked.
	return s.node(h, prefix)
}
func (s progressStore) saveIndex(ctx context.Context, n progressNode) (string, error) {
	b := s.indexBatch(ctx)
	if b == nil {
		return s.save("index", n)
	}
	raw, e := json.Marshal(n)
	if e != nil || len(raw) >= 128<<10 {
		return "", ErrLimit
	}
	raw = append(raw, '\n')
	h := digest(raw)
	if old, ok := b.pending[h]; ok {
		if string(old) != string(raw) {
			return "", ErrInvalid
		}
		return h, nil
	}
	if len(raw) > b.maxBytes {
		// A valid single node must not become a total-history/memory-limit error.
		return s.save("index", n)
	}
	if b.bytes > b.maxBytes-len(raw) || len(b.pending) >= b.maxNodes {
		// Spill immutable nodes, without advancing the durable head. A crash
		// preserves these as unused orphans; it never enrolls their contents.
		if e := b.persist(ctx, nil); e != nil {
			return "", e
		}
	}
	b.pending[h] = raw
	b.bytes += len(raw)
	return h, nil
}
func (b *progressIndexBatch) checkpointDue() bool {
	return b.bytes >= b.maxBytes/2 || len(b.pending) >= b.maxNodes/2
}

// Persist only new nodes reachable from this checkpoint's four roots. An
// unchanged disk subtree is already immutable; it cannot refer to newly staged
// contents. CID leaves additionally reference their bounded origin indexes.
// This walks at most the invocation's staged nodes, not all historical rows.
func (b *progressIndexBatch) flush(ctx context.Context, p progressState) error {
	needed := map[string]bool{}
	visited := map[struct {
		h   string
		cid bool
	}]bool{}
	var visit func(string, bool) error
	visit = func(h string, cid bool) error {
		raw, ok := b.pending[h]
		key := struct {
			h   string
			cid bool
		}{h, cid}
		if !ok || visited[key] {
			return nil
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		var n progressNode
		if digest(raw) != h || decodeProgressIndexNodeStrict(raw, &n) != nil {
			return ErrInvalid
		}
		needed[h] = true
		visited[key] = true
		for _, child := range n.Children {
			if e := visit(child.SHA, cid); e != nil {
				return e
			}
		}
		if cid {
			for _, item := range n.Items {
				var c progressCID
				if DecodeStrict(item.Value, &c) != nil || item.Key != c.CID || !validProgressCID(c) {
					return ErrInvalid
				}
				if e := visit(c.Origins, false); e != nil {
					return e
				}
			}
		}
		return nil
	}
	for _, root := range []string{p.RawRoot, p.RowsRoot, p.SeenRoot} {
		if e := visit(root, false); e != nil {
			return e
		}
	}
	if e := visit(p.CIDRoot, true); e != nil {
		return e
	}
	return b.persist(ctx, needed)
}

// nil selects a bounded pressure spill; a non-nil set selects a checkpoint's
// closure. Every selected file uses the original O_EXCL/NOFOLLOW/private-mode
// writer and its real file+directory fsync. No weaker alternate writer exists.
func (b *progressIndexBatch) persist(ctx context.Context, needed map[string]bool) error {
	keys := make([]string, 0, len(b.pending))
	for h := range b.pending {
		if needed == nil || needed[h] {
			keys = append(keys, h)
		}
	}
	sort.Strings(keys)
	for _, h := range keys {
		if e := ctx.Err(); e != nil {
			return e
		}
		var n progressNode
		raw := b.pending[h]
		if digest(raw) != h || decodeProgressIndexNodeStrict(raw, &n) != nil {
			return ErrInvalid
		}
		actual, e := (progressStore{b.dir}).save("index", n)
		if e != nil {
			return e
		}
		if actual != h {
			return ErrInvalid
		}
	}
	// Unreachable intermediate COW nodes were never published or persisted.
	b.pending, b.bytes = map[string][]byte{}, 0
	return nil
}

// Old V2 states omit workSequence and retain their original one-state/one-work
// meaning. New checkpoints keep the same physical Previous/Sequence chain but
// separately account for at most32 completed work units. Old readers reject
// this new JSON field instead of silently interpreting a new checkpoint.
func progressWorkSequence(p progressState) int64 {
	if p.WorkSequence == 0 {
		return p.Sequence
	}
	return p.WorkSequence
}
