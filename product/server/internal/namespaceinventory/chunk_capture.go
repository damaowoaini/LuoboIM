package namespaceinventory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ChunkSummary has no account, CID, endpoint, raw BSON or Redis key fields.
// ObservedComplete means two bounded observations matched, NEVER complete
// historical coverage, an atomic cross-store cut, a drain or a writer grant.
type ChunkSummary struct {
	State               string           `json:"state"`
	CheckpointSHA       string           `json:"checkpointSHA256"`
	Checkpoints         int64            `json:"checkpoints"`
	ObservedComplete    bool             `json:"twoPassObservationComplete"`
	MongoRows           int64            `json:"mongoRows"`
	RedisKeys           int64            `json:"redisKeys"`
	ErasedSlots         int64            `json:"erasedSlots"`
	CandidateCollisions int64            `json:"candidateCollisions"`
	Reasons             map[string]int64 `json:"reasonCounts"`
}

type indexRecord struct {
	Source      string      `json:"source"`
	Mongo       bool        `json:"mongo"`
	ContentSHA  string      `json:"contentSHA256"`
	TTL         int64       `json:"pttl"`
	Observation Observation `json:"observation"`
}
type originMarker struct {
	CID  string `json:"cid"`
	Kind string `json:"kind"`
	SHA  string `json:"sha256"`
}
type diskIndex struct {
	root     string
	identity os.FileInfo
}

func freshIndex(dir string) (*diskIndex, error) {
	// Only this newly created derived scratch directory is ever removed. Raw
	// chunks/checkpoints, unknown orphan pages and previous scratch survive.
	p, e := os.MkdirTemp(dir, ".derived-")
	if e != nil {
		return nil, ErrPrivate
	}
	for _, name := range []string{"rows", "seen", "cids"} {
		if os.Mkdir(filepath.Join(p, name), 0700) != nil {
			return nil, ErrPrivate
		}
	}
	identity, e := os.Lstat(p)
	if e != nil {
		return nil, ErrPrivate
	}
	return &diskIndex{root: p, identity: identity}, nil
}
func (i *diskIndex) close() error {
	if i == nil || !strings.HasPrefix(filepath.Base(i.root), ".derived-") {
		return ErrPrivate
	}
	current, e := os.Lstat(i.root)
	if e != nil || i.identity == nil || !current.IsDir() || current.Mode().Perm() != 0700 || !os.SameFile(current, i.identity) {
		return ErrPrivate
	}
	if os.RemoveAll(i.root) != nil {
		return ErrPrivate
	}
	return nil
}
func samePrivateJSON(path string, v interface{}) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return ErrInvalid
	}
	raw = append(raw, '\n')
	old, e := ReadPrivateDocument(path, MaxPrivateSnapshot)
	if e == nil {
		if digest(old) != digest(raw) {
			return ErrPrivate
		}
		return nil
	}
	if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		return ErrPrivate
	}
	_, e = writePrivateJSON(path, v)
	return e
}
func indexRows(page Page) []indexRecord {
	result := make([]indexRecord, 0, len(page.Mongo)+len(page.Redis))
	for _, row := range page.Mongo {
		result = append(result, indexRecord{Source: "mongo:" + row.Collection + ":" + row.KeySHA, Mongo: true, ContentSHA: digest(row.Raw), Observation: classify(row)})
	}
	for _, row := range page.Redis {
		// PTTL may decline between pages/passes; payload/type/head must match.
		content := struct {
			Key, Type string
			Dump      []byte
			Head      string
		}{row.Key, row.Type, row.Dump, row.RawHead}
		o := observations(nil, []RedisRow{row})[0]
		// This reason depends on ALL pages, not just this Redis row.
		filtered := o.Reasons[:0]
		for _, reason := range o.Reasons {
			if reason != "no-observed-raw-head-zero-not-inferred" {
				filtered = append(filtered, reason)
			}
		}
		o.Reasons = filtered
		result = append(result, indexRecord{Source: "redis:" + digest([]byte(row.Key)), ContentSHA: jsonSHA(content), TTL: row.PTTL, Observation: o})
	}
	return result
}
func (i *diskIndex) add(page Page, pass int) error {
	for _, row := range indexRows(page) {
		key := digest([]byte(row.Source))
		path := filepath.Join(i.root, "rows", key+".json")
		var old indexRecord
		e := loadPrivateJSON(path, MaxPrivateSnapshot, &old)
		if e == nil {
			if old.Source != row.Source || old.Mongo != row.Mongo || old.ContentSHA != row.ContentSHA || jsonSHA(old.Observation) != jsonSHA(row.Observation) || !row.Mongo && ((old.TTL < 0) != (row.TTL < 0) || row.TTL > old.TTL && row.TTL-old.TTL > 100) {
				return ErrChanged
			}
		} else {
			if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
				return ErrPrivate
			}
			if pass != 1 {
				return ErrChanged
			}
			if _, e := writePrivateJSON(path, row); e != nil {
				return e
			}
		}
		if pass == 2 {
			if e := samePrivateJSON(filepath.Join(i.root, "seen", key+".json"), row.Source); e != nil {
				return e
			}
			continue
		}
		o := row.Observation
		if o.CID == "" {
			continue
		}
		dir := filepath.Join(i.root, "cids", digest([]byte(o.CID)))
		if e := os.Mkdir(dir, 0700); e != nil && !errors.Is(e, os.ErrExist) {
			return ErrPrivate
		}
		if o.RawHead != nil {
			if e := samePrivateJSON(filepath.Join(dir, "head"), o.CID); e != nil {
				return e
			}
		}
		for _, origin := range o.Origins {
			marker := originMarker{CID: o.CID, Kind: origin.Kind, SHA: origin.SHA}
			if e := samePrivateJSON(filepath.Join(dir, "origin-"+digest([]byte(origin.Kind+":"+origin.SHA))), marker); e != nil {
				return e
			}
		}
	}
	return nil
}

