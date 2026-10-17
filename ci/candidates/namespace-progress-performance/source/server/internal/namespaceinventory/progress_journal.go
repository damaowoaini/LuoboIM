package namespaceinventory

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

type progressRef struct {
	Number  int64  `json:"pageNumber"`
	Row     int    `json:"row"`
	MetaSHA string `json:"pageMetadataSHA256"`
}
type progressPage struct {
	Number           int64  `json:"number"`
	Pass             int    `json:"pass"`
	File             string `json:"file"`
	SHA              string `json:"sha256"`
	LegacyCheckpoint string `json:"legacyCheckpointSHA256,omitempty"`
}
type progressRow struct {
	Source string      `json:"source"`
	Mongo  bool        `json:"mongo"`
	SHA    string      `json:"contentSHA256"`
	TTL    int64       `json:"pttl"`
	Ref    progressRef `json:"raw"`
}
type progressOrigin struct {
	CID  string      `json:"cidSHA256"`
	Kind string      `json:"kind"`
	SHA  string      `json:"sha256"`
	Ref  progressRef `json:"raw"`
}
type progressCID struct {
	CID     string       `json:"cidSHA256"`
	Origins string       `json:"originsRoot"`
	Count   int64        `json:"originCount"`
	Head    *progressRef `json:"headRaw,omitempty"`
}
type progressState struct {
	Version        int           `json:"version"`
	SessionSHA     string        `json:"sessionSHA256"`
	Sequence       int64         `json:"sequence"`
	Previous       string        `json:"previousStateSHA256"`
	Phase          string        `json:"phase"`
	Pass           int           `json:"pass"`
	Cursor         PageCursor    `json:"initialOrTerminalCursor"`
	CursorFrom     *progressPage `json:"cursorAfterRawPage,omitempty"`
	LegacySequence int64         `json:"legacySequence"`
	LegacySHA      string        `json:"legacySHA256"`
	LegacyDone     bool          `json:"legacyDone"`
	Pending        *progressPage `json:"pendingPage,omitempty"`
	Row            int           `json:"row"`
	Started        bool          `json:"rowStarted"`
	Origin         int           `json:"origin"`
	RawRoot        string        `json:"rawRoot"`
	RowsRoot       string        `json:"rowsRoot"`
	SeenRoot       string        `json:"seenRoot"`
	CIDRoot        string        `json:"cidRoot"`
	RawCount       int64         `json:"rawPages"`
	RowsCount      int64         `json:"indexedRows"`
	SeenCount      int64         `json:"verifiedRows"`
	After          string        `json:"summaryAfter"`
	CIDAfter       string        `json:"cidAfter"`
	ActiveCID      string        `json:"activeCID"`
	OriginAfter    string        `json:"originAfter"`
	OriginsSeen    int64         `json:"originsSeen"`
	ReducedRaw     int64         `json:"reducedRawPages"`
	ReducedSeen    int64         `json:"reducedSeenRows"`
	ReducedRows    int64         `json:"reducedRows"`
	Summary        ChunkSummary  `json:"summary"`
}
type progressHead struct {
	Version    int    `json:"version"`
	SessionSHA string `json:"sessionSHA256"`
	StateSHA   string `json:"stateSHA256"`
	Sequence   int64  `json:"sequence"`
}

// This is recorded progress, not a fresh full-custody reinspection. Every
// consumed node/page is hash-checked; the final bounded reduction checks all
// raw pages and index references before recording a completed observation.
type ResumableSummary struct {
	State                        string       `json:"state"`
	Phase                        string       `json:"phase"`
	CheckpointSHA                string       `json:"checkpointSHA256"`
	WorkSequence                 int64        `json:"workSequence"`
	RawPages                     int64        `json:"rawPages"`
	IndexedRows                  int64        `json:"indexedRows"`
	VerifiedRows                 int64        `json:"verifiedRows"`
	RecordedTwoPassComplete      bool         `json:"recordedTwoPassComplete"`
	CurrentFullCustodyReverified bool         `json:"currentFullCustodyReverified"`
	RecordedSummary              ChunkSummary `json:"recordedSummary"`
}

