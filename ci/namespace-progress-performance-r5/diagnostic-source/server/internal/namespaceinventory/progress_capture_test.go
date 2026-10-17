package namespaceinventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func progressAt(t *testing.T, dir string) (progressHead, progressState) {
	t.Helper()
	s, e := loadSession(dir)
	if e != nil {
		t.Fatal(e)
	}
	h, p, e := loadProgress(dir, s)
	if e != nil {
		t.Fatal(e)
	}
	return h, p
}
func progressUntil(t *testing.T, s *pagedFixture, dir, phase string) {
	t.Helper()
	for n := 0; n < 4096; n++ {
		_, p := progressAt(t, dir)
		if p.Phase == phase {
			return
		}
		if _, e := ResumeResumableCapture(context.Background(), s, s.id.Binding, dir, ProgressBudget{Steps: 1, SourcePages: 1}); e != nil {
			t.Fatal("bounded progress failed", e)
		}
	}
	t.Fatal("target phase not reached")
}
func completeProgress(t *testing.T, s *pagedFixture, dir string) ResumableSummary {
	t.Helper()
	for n := 0; n < 4096; n++ {
		out, e := ResumeResumableCapture(context.Background(), s, s.id.Binding, dir, ProgressBudget{Steps: 7, SourcePages: 2})
		if e != nil {
			t.Fatal("bounded resume failed", e)
		}
		if out.RecordedTwoPassComplete {
			return out
		}
	}
	t.Fatal("bounded progress never completed")
	return ResumableSummary{}
}

func TestNamespaceProgressContinuesAcrossEveryStage(t *testing.T) {
	s, dir := pageFixture(t), chunkDir(t)
	out, e := BeginResumableCapture(context.Background(), s, s.id.Binding, dir, chunkFixtureLimits(), ProgressBudget{Steps: 1, SourcePages: 1})
	if e != nil || out.Phase != "capture" || out.RecordedTwoPassComplete {
		t.Fatal("bounded initialization contract", e)
	}
	phases := map[string]bool{}
	last := out
	var latest progressState
	for n := 0; n < 2048; n++ {
		beforeCalls := s.pageCalls
		out, e = ResumeResumableCapture(context.Background(), s, s.id.Binding, dir, ProgressBudget{Steps: 1, SourcePages: 1})
		if e != nil {
			t.Fatal("single-quantum resume failed", e)
		}
		if out.WorkSequence != last.WorkSequence+1 || out.RawPages < last.RawPages || out.IndexedRows < last.IndexedRows || out.VerifiedRows < last.VerifiedRows || s.pageCalls-beforeCalls > 1 {
			t.Fatal("resume reset or exceeded its fixed work quantum")
		}
		phases[out.Phase] = true
		_, latest = progressAt(t, dir)
		last = out
		// Every call reopens only from on-disk head; no in-memory session/index
		// from the previous invocation is passed to the next one.
		if out.RecordedTwoPassComplete {
			break
		}
	}
	for _, phase := range []string{"capture", "summary-raw", "summary-seen", "summary-rows", "summary-origins", "complete"} {
		if !phases[phase] {
			t.Fatal("a persisted stage was skipped", phase)
		}
	}
	if !out.RecordedTwoPassComplete || out.State != State || out.CurrentFullCustodyReverified || out.RecordedSummary.CandidateCollisions != 2 || out.RecordedSummary.ErasedSlots != 1 || out.RecordedSummary.MongoRows != int64(len(collections)+4) || out.RecordedSummary.RedisKeys != 1 {
		t.Fatal("resumable summary lost evidence or claimed authority")
	}
	if latest.ReducedRaw != out.RawPages || latest.ReducedRows != out.IndexedRows || latest.ReducedSeen != out.VerifiedRows {
		t.Fatal("final reductions did not cover recorded roots")
	}
	beforeCalls := s.pageCalls
	again, e := ResumeResumableCapture(context.Background(), s, s.id.Binding, dir, ProgressBudget{Steps: 1, SourcePages: 1})
	if e != nil || !reflect.DeepEqual(out, again) || beforeCalls != s.pageCalls {
		t.Fatal("completed lost reply replayed source work")
	}
	inspected, e := InspectResumableProgress(dir)
	if e != nil || !reflect.DeepEqual(inspected, out) {
		t.Fatal("bounded offline progress read differs")
	}
	raw, _ := json.Marshal(inspected)
	for _, value := range []string{"si_a_b_c", "synthetic-tenant", "synthetic-dump", "n_a_b"} {
		if strings.Contains(string(raw), value) {
			t.Fatal("progress disclosed private evidence")
		}
	}
}