// No ReadDir-all, all-history slices or all-CID in-memory maps: enumeration
// and dedup/collision state are bounded on disk and rebuilt from committed raw
// chunks. Work grows with history; no aggregate 10000-row/64MiB refusal remains.
func eachName(ctx context.Context, dir string, visit func(string) error) (resultErr error) {
	f, e := os.Open(dir)
	if e != nil {
		return ErrPrivate
	}
	defer func() {
		if f.Close() != nil {
			resultErr = ErrPrivate
		}
	}()
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		names, e := f.Readdirnames(128)
		for _, name := range names {
			if e := visit(name); e != nil {
				return e
			}
		}
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return ErrPrivate
		}
	}
}
func (i *diskIndex) summary(ctx context.Context, s chunkSession, p chunkPosition) (ChunkSummary, error) {
	out := ChunkSummary{State: State, CheckpointSHA: p.SHA, Checkpoints: p.Sequence, ObservedComplete: p.Complete, Reasons: map[string]int64{}}
	for _, r := range fixedReasons(s.Identity) {
		out.Reasons[r]++
	}
	out.Reasons["checkpoint-is-not-drain-lifecycle-or-atomic-cut-attestation"]++
	if !p.Complete {
		out.Reasons["second-observation-pass-incomplete"]++
	}
	var rowCount, seenCount int64
	e := eachName(ctx, filepath.Join(i.root, "rows"), func(name string) error {
		if len(name) != 69 || !hexText(strings.TrimSuffix(name, ".json"), 64) {
			return ErrPrivate
		}
		var row indexRecord
		if e := loadPrivateJSON(filepath.Join(i.root, "rows", name), MaxPrivateSnapshot, &row); e != nil {
			return e
		}
		if name != digest([]byte(row.Source))+".json" {
			return ErrPrivate
		}
		rowCount++
		if row.Mongo {
			out.MongoRows++
		} else {
			out.RedisKeys++
		}
		out.ErasedSlots += int64(row.Observation.Erased)
		for _, reason := range row.Observation.Reasons {
			out.Reasons[reason]++
		}
		cid := row.Observation.CID
		if cid != "" {
			var stored string
			head := filepath.Join(i.root, "cids", digest([]byte(cid)), "head")
			if _, e := os.Lstat(head); errors.Is(e, os.ErrNotExist) {
				out.Reasons["no-observed-raw-head-zero-not-inferred"]++
			} else if e != nil || loadPrivateJSON(head, MaxPrivateSnapshot, &stored) != nil || stored != cid {
				return ErrPrivate
			}
		}
		return nil
	})
	if e != nil {
		return ChunkSummary{}, e
	}
	e = eachName(ctx, filepath.Join(i.root, "seen"), func(name string) error {
		var source string
		if loadPrivateJSON(filepath.Join(i.root, "seen", name), MaxPrivateSnapshot, &source) != nil || name != digest([]byte(source))+".json" {
			return ErrPrivate
		}
		if _, e := os.Lstat(filepath.Join(i.root, "rows", name)); e != nil {
			return ErrPrivate
		}
		seenCount++
		return nil
	})
	if e != nil {
		return ChunkSummary{}, e
	}
	if p.Complete && rowCount != seenCount {
		return ChunkSummary{}, ErrChanged
	}
	e = eachName(ctx, filepath.Join(i.root, "cids"), func(name string) error {
		if !hexText(name, 64) {
			return ErrPrivate
		}
		origins := 0
		return eachName(ctx, filepath.Join(i.root, "cids", name), func(marker string) error {
			if marker == "head" {
				return nil
			}
			var v originMarker
			if loadPrivateJSON(filepath.Join(i.root, "cids", name, marker), MaxPrivateSnapshot, &v) != nil || digest([]byte(v.CID)) != name || marker != "origin-"+digest([]byte(v.Kind+":"+v.SHA)) {
				return ErrPrivate
			}
			origins++
			if origins == 2 {
				out.CandidateCollisions++
				out.Reasons["ambiguous-current-typed-origin-candidates"]++
			}
			return nil
		})
	})
	if e != nil {
		return ChunkSummary{}, e
	}
	return out, nil
}