func validProgressCID(c progressCID) bool {
	return hexText(c.CID, 64) && c.Count >= 0 && c.Count < math.MaxInt64 && (c.Origins == "" && c.Count == 0 || hexText(c.Origins, 64))
}
func pageNumberKey(n int64) string { return digest([]byte(strconv.FormatInt(n, 10))) }
func (p progressState) valid(s chunkSession) bool {
	if p.Version != 2 || p.SessionSHA != s.SHA || p.Sequence < 0 || p.Sequence >= math.MaxInt64 || !p.Cursor.valid() || (p.Pass != 1 && p.Pass != 2) || p.LegacySequence < 0 || p.LegacySequence >= math.MaxInt64 || !hexText(p.LegacySHA, 64) || p.Row < 0 || p.Row > s.Limits.Rows || p.Origin < 0 || p.Origin > s.Limits.EntryBytes {
		return false
	}
	if p.Phase != "rebuild" && p.Phase != "capture" && p.Phase != "summary-raw" && p.Phase != "summary-seen" && p.Phase != "summary-rows" && p.Phase != "summary-origins" && p.Phase != "complete" {
		return false
	}
	for _, h := range []string{p.Previous, p.RawRoot, p.RowsRoot, p.SeenRoot, p.CIDRoot, p.After, p.CIDAfter, p.ActiveCID, p.OriginAfter} {
		if h != "" && !hexText(h, 64) {
			return false
		}
	}
	for _, n := range []int64{p.RawCount, p.RowsCount, p.SeenCount, p.OriginsSeen, p.ReducedRaw, p.ReducedSeen, p.ReducedRows, p.Summary.MongoRows, p.Summary.RedisKeys, p.Summary.ErasedSlots, p.Summary.CandidateCollisions} {
		if n < 0 || n >= math.MaxInt64 {
			return false
		}
	}
	if len(p.Cursor.AfterID) != 0 || len(p.Cursor.Pending) != 0 {
		return false
	}
	if p.CursorFrom != nil && (p.CursorFrom.Number < 1 || p.CursorFrom.Number > p.RawCount || p.CursorFrom.Pass != p.Pass) {
		return false
	}
	if p.Pending == nil && (p.Row != 0 || p.Started || p.Origin != 0) {
		return false
	}
	if p.Pending != nil && (p.Pending.Number < 1 || p.Pending.Number > p.RawCount || p.Pending.Pass != p.Pass) {
		return false
	}
	if p.Phase == "complete" && (p.Pending != nil || p.ReducedRaw != p.RawCount || p.ReducedRows != p.RowsCount || p.ReducedSeen != p.SeenCount || p.SeenCount != p.RowsCount || p.ActiveCID != "") {
		return false
	}
	if p.Summary.State != State || p.Summary.Reasons == nil {
		return false
	}
	for reason, n := range p.Summary.Reasons {
		if !knownProgressReason(reason) || n < 0 || n >= math.MaxInt64 {
			return false
		}
	}
	return true
}
func loadProgress(dir string, s chunkSession) (progressHead, progressState, error) {
	var h progressHead
	var p progressState
	if e := loadPrivateJSON(filepath.Join(dir, "progress-head.json"), 64<<10, &h); e != nil {
		return h, p, e
	}
	if h.Version != 2 || h.SessionSHA != s.SHA || !hexText(h.StateSHA, 64) || h.Sequence < 1 {
		return h, p, ErrInvalid
	}
	if e := (progressStore{dir}).load("state", h.StateSHA, 128<<10, &p); e != nil {
		return h, p, e
	}
	if !p.valid(s) || p.Sequence != h.Sequence {
		return h, p, ErrInvalid
	}
	if p.Sequence == 1 {
		if p.Previous != "" {
			return h, p, ErrInvalid
		}
	} else {
		var previous progressState
		if e := (progressStore{dir}).load("state", p.Previous, 128<<10, &previous); e != nil {
			return h, p, e
		}
		if previous.Sequence != p.Sequence-1 || previous.SessionSHA != s.SHA {
			return h, p, ErrInvalid
		}
	}
	return h, p, nil
}
func commitProgress(dir string, s chunkSession, old progressHead, p progressState) (progressHead, error) {
	if old.Sequence >= math.MaxInt64-2 {
		return old, ErrLimit
	}
	p.Sequence = old.Sequence + 1
	p.Previous = old.StateSHA
	if !p.valid(s) {
		return old, ErrInvalid
	}
	h, e := (progressStore{dir}).save("state", p)
	if e != nil {
		return old, e
	}
	next := progressHead{Version: 2, SessionSHA: s.SHA, StateSHA: h, Sequence: p.Sequence}
	path := filepath.Join(dir, "progress-head.json")
	if old.Sequence == 0 {
		if _, e := writePrivateJSON(path, next); e != nil {
			return old, e
		}
		return next, nil
	}
	// All cooperating legacy/progress operators hold the SAME journal flock.
	// Replace only the exact known owned head; no unknown target is overwritten.
	var current progressHead
	if loadPrivateJSON(path, 64<<10, &current) != nil || current != old {
		return old, ErrPrivate
	}
	before, e := os.Lstat(path)
	if e != nil {
		return old, ErrPrivate
	}
	id, e := uuid.NewRandom()
	if e != nil {
		return old, ErrSource
	}
	temp := "head-" + id.String() + ".json"
	if _, e := writePrivateJSON(filepath.Join(dir, temp), next); e != nil {
		return old, e
	}
	parent, name, e := privateParent(path)
	if e != nil {
		return old, e
	}
	defer unix.Close(parent)
	after, e := os.Lstat(path)
	if e != nil || !os.SameFile(before, after) || loadPrivateJSON(path, 64<<10, &current) != nil || current != old {
		return old, ErrPrivate
	}
	if unix.Renameat(parent, temp, parent, name) != nil || unix.Fsync(parent) != nil {
		return old, ErrPrivate
	}
	return next, nil
}
func (s progressStore) page(ctx context.Context, st progressState, m progressPage, limits Limits) (Page, error) {
	var expected progressPage
	ok, e := s.get(ctx, st.RawRoot, pageNumberKey(m.Number), &expected)
	if e != nil {
		return Page{}, e
	}
	if !ok || expected != m || m.Number < 1 || (m.Pass != 1 && m.Pass != 2) || !hexText(m.SHA, 64) || filepath.Base(m.File) != m.File {
		return Page{}, ErrInvalid
	}
	if m.LegacyCheckpoint != "" {
		if !hexText(m.LegacyCheckpoint, 64) || len(m.File) != 46 || !strings.HasPrefix(m.File, "page-") || !strings.HasSuffix(m.File, ".json") || !validUUID(m.File[5:41]) {
			return Page{}, ErrInvalid
		}
		var cp chunkCheckpoint
		if loadPrivateJSON(filepath.Join(s.dir, checkpointName(m.Number)), 64<<10, &cp) != nil || cp.SHA != m.LegacyCheckpoint || cp.SHA != checkpointSHA(cp) || cp.Sequence != m.Number || cp.Pass != m.Pass || cp.Page != m.File || cp.PageSHA != m.SHA {
			return Page{}, ErrInvalid
		}
	} else if m.File != "pagebody-"+m.SHA+".json" {
		return Page{}, ErrInvalid
	}
	raw, e := ReadPrivateDocument(filepath.Join(s.dir, m.File), MaxPrivateSnapshot)
	if e != nil {
		return Page{}, e
	}
	return decodeProgressPage(ctx, m.SHA, raw, limits)
}