func TestNamespaceProgressImportsOldPrefixWithoutRescanning(t *testing.T) {
	s, dir := pageFixture(t), chunkDir(t)
	firstPassOnly(t, s, dir)
	originalCalls := s.pageCalls
	oldSession, e := loadSession(dir)
	if e != nil {
		t.Fatal(e)
	}
	oldPosition, e := replayJournal(context.Background(), dir, oldSession, func(Page, int) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	original := map[string][]byte{}
	for n := int64(1); n <= oldPosition.Sequence; n++ {
		name := checkpointName(n)
		b, e := ReadPrivateDocument(filepath.Join(dir, name), 64<<10)
		if e != nil {
			t.Fatal(e)
		}
		original[name] = b
	}
	for n := 0; n < 256; n++ {
		if _, e := ResumeResumableCapture(context.Background(), s, s.id.Binding, dir, ProgressBudget{Steps: 1, SourcePages: 1}); e != nil {
			t.Fatal(e)
		}
		_, p := progressAt(t, dir)
		if p.LegacySequence >= 2 && p.Pending == nil {
			break
		}
		if n == 255 {
			t.Fatal("legacy rebuild made no durable progress")
		}
	}
	_, before := progressAt(t, dir)
	hidden := filepath.Join(dir, "checkpoint-prefix-private-backup.json")
	first := filepath.Join(dir, checkpointName(1))
	if os.Rename(first, hidden) != nil {
		t.Fatal("private prefix probe setup")
	}
	// This next bounded unit registers only the NEXT original checkpoint.
	// A restart-from-page1 implementation would fail on the hidden cp1.
	_, probeErr := ResumeResumableCapture(context.Background(), s, s.id.Binding, dir, ProgressBudget{Steps: 1, SourcePages: 1})
	if os.Rename(hidden, first) != nil {
		t.Fatal("private prefix probe restore")
	}
	if probeErr != nil {
		t.Fatal("resume unnecessarily restarted old prefix", probeErr)
	}
	_, after := progressAt(t, dir)
	if after.LegacySequence < before.LegacySequence || after.RawCount != before.RawCount+1 {
		t.Fatal("rebuild did not advance from stored prefix")
	}
	out := completeProgress(t, s, dir)
	if out.RecordedSummary.CandidateCollisions != 2 || out.RecordedSummary.ErasedSlots != 1 || s.pageCalls-originalCalls != originalCalls {
		t.Fatal("conversion replayed first-pass source or lost raw evidence")
	}
	for name, b := range original {
		got, e := ReadPrivateDocument(filepath.Join(dir, name), 64<<10)
		if e != nil || !reflect.DeepEqual(got, b) {
			t.Fatal("old immutable checkpoint was overwritten")
		}
	}
	if _, e := ResumeChunkCapture(context.Background(), s, s.id.Binding, dir, 1); !errors.Is(e, ErrPrivate) {
		t.Fatal("legacy API forked converted history")
	}
	if _, e := InspectChunks(context.Background(), dir); !errors.Is(e, ErrPrivate) {
		t.Fatal("legacy inspection mislabeled converted progress")
	}
}

func TestNamespaceProgressRejectsUnboundDerivedState(t *testing.T) {
	for _, kind := range []string{"raw-tamper", "index-tamper", "valid-hash-foreign-reference", "restart", "cancelled", "partial-head"} {
		t.Run(kind, func(t *testing.T) {
			s, dir := pageFixture(t), chunkDir(t)
			if _, e := BeginResumableCapture(context.Background(), s, s.id.Binding, dir, chunkFixtureLimits(), ProgressBudget{Steps: 1, SourcePages: 1}); e != nil {
				t.Fatal(e)
			}
			progressUntil(t, s, dir, "summary-raw")
			head, p := progressAt(t, dir)
			session, e := loadSession(dir)
			if e != nil {
				t.Fatal(e)
			}
			store := progressStore{dir}
			switch kind {
			case "raw-tamper":
				item, ok, e := store.next(context.Background(), p.RawRoot, "")
				if e != nil || !ok {
					t.Fatal("raw probe setup")
				}
				var m progressPage
				if DecodeStrict(item.Value, &m) != nil {
					t.Fatal("raw metadata setup")
				}
				path := filepath.Join(dir, m.File)
				b, e := os.ReadFile(path)
				if e != nil || os.WriteFile(path, append(b, ' '), 0600) != nil {
					t.Fatal("raw tamper setup")
				}
			case "index-tamper":
				path := store.objectPath("index", p.RowsRoot)
				b, e := os.ReadFile(path)
				if e != nil || os.WriteFile(path, append(b, ' '), 0600) != nil {
					t.Fatal("index tamper setup")
				}
			case "valid-hash-foreign-reference":
				item, ok, e := store.next(context.Background(), p.RowsRoot, "")
				if e != nil || !ok {
					t.Fatal("reference setup")
				}
				var row progressRow
				if DecodeStrict(item.Value, &row) != nil {
					t.Fatal("row setup")
				}
				row.Ref.MetaSHA = strings.Repeat("0", 64)
				root, e := store.put(context.Background(), p.RowsRoot, item.Key, row)
				if e != nil {
					t.Fatal(e)
				}
				p.RowsRoot = root
				if _, e := commitProgress(dir, session, head, p); e != nil {
					t.Fatal("self-consistent forged index setup")
				}
			case "restart":
				s.id.MongoProcess = strings.Repeat("d", 24)
			case "cancelled":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if _, e := ResumeResumableCapture(ctx, s, s.id.Binding, dir, ProgressBudget{Steps: 1, SourcePages: 1}); !errors.Is(e, context.Canceled) {
					t.Fatal("cancelled progress accepted")
				}
				return
			case "partial-head":
				if os.WriteFile(filepath.Join(dir, "progress-head.json"), []byte("{"), 0600) != nil {
					t.Fatal("partial head setup")
				}
			}
			if _, e := ResumeResumableCapture(context.Background(), s, s.id.Binding, dir, ProgressBudget{Steps: 1024, SourcePages: 1}); e == nil {
				t.Fatal("tampered/unbound/restarted input became completed observation")
			}
		})
	}
}

// The following helpers exist only in this CI observation overlay. They publish
// immutable scalar snapshots; the timer never reads progress files or live state.
type progressLargeDiagnosticSnapshot struct {
	Phase           string `json:"phase"`
	Work            int64  `json:"work"`
	RawPages        int64  `json:"rawPages"`
	IndexedRows     int64  `json:"indexedRows"`
	VerifiedRows    int64  `json:"verifiedRows"`
	ReducedRaw      int64  `json:"reducedRaw"`
	ReducedSeen     int64  `json:"reducedSeen"`
	ReducedRows     int64  `json:"reducedRows"`
	ReducedAtWork   int64  `json:"reducedAtWork"`
	ResumeCalls     uint64 `json:"resumeCalls"`
	ResumeReturns   uint64 `json:"resumeReturns"`
	InFlight        bool   `json:"inFlight"`
	InFlightSinceMS int64  `json:"inFlightSinceMS"`
	LastReturnMS    int64  `json:"lastReturnMS"`
}

type progressLargeDiagnosticRecord struct {
	Version                    int                             `json:"v"`
	Mark                       string                          `json:"mark"`
	ElapsedMS                  int64                           `json:"elapsedMS"`
	DeadlineKnown              bool                            `json:"deadlineKnown"`
	OwnerReleased              bool                            `json:"ownerReleased"`
	Progress                   progressLargeDiagnosticSnapshot `json:"progress"`
	SummaryCheckedRowCompleted int64                           `json:"summaryCheckedRowCompleted"`
	IndexN                     uint64                          `json:"indexN"`
	IndexBytes                 uint64                          `json:"indexBytes"`
	IndexOK                    uint64                          `json:"indexOK"`
	IndexErr                   uint64                          `json:"indexErr"`
	PageColdN                  uint64                          `json:"pageColdN"`
	PageColdBytes              uint64                          `json:"pageColdBytes"`
	PageColdOK                 uint64                          `json:"pageColdOK"`
	PageColdErr                uint64                          `json:"pageColdErr"`
}

type progressLargeDiagnostic struct {
	t             *testing.T
	start         time.Time
	decode        *progressDecodeDiagnostic
	latest        atomic.Pointer[progressLargeDiagnosticSnapshot]
	mu            sync.Mutex
	timer         *time.Timer
	timerDone     chan struct{}
	closed        bool
	ordinary      int
	lastPhase     string
	lastWork      int64
	deadlineKnown bool
	ownerReleased bool
}

func startProgressLargeDiagnostic(t *testing.T) *progressLargeDiagnostic {
	d := &progressLargeDiagnostic{t: t, start: time.Now(), decode: &progressDecodeDiagnostic{}}
	d.latest.Store(&progressLargeDiagnosticSnapshot{Phase: "initial"})
	if !progressDecodeDiagnosticOwner.CompareAndSwap(nil, d.decode) {
		t.Fatal("diagnostic counter owner already installed")
	}
	deadline, ok := t.Deadline()
	d.deadlineKnown = ok
	d.emit("start")
	if ok {
		delay := time.Until(deadline) - 30*time.Second
		if delay < 0 {
			delay = 0
		}
		d.timerDone = make(chan struct{})
		d.timer = time.AfterFunc(delay, func() {
			defer close(d.timerDone)
			d.emit("pre_timeout")
		})
	} else {
		// An unavailable deadline is visible, never replaced by a longer timeout.
		d.emit("pre_timeout_unavailable")
	}
	return d
}

func progressLargeDiagnosticPhase(phase string) string {
	switch phase {
	case "rebuild", "capture", "summary-raw", "summary-seen", "summary-rows", "summary-origins", "complete":
		return phase
	default:
		return "unknown"
	}
}

func (d *progressLargeDiagnostic) beginCall(resume bool) {
	next := *d.latest.Load()
	next.InFlight = true
	next.InFlightSinceMS = time.Since(d.start).Milliseconds()
	if resume {
		next.ResumeCalls++
	}
	d.latest.Store(&next)
}

func (d *progressLargeDiagnostic) returned(out ResumableSummary, resume bool) {
	next := *d.latest.Load()
	next.Phase = progressLargeDiagnosticPhase(out.Phase)
	next.Work = out.WorkSequence
	next.RawPages = out.RawPages
	next.IndexedRows = out.IndexedRows
	next.VerifiedRows = out.VerifiedRows
	next.InFlight = false
	next.LastReturnMS = time.Since(d.start).Milliseconds()
	if resume {
		next.ResumeReturns++
	}
	d.latest.Store(&next)
}

func (d *progressLargeDiagnostic) reduced(state progressState, work int64) {
	next := *d.latest.Load()
	next.ReducedRaw = state.ReducedRaw
	next.ReducedSeen = state.ReducedSeen
	next.ReducedRows = state.ReducedRows
	next.ReducedAtWork = work
	d.latest.Store(&next)
}

func (d *progressLargeDiagnostic) emit(mark string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.closed {
		d.emitLocked(mark)
	}
}

func (d *progressLargeDiagnostic) emitLocked(mark string) {
	current := *d.latest.Load()
	if mark == "progress" {
		if d.ordinary >= 60 || current.Phase == d.lastPhase && current.Work-d.lastWork < 2048 {
			return
		}
		d.ordinary++
		d.lastPhase, d.lastWork = current.Phase, current.Work
	}
	// Each scalar counter is atomic; a live snapshot can span an in-flight call.
	// Success means typed decode only; outer prefix/page validation is unchanged.
	record := progressLargeDiagnosticRecord{
		Version: 1, Mark: mark, ElapsedMS: time.Since(d.start).Milliseconds(),
		DeadlineKnown: d.deadlineKnown, OwnerReleased: d.ownerReleased, Progress: current,
		SummaryCheckedRowCompleted: current.ReducedSeen + current.ReducedRows,
		IndexN:                     d.decode.indexN.Load(), IndexBytes: d.decode.indexBytes.Load(),
		IndexOK: d.decode.indexOK.Load(), IndexErr: d.decode.indexErr.Load(),
		PageColdN: d.decode.pageColdN.Load(), PageColdBytes: d.decode.pageColdBytes.Load(),
		PageColdOK: d.decode.pageColdOK.Load(), PageColdErr: d.decode.pageColdErr.Load(),
	}
	body, err := json.Marshal(record)
	if err != nil || len(body) > 2048 {
		d.t.Log("NAMESPACE_PROGRESS_DIAG_V1 {\"v\":1,\"mark\":\"invalid_record\"}")
		return
	}
	d.t.Logf("NAMESPACE_PROGRESS_DIAG_V1 %s", body)
}

func (d *progressLargeDiagnostic) close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.ownerReleased = progressDecodeDiagnosticOwner.CompareAndSwap(d.decode, nil)
	d.emitLocked("final")
	d.closed = true
	d.mu.Unlock()
	if d.timer != nil {
		if d.timer.Stop() {
			close(d.timerDone)
		}
		// A running callback sees closed under mu and must return before teardown.
		<-d.timerDone
	}
}