func defaultPageLimits() Limits {
	l := DefaultLimits()
	l.Rows = 256
	l.Bytes = 16 << 20
	l.Timeout = 20 * time.Second
	return l
}

func BeginChunkCapture(ctx context.Context, source PageSource, binding Binding, dir string, limits Limits, pages int) (ChunkSummary, error) {
	if ctx == nil || source == nil || !binding.valid() || !pageLimits(limits) || pages < 1 || pages > 128 {
		return ChunkSummary{}, ErrInvalid
	}
	identity, e := source.Identity(ctx)
	if e != nil {
		return ChunkSummary{}, e
	}
	if !identity.valid() || identity.Binding != binding {
		return ChunkSummary{}, ErrIdentity
	}
	id, e := uuid.NewRandom()
	if e != nil {
		return ChunkSummary{}, ErrSource
	}
	s := chunkSession{Version: 1, ID: id.String(), Created: time.Now().UTC(), State: State, Identity: identity, Limits: limits}
	s.SHA = sessionSHA(s)
	if e := createJournal(dir, s); e != nil {
		return ChunkSummary{}, e
	}
	return ResumeChunkCapture(ctx, source, binding, dir, pages)
}

// Resume never refreshes the original binding/identity or discards old chunks.
// It rebuilds disk-only indexes from immutable committed chunks, then appends
// at most pages source pages. Redis duplicate SCAN keys are idempotent. A new
// or changed row on pass2, an absent captured row at completion, expired key,
// catalog replacement, observed mutation or source restart rejects progress.
// Unobserved interleavings remain explicitly UNTRUSTED, like the old capture;
// checkpointing does not turn two passes into a historical snapshot/drain.
func ResumeChunkCapture(ctx context.Context, source PageSource, binding Binding, dir string, pages int) (out ChunkSummary, resultErr error) {
	if ctx == nil || source == nil || !binding.valid() || pages < 1 || pages > 128 {
		return out, ErrInvalid
	}
	lock, e := lockJournal(dir)
	if e != nil {
		return out, e
	}
	defer func() {
		if lock.Close() != nil {
			out = ChunkSummary{}
			resultErr = ErrPrivate
		}
	}()
	// A converted journal has one versioned progress writer; do not fork its history.
	if !missingPrivate(filepath.Join(dir, "progress-head.json")) {
		return out, ErrPrivate
	}
	s, e := loadSession(dir)
	if e != nil {
		return out, e
	}
	if s.Identity.Binding != binding {
		return out, ErrIdentity
	}
	check := func() error {
		got, e := source.Identity(ctx)
		if e != nil {
			return e
		}
		if got != s.Identity {
			return ErrIdentity
		}
		return ctx.Err()
	}
	if e := check(); e != nil {
		return out, e
	}
	index, e := freshIndex(dir)
	if e != nil {
		return out, e
	}
	defer func() {
		if index.close() != nil {
			out = ChunkSummary{}
			resultErr = ErrPrivate
		}
	}()
	p, e := replayJournal(ctx, dir, s, index.add)
	if e != nil {
		return out, e
	}
	for n := 0; n < pages && !p.Complete; n++ {
		if e := check(); e != nil {
			return out, e
		}
		page, e := source.Page(ctx, p.Cursor, s.Limits)
		if e != nil {
			return out, e
		}
		if !sameCursor(page.Before, p.Cursor) || validatePage(page, s.Limits) != nil {
			return out, ErrInvalid
		}
		if e := check(); e != nil {
			return out, e
		}
		if e := index.add(page, p.Pass); e != nil {
			return out, e
		}
		next := nextPosition(p, page)
		if next.Complete {
			if _, e := index.summary(ctx, s, next); e != nil {
				return out, e
			}
		}
		p, e = appendCheckpoint(dir, s, p, page)
		if e != nil {
			return out, e
		}
	}
	if e := check(); e != nil {
		return out, e
	}
	return index.summary(ctx, s, p)
}

// Offline inspect opens NO source connections. It validates all checkpoint
// and page hashes and rederives typed origins/collisions/erasure from raw BSON
// using a bounded disposable private disk index, not edited summary fields.
func InspectChunks(ctx context.Context, dir string) (out ChunkSummary, resultErr error) {
	if ctx == nil {
		return out, ErrInvalid
	}
	lock, e := lockJournal(dir)
	if e != nil {
		return out, e
	}
	defer func() {
		if lock.Close() != nil {
			out = ChunkSummary{}
			resultErr = ErrPrivate
		}
	}()
	// A converted journal has one versioned progress writer; do not fork its history.
	if !missingPrivate(filepath.Join(dir, "progress-head.json")) {
		return out, ErrPrivate
	}
	s, e := loadSession(dir)
	if e != nil {
		return out, e
	}
	index, e := freshIndex(dir)
	if e != nil {
		return out, e
	}
	defer func() {
		if index.close() != nil {
			out = ChunkSummary{}
			resultErr = ErrPrivate
		}
	}()
	p, e := replayJournal(ctx, dir, s, index.add)
	if e != nil {
		return out, e
	}
	return index.summary(ctx, s, p)
}