// Opaque large cursors are referenced by the already committed raw page.
// Repeated row steps never copy a multi-MiB BSON id/SCAN tail into each state.
func (s progressStore) cursor(ctx context.Context, st progressState, l Limits) (PageCursor, error) {
	if st.CursorFrom == nil {
		return st.Cursor, nil
	}
	page, e := s.page(ctx, st, *st.CursorFrom, l)
	if e != nil {
		return PageCursor{}, e
	}
	return page.After, nil
}
func (s progressStore) hydrate(ctx context.Context, st progressState, ref progressRef, limits Limits) (indexRecord, error) {
	if ref.Number < 1 || ref.Number > st.RawCount || ref.Row < 0 || ref.Row >= limits.Rows || !hexText(ref.MetaSHA, 64) {
		return indexRecord{}, ErrInvalid
	}
	var meta progressPage
	ok, e := s.get(ctx, st.RawRoot, pageNumberKey(ref.Number), &meta)
	if e != nil || !ok || jsonSHA(meta) != ref.MetaSHA {
		return indexRecord{}, ErrInvalid
	}
	page, e := s.page(ctx, st, meta, limits)
	if e != nil {
		return indexRecord{}, e
	}
	return progressIndexRow(page, ref.Row)
}
func storedRow(rec indexRecord, ref progressRef) progressRow {
	return progressRow{Source: rec.Source, Mongo: rec.Mongo, SHA: rec.ContentSHA, TTL: rec.TTL, Ref: ref}
}
func (s progressStore) checkedRow(ctx context.Context, st progressState, row progressRow, l Limits) (indexRecord, error) {
	var meta progressPage
	ok, e := s.get(ctx, st.RawRoot, pageNumberKey(row.Ref.Number), &meta)
	if e != nil {
		return indexRecord{}, e
	}
	if !ok || meta.Pass != 1 {
		return indexRecord{}, ErrInvalid
	}
	rec, e := s.hydrate(ctx, st, row.Ref, l)
	if e != nil {
		return rec, e
	}
	if rec.Source != row.Source || rec.Mongo != row.Mongo || rec.ContentSHA != row.SHA || rec.TTL != row.TTL {
		return rec, ErrInvalid
	}
	return rec, nil
}
func publicProgress(p progressState, h progressHead) ResumableSummary {
	summary := p.Summary
	summary.CheckpointSHA = h.StateSHA
	summary.Checkpoints = h.Sequence
	summary.ObservedComplete = p.Phase == "complete"
	// Only the finite source taxonomy is public, never attacker-edited map keys.
	filtered := map[string]int64{}
	for name, n := range summary.Reasons {
		if n >= 0 && knownProgressReason(name) {
			filtered[name] = n
		}
	}
	summary.Reasons = filtered
	return ResumableSummary{State: State, Phase: p.Phase, CheckpointSHA: h.StateSHA, WorkSequence: h.Sequence, RawPages: p.RawCount, IndexedRows: p.RowsCount, VerifiedRows: p.SeenCount, RecordedTwoPassComplete: p.Phase == "complete", CurrentFullCustodyReverified: false, RecordedSummary: summary}
}
func knownProgressReason(v string) bool {
	switch v {
	case "external-drain-unverified", "old-binary-exclusion-unverified", "historical-lifecycle-coverage-unverified", "two-pass-observation-is-not-cross-store-atomicity", "tenant-is-operator-binding-not-storage-attestation", "checkpoint-is-not-drain-lifecycle-or-atomic-cut-attestation", "malformed-or-duplicate-BSON", "malformed-message-document", "invalid-document-coordinate", "deleted-slot-retains-original-origin-only", "message-origin-or-slot-coordinate-invalid", "erased-slot-origin-and-head-incomplete", "no-retained-message-origin", "unresolved-current-account-routing", "current-routing-does-not-prove-nonreuse", "unresolved-current-group-routing", "invalid-shared-head", "metadata-observation-not-origin-or-owner-grant", "redis-key-does-not-prove-typed-origin", "invalid-raw-redis-head", "expiring-raw-head", "no-observed-raw-head-zero-not-inferred", "ambiguous-current-typed-origin-candidates":
		return true
	}
	return false
}
func addProgressCount(n *int64, delta int64) error {
	if delta < 0 || *n < 0 || *n >= math.MaxInt64-delta {
		return ErrLimit
	}
	*n += delta
	return nil
}
func addProgressReason(p *progressState, r string, n int64) error {
	if !knownProgressReason(r) {
		return ErrInvalid
	}
	old := p.Summary.Reasons[r]
	if e := addProgressCount(&old, n); e != nil {
		return e
	}
	p.Summary.Reasons[r] = old
	return nil
}
func initialProgress(s chunkSession) progressState {
	p := progressState{Version: 2, SessionSHA: s.SHA, Phase: "rebuild", Pass: 1, Cursor: firstPageCursor(), LegacySHA: s.SHA, Summary: ChunkSummary{State: State, Reasons: map[string]int64{}}}
	for _, r := range fixedReasons(s.Identity) {
		p.Summary.Reasons[r]++
	}
	p.Summary.Reasons["checkpoint-is-not-drain-lifecycle-or-atomic-cut-attestation"]++
	return p
}
func missingPrivate(path string) bool { _, e := os.Lstat(path); return errors.Is(e, os.ErrNotExist) }
