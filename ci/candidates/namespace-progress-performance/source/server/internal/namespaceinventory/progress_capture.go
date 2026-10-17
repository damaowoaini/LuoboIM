package namespaceinventory

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Fixed work quanta, not total-history limits. A row with many candidate
// origins is split into origin steps. Final raw/seen/row/CID reduction is
// resumable too, so no last invocation must re-read the entire history.
type ProgressBudget struct {
	Steps       int
	SourcePages int
}

func (b ProgressBudget) valid() bool {
	return b.Steps >= 1 && b.Steps <= 1024 && b.SourcePages >= 1 && b.SourcePages <= 128
}
func sameObservedRow(old progressRow, now indexRecord) bool {
	return old.Source == now.Source && old.Mongo == now.Mongo && old.SHA == now.ContentSHA && (now.Mongo || ((old.TTL < 0) == (now.TTL < 0) && !(now.TTL > old.TTL && now.TTL-old.TTL > 100)))
}
func (s progressStore) cid(ctx context.Context, p progressState, cid string) (progressCID, error) {
	var result progressCID
	ok, e := s.get(ctx, p.CIDRoot, digest([]byte(cid)), &result)
	if e != nil {
		return result, e
	}
	if !ok {
		return progressCID{CID: digest([]byte(cid))}, nil
	}
	if result.CID != digest([]byte(cid)) || !validProgressCID(result) {
		return result, ErrInvalid
	}
	return result, nil
}
func (s progressStore) registerPage(ctx context.Context, p *progressState, m progressPage) error {
	if p.RawCount >= math.MaxInt64-1 {
		return ErrLimit
	}
	m.Number = p.RawCount + 1
	h, e := s.put(ctx, p.RawRoot, pageNumberKey(m.Number), m)
	if e != nil {
		return e
	}
	p.RawRoot = h
	p.RawCount++
	p.Pending = &m
	p.Row = 0
	p.Origin = 0
	p.Started = false
	return nil
}
func (s progressStore) rebuildStep(ctx context.Context, session chunkSession, p *progressState) error {
	if p.LegacySequence >= math.MaxInt64-1 {
		return ErrLimit
	}
	path := filepath.Join(s.dir, checkpointName(p.LegacySequence+1))
	if missingPrivate(path) {
		if p.LegacyDone {
			if p.RowsCount != p.SeenCount {
				return ErrChanged
			}
			p.Phase = "summary-raw"
		} else {
			p.Phase = "capture"
		}
		return nil
	}
	if p.LegacyDone {
		return ErrInvalid
	}
	var cp chunkCheckpoint
	if e := loadPrivateJSON(path, 64<<10, &cp); e != nil {
		return e
	}
	if cp.Version != 1 || cp.Session != session.ID || cp.Sequence != p.LegacySequence+1 || cp.Pass != p.Pass || cp.Previous != p.LegacySHA || cp.SHA != checkpointSHA(cp) || !hexText(cp.PageSHA, 64) || len(cp.Page) != 46 || !strings.HasPrefix(cp.Page, "page-") || !strings.HasSuffix(cp.Page, ".json") || !validUUID(cp.Page[5:41]) {
		return ErrInvalid
	}
	b, e := ReadPrivateDocument(filepath.Join(s.dir, cp.Page), MaxPrivateSnapshot)
	if e != nil {
		return e
	}
	if digest(b) != cp.PageSHA {
		return ErrInvalid
	}
	cursor, e := s.cursor(ctx, *p, session.Limits)
	if e != nil {
		return e
	}
	var page Page
	if DecodeStrict(b, &page) != nil || validatePage(page, session.Limits) != nil || !sameCursor(page.Before, cursor) {
		return ErrInvalid
	}
	return s.registerPage(ctx, p, progressPage{Pass: cp.Pass, File: cp.Page, SHA: cp.PageSHA, LegacyCheckpoint: cp.SHA})
}
func (s progressStore) processPending(ctx context.Context, session chunkSession, p *progressState) error {
	if p.Pending == nil {
		return ErrInvalid
	}
	page, e := s.page(ctx, *p, *p.Pending, session.Limits)
	if e != nil {
		return e
	}
	cursor, e := s.cursor(ctx, *p, session.Limits)
	if e != nil {
		return e
	}
	if !sameCursor(page.Before, cursor) {
		return ErrInvalid
	}
	if p.Row >= len(page.Mongo)+len(page.Redis) {
		if p.Started || p.Origin != 0 {
			return ErrInvalid
		}
		legacy := p.Pending.LegacyCheckpoint
		last := *p.Pending
		p.Cursor = firstPageCursor()
		p.CursorFrom = &last
		p.Pending = nil
		p.Row = 0
		if page.After.Phase == "done" {
			p.CursorFrom = nil
			if p.Pass == 1 {
				p.Pass = 2
				p.Cursor = firstPageCursor()
			} else {
				p.Cursor = PageCursor{Phase: "done"}
				if p.RowsCount != p.SeenCount {
					return ErrChanged
				}
				if legacy != "" {
					p.LegacyDone = true
				} else {
					p.Phase = "summary-raw"
				}
			}
		}
		if legacy != "" {
			p.LegacySequence++
			p.LegacySHA = legacy
		}
		return nil
	}
	rec, e := progressIndexRow(page, p.Row)
	if e != nil {
		return e
	}
	key := digest([]byte(rec.Source))
	ref := progressRef{Number: p.Pending.Number, Row: p.Row, MetaSHA: jsonSHA(*p.Pending)}
	if p.Pass == 2 {
		var old progressRow
		ok, e := s.get(ctx, p.RowsRoot, key, &old)
		if e != nil {
			return e
		}
		if !ok {
			return ErrChanged
		}
		if _, e := s.checkedRow(ctx, *p, old, session.Limits); e != nil {
			return e
		}
		if !sameObservedRow(old, rec) {
			return ErrChanged
		}
		var seen progressRef
		exists, e := s.get(ctx, p.SeenRoot, key, &seen)
		if e != nil {
			return e
		}
		if !exists {
			h, e := s.put(ctx, p.SeenRoot, key, ref)
			if e != nil {
				return e
			}
			p.SeenRoot = h
			if e := addProgressCount(&p.SeenCount, 1); e != nil {
				return e
			}
		}
		p.Row++
		return nil
	}
	if !p.Started {
		var old progressRow
		exists, e := s.get(ctx, p.RowsRoot, key, &old)
		if e != nil {
			return e
		}
		if exists {
			if _, e := s.checkedRow(ctx, *p, old, session.Limits); e != nil {
				return e
			}
			if !sameObservedRow(old, rec) {
				return ErrChanged
			}
			p.Row++
			return nil
		}
		h, e := s.put(ctx, p.RowsRoot, key, storedRow(rec, ref))
		if e != nil {
			return e
		}
		p.RowsRoot = h
		if e := addProgressCount(&p.RowsCount, 1); e != nil {
			return e
		}
		if rec.Observation.CID != "" {
			cid, e := s.cid(ctx, *p, rec.Observation.CID)
			if e != nil {
				return e
			}
			if rec.Observation.RawHead != nil && cid.Head == nil {
				copy := ref
				cid.Head = &copy
			}
			h, e := s.put(ctx, p.CIDRoot, cid.CID, cid)
			if e != nil {
				return e
			}
			p.CIDRoot = h
		}
		p.Started = true
		return nil
	}
	if p.Origin < len(rec.Observation.Origins) {
		o := rec.Observation.Origins[p.Origin]
		cid, e := s.cid(ctx, *p, rec.Observation.CID)
		if e != nil {
			return e
		}
		originKey := digest([]byte(o.Kind + ":" + o.SHA))
		var old progressOrigin
		exists, e := s.get(ctx, cid.Origins, originKey, &old)
		if e != nil {
			return e
		}
		if exists {
			if old.CID != cid.CID || old.Kind != o.Kind || old.SHA != o.SHA {
				return ErrInvalid
			}
		} else {
			h, e := s.put(ctx, cid.Origins, originKey, progressOrigin{CID: cid.CID, Kind: o.Kind, SHA: o.SHA, Ref: ref})
			if e != nil {
				return e
			}
			cid.Origins = h
			if e := addProgressCount(&cid.Count, 1); e != nil {
				return e
			}
			h, e = s.put(ctx, p.CIDRoot, cid.CID, cid)
			if e != nil {
				return e
			}
			p.CIDRoot = h
		}
		p.Origin++
		return nil
	}
	p.Row++
	p.Started = false
	p.Origin = 0
	return nil
}
func (s progressStore) summaryStep(ctx context.Context, session chunkSession, p *progressState) error {
	switch p.Phase {
	case "summary-raw":
		item, ok, e := s.next(ctx, p.RawRoot, p.After)
		if e != nil {
			return e
		}
		if !ok {
			if p.ReducedRaw != p.RawCount {
				return ErrInvalid
			}
			p.Phase = "summary-seen"
			p.After = ""
			return nil
		}
		var meta progressPage
		if DecodeStrict(item.Value, &meta) != nil || item.Key != pageNumberKey(meta.Number) {
			return ErrInvalid
		}
		if _, e := s.page(ctx, *p, meta, session.Limits); e != nil {
			return e
		}
		if e := addProgressCount(&p.ReducedRaw, 1); e != nil {
			return e
		}
		p.After = item.Key
		return nil
	case "summary-seen":
		item, ok, e := s.next(ctx, p.SeenRoot, p.After)
		if e != nil {
			return e
		}
		if !ok {
			if p.ReducedSeen != p.SeenCount || p.SeenCount != p.RowsCount {
				return ErrChanged
			}
			p.Phase = "summary-rows"
			p.After = ""
			return nil
		}
		var ref progressRef
		if DecodeStrict(item.Value, &ref) != nil {
			return ErrInvalid
		}
		var meta progressPage
		found, e := s.get(ctx, p.RawRoot, pageNumberKey(ref.Number), &meta)
		if e != nil {
			return e
		}
		if !found || meta.Pass != 2 {
			return ErrInvalid
		}
		rec, e := s.hydrate(ctx, *p, ref, session.Limits)
		if e != nil {
			return e
		}
		if digest([]byte(rec.Source)) != item.Key {
			return ErrInvalid
		}
		var old progressRow
		found, e = s.get(ctx, p.RowsRoot, item.Key, &old)
		if e != nil {
			return e
		}
		if !found {
			return ErrChanged
		}
		if _, e := s.checkedRow(ctx, *p, old, session.Limits); e != nil {
			return e
		}
		if !sameObservedRow(old, rec) {
			return ErrChanged
		}
		if e := addProgressCount(&p.ReducedSeen, 1); e != nil {
			return e
		}
		p.After = item.Key
		return nil
	case "summary-rows":
		item, ok, e := s.next(ctx, p.RowsRoot, p.After)
		if e != nil {
			return e
		}
		if !ok {
			if p.ReducedRows != p.RowsCount {
				return ErrInvalid
			}
			p.Phase = "summary-origins"
			p.After = ""
			return nil
		}
		var row progressRow
		if DecodeStrict(item.Value, &row) != nil || digest([]byte(row.Source)) != item.Key {
			return ErrInvalid
		}
		rec, e := s.checkedRow(ctx, *p, row, session.Limits)
		if e != nil {
			return e
		}
		if row.Mongo {
			e = addProgressCount(&p.Summary.MongoRows, 1)
		} else {
			e = addProgressCount(&p.Summary.RedisKeys, 1)
		}
		if e != nil {
			return e
		}
		if e := addProgressCount(&p.Summary.ErasedSlots, int64(rec.Observation.Erased)); e != nil {
			return e
		}
		for _, reason := range rec.Observation.Reasons {
			if e := addProgressReason(p, reason, 1); e != nil {
				return e
			}
		}
		if rec.Observation.CID != "" {
			cid, e := s.cid(ctx, *p, rec.Observation.CID)
			if e != nil {
				return e
			}
			if cid.Head == nil {
				if e := addProgressReason(p, "no-observed-raw-head-zero-not-inferred", 1); e != nil {
					return e
				}
			} else {
				head, e := s.hydrate(ctx, *p, *cid.Head, session.Limits)
				if e != nil {
					return e
				}
				if digest([]byte(head.Observation.CID)) != cid.CID || head.Observation.RawHead == nil {
					return ErrInvalid
				}
			}
		}
		if e := addProgressCount(&p.ReducedRows, 1); e != nil {
			return e
		}
		p.After = item.Key
		return nil
	case "summary-origins":
		if p.ActiveCID == "" {
			item, ok, e := s.next(ctx, p.CIDRoot, p.CIDAfter)
			if e != nil {
				return e
			}
			if !ok {
				p.Phase = "complete"
				return nil
			}
			var cid progressCID
			if DecodeStrict(item.Value, &cid) != nil || cid.CID != item.Key {
				return ErrInvalid
			}
			if !validProgressCID(cid) {
				return ErrInvalid
			}
			p.ActiveCID = item.Key
			p.OriginsSeen = 0
			p.OriginAfter = ""
			return nil
		}
		var cid progressCID
		ok, e := s.get(ctx, p.CIDRoot, p.ActiveCID, &cid)
		if e != nil {
			return e
		}
		if !ok || cid.CID != p.ActiveCID {
			return ErrInvalid
		}
		item, ok, e := s.next(ctx, cid.Origins, p.OriginAfter)
		if e != nil {
			return e
		}
		if !ok {
			if p.OriginsSeen != cid.Count {
				return ErrInvalid
			}
			if p.OriginsSeen > 1 {
				if e := addProgressCount(&p.Summary.CandidateCollisions, 1); e != nil {
					return e
				}
				if e := addProgressReason(p, "ambiguous-current-typed-origin-candidates", 1); e != nil {
					return e
				}
			}
			p.CIDAfter = p.ActiveCID
			p.ActiveCID = ""
			p.OriginAfter = ""
			p.OriginsSeen = 0
			return nil
		}
		var origin progressOrigin
		if DecodeStrict(item.Value, &origin) != nil || origin.CID != cid.CID || item.Key != digest([]byte(origin.Kind+":"+origin.SHA)) {
			return ErrInvalid
		}
		rec, e := s.hydrate(ctx, *p, origin.Ref, session.Limits)
		if e != nil {
			return e
		}
		found := false
		if digest([]byte(rec.Observation.CID)) == cid.CID {
			for _, o := range rec.Observation.Origins {
				if o.Kind == origin.Kind && o.SHA == origin.SHA {
					found = true
					break
				}
			}
		}
		if !found {
			return ErrInvalid
		}
		if e := addProgressCount(&p.OriginsSeen, 1); e != nil {
			return e
		}
		p.OriginAfter = item.Key
		return nil
	}
	return ErrInvalid
}

