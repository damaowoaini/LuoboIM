package namespaceinventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Immutable radix index: bounded16-entry leaves, at most64 hash digits per
// lookup/update. No per-resume scan of all keys or unbounded bucket rewrite.
// Values contain raw-page references, never an independently trusted summary.
type progressKV struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}
type progressChild struct {
	Digit string `json:"digit"`
	SHA   string `json:"sha256"`
}
type progressNode struct {
	Kind     string          `json:"kind"`
	Depth    int             `json:"depth"`
	Items    []progressKV    `json:"items,omitempty"`
	Children []progressChild `json:"children,omitempty"`
}
type progressStore struct{ dir string }

func (s progressStore) objectPath(kind, sha string) string {
	return filepath.Join(s.dir, kind+"-"+sha+".json")
}
func (s progressStore) save(kind string, v interface{}) (string, error) {
	b, e := json.Marshal(v)
	if e != nil || len(b) >= MaxPrivateSnapshot {
		return "", ErrLimit
	}
	b = append(b, '\n')
	h := digest(b)
	path := s.objectPath(kind, h)
	if _, e := os.Lstat(path); e == nil {
		old, e := ReadPrivateDocument(path, MaxPrivateSnapshot)
		if e != nil || digest(old) != h {
			return "", ErrPrivate
		}
		return h, nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", ErrPrivate
	}
	actual, e := writePrivateJSON(path, v)
	if e != nil || actual != h {
		return "", ErrPrivate
	}
	return h, nil
}
func (s progressStore) load(kind, h string, limit int, v interface{}) error {
	if !hexText(h, 64) {
		return ErrInvalid
	}
	b, e := ReadPrivateDocument(s.objectPath(kind, h), limit)
	if e != nil {
		return e
	}
	if digest(b) != h {
		return ErrInvalid
	}
	return DecodeStrict(b, v)
}