func TestNamespaceProgressLargeHistoryHasNoFinalFullSweep(t *testing.T) {
	diagnostic := startProgressLargeDiagnostic(t)
	defer diagnostic.close()
	s := &pagedFixture{id: fixtureIdentity(), rows: map[string][]MongoRow{}}
	for n := 0; n < 10001; n++ {
		s.rows["group_member"] = append(s.rows["group_member"], rawRow(t, "group_member", fmt.Sprint(n), bson.M{"_id": n, "group_id": "synthetic-g", "user_id": "synthetic-u"}))
	}
	dir := chunkDir(t)
	budget := ProgressBudget{Steps: 32, SourcePages: 1}
	diagnostic.beginCall(false)
	out, e := BeginResumableCapture(context.Background(), s, s.id.Binding, dir, defaultPageLimits(), budget)
	diagnostic.returned(out, false)
	if e != nil {
		t.Fatal(e)
	}
	diagnostic.emit("progress")
	summaryCalls := 0
	last := out
	for n := 0; n < 10001*3; n++ {
		diagnostic.beginCall(true)
		out, e = ResumeResumableCapture(context.Background(), s, s.id.Binding, dir, budget)
		diagnostic.returned(out, true)
		if e != nil {
			t.Fatal("bounded history progress failed", e)
		}
		if out.WorkSequence-last.WorkSequence > int64(budget.Steps) || out.WorkSequence <= last.WorkSequence {
			t.Fatal("a bounded invocation reset or exceeded its quota")
		}
		if strings.HasPrefix(out.Phase, "summary-") {
			summaryCalls++
			_, state := progressAt(t, dir)
			_, old := progressAtForHead(t, dir, last.CheckpointSHA)
			if state.ReducedRows < old.ReducedRows || state.ReducedSeen < old.ReducedSeen || state.ReducedRaw < old.ReducedRaw {
				t.Fatal("final reducer restarted history")
			}
			diagnostic.reduced(state, out.WorkSequence)
		}
		diagnostic.emit("progress")
		last = out
		if out.RecordedTwoPassComplete {
			break
		}
	}
	if !out.RecordedTwoPassComplete || out.RecordedSummary.MongoRows != int64(10001+len(collections)) || summaryCalls < 100 || out.State != State {
		t.Fatal("large history was capped or finalized through a hidden whole-history operation")
	}
}
func progressAtForHead(t *testing.T, dir, h string) (chunkSession, progressState) {
	t.Helper()
	s, e := loadSession(dir)
	if e != nil {
		t.Fatal(e)
	}
	var p progressState
	if e := (progressStore{dir}).load("state", h, 128<<10, &p); e != nil {
		t.Fatal(e)
	}
	return s, p
}
