package namespaceinventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// ImportWorkspace stores bounded derived source work beside (never inside) the
// sealed raw snapshot. The recorded phase is NOT inventory trust, Ready, an
// owner bound or a writer permit. Final semantics still re-resolve RawRoot refs.
// Existing immutable radix objects supply O(log N) disk lookup; resume does not
// reread all history or load a complete group's owners into memory.
type ImportWorkspace struct {
	dir            string
	snapshot       *ImportSnapshot
	live           PageSource
	header, policy string
}
type DerivedCheckpoint struct {
	Schema         int    `json:"schema"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
	HeaderSHA256   string `json:"header_sha256"`
	PolicySHA256   string `json:"policy_sha256"`
	Sequence       uint64 `json:"sequence"`
	PreviousSHA256 string `json:"previous_sha256"`
	Phase          string `json:"phase"`
	Cursor         string `json:"cursor"`
	Subcursor      string `json:"subcursor"`
	SetsRoot       string `json:"sets_root"`
}
type derivedHead struct {
	Sequence uint64 `json:"sequence"`
	SHA256   string `json:"sha256"`
}
type derivedSet struct {
	Root  string `json:"root"`
	Count uint64 `json:"count"`
}
type derivedValue struct {
	SHA256 string `json:"sha256"`
}
type DerivedMutation struct {
	SetID, Key, ExpectedSHA256 string
	Value                      json.RawMessage
}
type DerivedEntry struct {
	Key, SHA256 string
	Value       json.RawMessage
}

func DerivedSetID(kind, identity string) string {
	return jsonSHA([]string{"namespace-materializer-set-v1", kind, identity})
}
func derivedPhase(s string) int {
	switch s {
	case "raw":
		return 0
	case "reduce":
		return 1
	case "roots":
		return 2
	case "pages":
		return 3
	case "complete":
		return 4
	}
	return -1
}
func validDerived(st DerivedCheckpoint) bool {
	return st.Schema == 1 && hexText(st.SnapshotSHA256, 64) && hexText(st.HeaderSHA256, 64) && hexText(st.PolicySHA256, 64) && st.Sequence > 0 && derivedPhase(st.Phase) >= 0 && (st.PreviousSHA256 == "" || hexText(st.PreviousSHA256, 64)) && (st.Cursor == "" || hexText(st.Cursor, 64)) && len(st.Subcursor) <= 512 && !strings.ContainsAny(st.Subcursor, "\x00\r\n") && (st.SetsRoot == "" || hexText(st.SetsRoot, 64))
}
func (w *ImportWorkspace) check(ctx context.Context) error {
	if w == nil || w.snapshot == nil || w.live == nil || ctx == nil {
		return ErrInvalid
	}
	return w.snapshot.check(ctx, w.live)
}
func (w *ImportWorkspace) load() (DerivedCheckpoint, string, error) {
	var h derivedHead
	body, e := ReadPrivateDocument(filepath.Join(w.dir, "DERIVED-HEAD"), 64<<10)
	if e != nil || DecodeStrict(body, &h) != nil || h.Sequence == 0 || !hexText(h.SHA256, 64) {
		return DerivedCheckpoint{}, "", ErrChanged
	}
	raw, e := ReadPrivateDocument(filepath.Join(w.dir, fmt.Sprintf("derived-state-%020d.json", h.Sequence)), 64<<10)
	var st DerivedCheckpoint
	if e != nil || digest(raw) != h.SHA256 || DecodeStrict(raw, &st) != nil || !validDerived(st) || st.Sequence != h.Sequence || st.SnapshotSHA256 != w.snapshot.Fingerprint() || st.HeaderSHA256 != w.header || st.PolicySHA256 != w.policy {
		return st, "", ErrChanged
	}
	return st, h.SHA256, nil
}
func OpenImportWorkspace(ctx context.Context, snapshot *ImportSnapshot, live PageSource, dir, headerSHA, policySHA string, resume bool) (out *ImportWorkspace, resultErr error) {
	if snapshot == nil || live == nil || !hexText(headerSHA, 64) || !hexText(policySHA, 64) || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, ErrInvalid
	}
	if e := importWorkspaceBoundary(snapshot.ImportSnapshotPath(), dir); e != nil {
		return nil, e
	}
	if !resume {
		// Match the raw journal custody contract: create the fresh directory
		// through its admitted private parent, durably retain its name, then
		// exclusively create the 0600 lock BEFORE trying to acquire it. Resume
		// never repairs a missing lock or adopts an existing directory.
		parent, name, e := privateParent(dir)
		if e != nil {
			return nil, e
		}
		mkdirErr := unix.Mkdirat(parent, name, 0700)
		syncErr, closeErr := unix.Fsync(parent), unix.Close(parent)
		if mkdirErr != nil || syncErr != nil || closeErr != nil {
			return nil, ErrPrivate
		}
		if _, e = writePrivateJSON(filepath.Join(dir, "lock"), struct {
			Version int `json:"version"`
		}{1}); e != nil {
			return nil, e
		}
	}
	lock, e := lockJournal(dir)
	if e != nil {
		return nil, e
	}
	defer func() {
		if lock.Close() != nil {
			out = nil
			resultErr = ErrPrivate
		}
	}()
	w := &ImportWorkspace{dir, snapshot, live, headerSHA, policySHA}
	if e = w.check(ctx); e != nil {
		return nil, e
	}
	if resume {
		if _, _, e = w.load(); e != nil {
			return nil, e
		}
		return w, nil
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 1 {
		return nil, ErrPrivate
	}
	st := DerivedCheckpoint{Schema: 1, SnapshotSHA256: snapshot.Fingerprint(), HeaderSHA256: headerSHA, PolicySHA256: policySHA, Sequence: 1, Phase: "raw"}
	if e = w.save(derivedHead{}, st); e != nil {
		return nil, e
	}
	return w, nil
}
func (w *ImportWorkspace) Checkpoint(ctx context.Context) (st DerivedCheckpoint, sha string, resultErr error) {
	if e := w.check(ctx); e != nil {
		return st, "", e
	}
	lock, e := lockJournal(w.dir)
	if e != nil {
		return st, "", e
	}
	defer func() {
		if lock.Close() != nil {
			st = DerivedCheckpoint{}
			sha = ""
			resultErr = ErrPrivate
		}
	}()
	return w.load()
}
func (w *ImportWorkspace) save(old derivedHead, next DerivedCheckpoint) error {
	if next.Sequence != old.Sequence+1 || next.PreviousSHA256 != old.SHA256 || !validDerived(next) {
		return ErrInvalid
	}
	path := filepath.Join(w.dir, fmt.Sprintf("derived-state-%020d.json", next.Sequence))
	want, e := json.Marshal(next)
	if e != nil {
		return e
	}
	want = append(want, '\n')
	if prior, e := ReadPrivateDocument(path, 64<<10); e == nil {
		if !bytes.Equal(prior, want) {
			return ErrChanged
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		// PrivateDocument intentionally normalizes some missing-file errors. Only
		// an actual absent path permits a first exclusive create, never bad custody.
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			return e
		}
		if _, e = writePrivateJSON(path, next); e != nil {
			return e
		}
	} else if _, e = writePrivateJSON(path, next); e != nil {
		return e
	}
	head := derivedHead{next.Sequence, digest(want)}
	temp := filepath.Join(w.dir, fmt.Sprintf("DERIVED-HEAD-%020d.tmp", next.Sequence))
	encoded, e := json.Marshal(head)
	if e != nil {
		return e
	}
	encoded = append(encoded, '\n')
	if prior, e := ReadPrivateDocument(temp, 64<<10); e == nil {
		if !bytes.Equal(prior, encoded) {
			return ErrChanged
		}
	} else {
		if _, statErr := os.Lstat(temp); !errors.Is(statErr, os.ErrNotExist) {
			return ErrChanged
		}
		if _, e = writePrivateJSON(temp, head); e != nil {
			return e
		}
	}
	if e = os.Rename(temp, filepath.Join(w.dir, "DERIVED-HEAD")); e != nil {
		return e
	}
	dir, e := os.Open(w.dir)
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}

// Commit changes at most64 small index entries, all rooted in one immutable
// checkpoint. Value bodies are separate <=64KiB private JSON objects. The
// journal head advances only after every immutable object/state was fsynced.
// An interrupted state can complete only by replaying the exact same next body.
func (w *ImportWorkspace) Commit(ctx context.Context, expectedSHA, phase, cursor, subcursor string, changes []DerivedMutation) (st DerivedCheckpoint, sum string, resultErr error) {
	if e := w.check(ctx); e != nil {
		return st, "", e
	}
	if !hexText(expectedSHA, 64) || len(changes) > 64 {
		return st, "", ErrLimit
	}
	lock, e := lockJournal(w.dir)
	if e != nil {
		return st, "", e
	}
	defer func() {
		if lock.Close() != nil {
			st = DerivedCheckpoint{}
			sum = ""
			resultErr = ErrPrivate
		}
	}()
	old, oldSHA, e := w.load()
	if e != nil || oldSHA != expectedSHA || old.Sequence == ^uint64(0) || old.Phase == "complete" || derivedPhase(phase) < derivedPhase(old.Phase) || derivedPhase(phase) > derivedPhase(old.Phase)+1 {
		return st, "", ErrChanged
	}
	next := old
	next.Sequence++
	next.PreviousSHA256 = oldSHA
	next.Phase = phase
	next.Cursor = cursor
	next.Subcursor = subcursor
	if !validDerived(next) {
		return st, "", ErrInvalid
	}
	store := progressStore{w.dir}
	seen := map[string]bool{}
	for _, change := range changes {
		if e = ctx.Err(); e != nil {
			return st, "", e
		}
		k := change.SetID + change.Key
		if seen[k] || !hexText(change.SetID, 64) || !hexText(change.Key, 64) || change.ExpectedSHA256 != "" && !hexText(change.ExpectedSHA256, 64) || len(change.Value) == 0 || len(change.Value) > 64<<10 || !json.Valid(change.Value) {
			return st, "", ErrInvalid
		}
		seen[k] = true
		var set derivedSet
		present, e := store.get(ctx, next.SetsRoot, change.SetID, &set)
		if e != nil || present && (set.Count == 0 || !hexText(set.Root, 64)) {
			return st, "", ErrChanged
		}
		var prior derivedValue
		exists, e := store.get(ctx, set.Root, change.Key, &prior)
		if e != nil {
			return st, "", e
		}
		if exists && prior.SHA256 != change.ExpectedSHA256 || !exists && change.ExpectedSHA256 != "" {
			return st, "", ErrChanged
		}
		valueSHA, e := store.save("derived-value", change.Value)
		if e != nil {
			return st, "", e
		}
		root, e := store.put(ctx, set.Root, change.Key, derivedValue{valueSHA})
		if e != nil {
			return st, "", e
		}
		set.Root = root
		if !exists {
			if set.Count == ^uint64(0) {
				return st, "", ErrLimit
			}
			set.Count++
		}
		next.SetsRoot, e = store.put(ctx, next.SetsRoot, change.SetID, set)
		if e != nil {
			return st, "", e
		}
	}
	if e = w.check(ctx); e != nil {
		return st, "", e
	}
	if e = w.save(derivedHead{old.Sequence, oldSHA}, next); e != nil {
		return st, "", e
	}
	return w.load()
}

// ReadEntries verifies each value's actual private bytes on every invocation.
// No process-global decode cache or whole-set scan is used. Empty is a factual
// absent derived set, never a proof that an original namespace/head is zero.
func (w *ImportWorkspace) ReadEntries(ctx context.Context, setID, after string, limit int) (out []DerivedEntry, next string, done bool, total uint64, resultErr error) {
	if e := w.check(ctx); e != nil {
		return nil, "", false, 0, e
	}
	if !hexText(setID, 64) || after != "" && !hexText(after, 64) || limit < 1 || limit > 64 {
		return nil, "", false, 0, ErrLimit
	}
	lock, e := lockJournal(w.dir)
	if e != nil {
		return nil, "", false, 0, e
	}
	defer func() {
		if lock.Close() != nil {
			out = nil
			next = ""
			done = false
			total = 0
			resultErr = ErrPrivate
		}
	}()
	state, _, e := w.load()
	if e != nil {
		return nil, "", false, 0, e
	}
	store := progressStore{w.dir}
	var set derivedSet
	present, e := store.get(ctx, state.SetsRoot, setID, &set)
	if e != nil {
		return nil, "", false, 0, e
	}
	if !present {
		return []DerivedEntry{}, after, true, 0, nil
	}
	if set.Count == 0 || !hexText(set.Root, 64) {
		return nil, "", false, 0, ErrChanged
	}
	total = set.Count
	next = after
	bytesRead := 0
	for len(out) < limit {
		row, ok, e := store.next(ctx, set.Root, next)
		if e != nil {
			return nil, "", false, 0, e
		}
		if !ok {
			done = true
			break
		}
		var value derivedValue
		if DecodeStrict(row.Value, &value) != nil || !hexText(value.SHA256, 64) {
			return nil, "", false, 0, ErrChanged
		}
		raw, e := ReadPrivateDocument(store.objectPath("derived-value", value.SHA256), (64<<10)+1)
		if e != nil || digest(raw) != value.SHA256 || !json.Valid(raw) {
			return nil, "", false, 0, ErrChanged
		}
		if bytesRead+len(raw) > 256<<10 {
			break
		}
		bytesRead += len(raw)
		out = append(out, DerivedEntry{row.Key, value.SHA256, append(json.RawMessage(nil), raw...)})
		next = row.Key
	}
	if e = w.check(ctx); e != nil {
		return nil, "", false, 0, e
	}
	return
}

func (w *ImportWorkspace) ReadEntry(ctx context.Context, setID, key string) (entry DerivedEntry, found bool, resultErr error) {
	if e := w.check(ctx); e != nil {
		return entry, false, e
	}
	if !hexText(setID, 64) || !hexText(key, 64) {
		return entry, false, ErrInvalid
	}
	lock, e := lockJournal(w.dir)
	if e != nil {
		return entry, false, e
	}
	defer func() {
		if lock.Close() != nil {
			entry = DerivedEntry{}
			found = false
			resultErr = ErrPrivate
		}
	}()
	state, _, e := w.load()
	if e != nil {
		return entry, false, e
	}
	store := progressStore{w.dir}
	var set derivedSet
	ok, e := store.get(ctx, state.SetsRoot, setID, &set)
	if e != nil || !ok {
		return entry, false, e
	}
	var ref derivedValue
	ok, e = store.get(ctx, set.Root, key, &ref)
	if e != nil || !ok {
		return entry, false, e
	}
	body, e := ReadPrivateDocument(store.objectPath("derived-value", ref.SHA256), (64<<10)+1)
	if e != nil || digest(body) != ref.SHA256 || !json.Valid(body) {
		return entry, false, ErrChanged
	}
	return DerivedEntry{key, ref.SHA256, append(json.RawMessage(nil), body...)}, true, nil
}

func importPathWithin(root, path string) bool {
	relative, e := filepath.Rel(root, path)
	return e == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

// Check both the supplied path and its real existing-parent/target identity
// BEFORE Mkdir/lock/load. No fresh or resumed derivative path may name the
// original snapshot or any descendant, including a symlink alias or a lexical
// path through the original tree that escapes via a pre-existing symlink.
func importWorkspaceBoundary(snapshot, dir string) error {
	if !filepath.IsAbs(snapshot) || filepath.Clean(snapshot) != snapshot || importPathWithin(snapshot, dir) {
		return ErrInvalid
	}
	actualSnapshot, e := filepath.EvalSymlinks(snapshot)
	if e != nil {
		return ErrPrivate
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(dir))
	if e != nil {
		return ErrPrivate
	}
	actualTarget := filepath.Join(parent, filepath.Base(dir))
	if _, e := os.Lstat(dir); e == nil {
		actualTarget, e = filepath.EvalSymlinks(dir)
		if e != nil {
			return ErrPrivate
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if importPathWithin(actualSnapshot, actualTarget) {
		return ErrInvalid
	}
	return nil
}