// Only the progressNode typed decode uses the already reviewed strict scanner.
// Generic DecodeStrict, current private path checks, fresh file read, SHA and
// prefix/schema validation remain unchanged. No parsed/index custody is cached.
func decodeProgressIndexNodeStrict(raw []byte, out *progressNode) error {
	if e := uniqueProgressJSON(raw); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrInvalid
	}
	return nil
}
func (s progressStore) node(h, prefix string) (progressNode, error) {
	var n progressNode
	if !hexText(h, 64) {
		return n, ErrInvalid
	}
	raw, e := ReadPrivateDocument(s.objectPath("index", h), 128<<10)
	if e != nil {
		return n, e
	}
	if digest(raw) != h {
		return n, ErrInvalid
	}
	if e := decodeProgressIndexNodeStrict(raw, &n); e != nil {
		return n, e
	}
	return checkedProgressNode(n, prefix)
}
func checkedProgressNode(n progressNode, prefix string) (progressNode, error) {
	if n.Kind != "raw-bound-index-v1" || n.Depth != len(prefix) || n.Depth > 64 || len(n.Items) > 16 || len(n.Children) > 16 || len(n.Items) != 0 && len(n.Children) != 0 {
		return n, ErrInvalid
	}
	last := ""
	for _, item := range n.Items {
		if !hexText(item.Key, 64) || item.Key <= last || !strings.HasPrefix(item.Key, prefix) || len(item.Value) < 1 || len(item.Value) > 4096 {
			return n, ErrInvalid
		}
		last = item.Key
	}
	last = ""
	for _, child := range n.Children {
		if n.Depth == 64 || len(child.Digit) != 1 || !strings.Contains("0123456789abcdef", child.Digit) || child.Digit <= last || !hexText(child.SHA, 64) {
			return n, ErrInvalid
		}
		last = child.Digit
	}
	return n, nil
}
func (s progressStore) get(ctx context.Context, root, key string, out interface{}) (bool, error) {
	if !hexText(key, 64) {
		return false, ErrInvalid
	}
	prefix := ""
	for root != "" {
		if e := ctx.Err(); e != nil {
			return false, e
		}
		n, e := s.nodeAt(ctx, root, prefix)
		if e != nil {
			return false, e
		}
		if len(n.Children) == 0 {
			for _, item := range n.Items {
				if item.Key == key {
					return true, DecodeStrict(item.Value, out)
				}
			}
			return false, nil
		}
		root = ""
		digit := key[len(prefix) : len(prefix)+1]
		for _, child := range n.Children {
			if child.Digit == digit {
				root = child.SHA
				break
			}
		}
		prefix += digit
	}
	return false, nil
}
func (s progressStore) build(ctx context.Context, items []progressKV, prefix string) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	n := progressNode{Kind: "raw-bound-index-v1", Depth: len(prefix)}
	if len(items) <= 16 {
		n.Items = items
		return s.saveIndex(ctx, n)
	}
	if len(prefix) >= 64 {
		return "", ErrInvalid
	}
	for start := 0; start < len(items); {
		digit := items[start].Key[len(prefix) : len(prefix)+1]
		end := start + 1
		for end < len(items) && strings.HasPrefix(items[end].Key, prefix+digit) {
			end++
		}
		h, e := s.build(ctx, items[start:end], prefix+digit)
		if e != nil {
			return "", e
		}
		n.Children = append(n.Children, progressChild{Digit: digit, SHA: h})
		start = end
	}
	return s.saveIndex(ctx, n)
}
func (s progressStore) put(ctx context.Context, root, key string, value interface{}) (string, error) {
	if !hexText(key, 64) {
		return "", ErrInvalid
	}
	raw, e := json.Marshal(value)
	if e != nil || len(raw) > 4096 {
		return "", ErrLimit
	}
	var visit func(string, string) (string, error)
	visit = func(h, prefix string) (string, error) {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		n := progressNode{Kind: "raw-bound-index-v1", Depth: len(prefix)}
		if h != "" {
			var e error
			n, e = s.nodeAt(ctx, h, prefix)
			if e != nil {
				return "", e
			}
		}
		if len(n.Children) == 0 {
			found := false
			for at := range n.Items {
				if n.Items[at].Key == key {
					n.Items[at].Value = raw
					found = true
					break
				}
			}
			if !found {
				n.Items = append(n.Items, progressKV{Key: key, Value: raw})
			}
			sort.Slice(n.Items, func(i, j int) bool { return n.Items[i].Key < n.Items[j].Key })
			return s.build(ctx, n.Items, prefix)
		}
		digit := key[len(prefix) : len(prefix)+1]
		old := ""
		at := -1
		for k, child := range n.Children {
			if child.Digit == digit {
				old, at = child.SHA, k
				break
			}
		}
		next, e := visit(old, prefix+digit)
		if e != nil {
			return "", e
		}
		if at >= 0 {
			n.Children[at].SHA = next
		} else {
			n.Children = append(n.Children, progressChild{Digit: digit, SHA: next})
			sort.Slice(n.Children, func(i, j int) bool { return n.Children[i].Digit < n.Children[j].Digit })
		}
		return s.saveIndex(ctx, n)
	}
	return visit(root, "")
}

// Seek strictly after the previous hash key. Prefix bounds skip completed
// subtrees; a new invocation traverses bounded-depth paths, not old entries.
func (s progressStore) next(ctx context.Context, root, after string) (progressKV, bool, error) {
	if after != "" && !hexText(after, 64) {
		return progressKV{}, false, ErrInvalid
	}
	var visit func(string, string) (progressKV, bool, error)
	visit = func(h, prefix string) (progressKV, bool, error) {
		if e := ctx.Err(); e != nil {
			return progressKV{}, false, e
		}
		if h == "" {
			return progressKV{}, false, nil
		}
		if after != "" && prefix+strings.Repeat("f", 64-len(prefix)) <= after {
			return progressKV{}, false, nil
		}
		n, e := s.nodeAt(ctx, h, prefix)
		if e != nil {
			return progressKV{}, false, e
		}
		for _, item := range n.Items {
			if item.Key > after {
				return item, true, nil
			}
		}
		for _, child := range n.Children {
			v, ok, e := visit(child.SHA, prefix+child.Digit)
			if e != nil || ok {
				return v, ok, e
			}
		}
		return progressKV{}, false, nil
	}
	return visit(root, "")
}