func BeginResumableCapture(ctx context.Context, source PageSource, b Binding, dir string, l Limits, budget ProgressBudget) (ResumableSummary, error) {
	if ctx == nil || source == nil || !b.valid() || !pageLimits(l) || !budget.valid() {
		return ResumableSummary{}, ErrInvalid
	}
	id, e := source.Identity(ctx)
	if e != nil {
		return ResumableSummary{}, e
	}
	if !id.valid() || id.Binding != b {
		return ResumableSummary{}, ErrIdentity
	}
	u, e := uuid.NewRandom()
	if e != nil {
		return ResumableSummary{}, ErrSource
	}
	session := chunkSession{Version: 1, ID: u.String(), Created: time.Now().UTC(), State: State, Identity: id, Limits: l}
	session.SHA = sessionSHA(session)
	if e := createJournal(dir, session); e != nil {
		return ResumableSummary{}, e
	}
	return ResumeResumableCapture(ctx, source, b, dir, budget)
}

// Supports an existing V1 chunk journal too: its old chain is validated and
// indexed ONE bounded work step at a time, then continues the original cursor.
// No raw is copied into a fresh identity or scanned again to rebuild a prefix.
func ResumeResumableCapture(ctx context.Context, source PageSource, b Binding, dir string, budget ProgressBudget) (out ResumableSummary, resultErr error) {
	if ctx == nil || source == nil || !b.valid() || !budget.valid() {
		return out, ErrInvalid
	}
	// Parsed pages live only for this invocation. Every use still rereads and
	// hashes the private raw file and verifies current committed metadata.
	ctx = newProgressPageContext(ctx)
	defer closeProgressPageContext(ctx)
	lock, e := lockJournal(dir)
	if e != nil {
		return out, e
	}
	defer func() {
		if lock.Close() != nil {
			out = ResumableSummary{}
			resultErr = ErrPrivate
		}
	}()
	session, e := loadSession(dir)
	if e != nil {
		return out, e
	}
	if session.Identity.Binding != b {
		return out, ErrIdentity
	}
	check := func() error {
		got, e := source.Identity(ctx)
		if e != nil {
			return e
		}
		if got != session.Identity {
			return ErrIdentity
		}
		return ctx.Err()
	}
	if e := check(); e != nil {
		return out, e
	}
	store := progressStore{dir}
	var head progressHead
	var p progressState
	if missingPrivate(filepath.Join(dir, "progress-head.json")) {
		p = initialProgress(session)
		head, e = commitProgress(dir, session, head, p)
		if e != nil {
			return out, e
		}
	}
	head, p, e = loadProgress(dir, session)
	if e != nil {
		return out, e
	}
	pages := 0
	for work := 0; work < budget.Steps && p.Phase != "complete"; work++ {
		if e := ctx.Err(); e != nil {
			return out, e
		}
		if p.Pending != nil {
			e = store.processPending(ctx, session, &p)
		} else if p.Phase == "rebuild" {
			e = store.rebuildStep(ctx, session, &p)
		} else if p.Phase == "capture" {
			if pages >= budget.SourcePages {
				break
			}
			if e := check(); e != nil {
				return out, e
			}
			cursor, err := store.cursor(ctx, p, session.Limits)
			if err != nil {
				return out, err
			}
			page, err := source.Page(ctx, cursor, session.Limits)
			if err != nil {
				return out, err
			}
			if !sameCursor(page.Before, cursor) || validatePage(page, session.Limits) != nil {
				return out, ErrInvalid
			}
			if e := check(); e != nil {
				return out, e
			}
			h, err := store.save("pagebody", page)
			if err != nil {
				return out, err
			}
			e = store.registerPage(ctx, &p, progressPage{Pass: p.Pass, File: "pagebody-" + h + ".json", SHA: h})
			pages++
		} else {
			e = store.summaryStep(ctx, session, &p)
		}
		if e != nil {
			return out, e
		}
		next, err := commitProgress(dir, session, head, p)
		if err != nil {
			return out, err
		}
		p.Previous = head.StateSHA
		head = next
		p.Sequence = head.Sequence
	}
	if e := check(); e != nil {
		return out, e
	}
	return publicProgress(p, head), nil
}

// Pure offline progress read: bounded head/session/root checks only. It does
// NOT claim a fresh reinspection of every historical byte. Resume performs
// bounded full reductions before it can RECORD a complete two-pass summary.
func InspectResumableProgress(dir string) (out ResumableSummary, resultErr error) {
	lock, e := lockJournal(dir)
	if e != nil {
		return out, e
	}
	defer func() {
		if lock.Close() != nil {
			out = ResumableSummary{}
			resultErr = ErrPrivate
		}
	}()
	s, e := loadSession(dir)
	if e != nil {
		return out, e
	}
	h, p, e := loadProgress(dir, s)
	if e != nil {
		return out, e
	}
	for _, root := range []string{p.RawRoot, p.RowsRoot, p.SeenRoot, p.CIDRoot} {
		if root != "" {
			if _, e := (progressStore{dir}).node(root, ""); e != nil {
				return out, e
			}
		}
	}
	return publicProgress(p, h), nil
}
