package namespaceinventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNamespaceProgressCheckpointBatchPreservesBudgetAndChain(t *testing.T) {
	s, dir := pageFixture(t), chunkDir(t)
	first, e := BeginResumableCapture(context.Background(), s, s.id.Binding, dir, chunkFixtureLimits(), ProgressBudget{Steps: 1, SourcePages: 1})
	if e != nil {
		t.Fatal(e)
	}
	old, _ := progressAt(t, dir)
	out, e := ResumeResumableCapture(context.Background(), s, s.id.Binding, dir, ProgressBudget{Steps: 32, SourcePages: 1})
	if e != nil {
		t.Fatal(e)
	}
	head, p := progressAt(t, dir)
	logical := out.WorkSequence - first.WorkSequence
	if logical <= 1 || logical > 32 || head.Sequence-old.Sequence >= logical || progressWorkSequence(p) != out.WorkSequence {
		t.Fatal("work budget was changed or each quantum still published a head")
	}
	states, e := filepath.Glob(filepath.Join(dir, "state-*.json"))
	if e != nil || int64(len(states)) != head.Sequence {
		t.Fatal("physical immutable checkpoint count differs from its chain")
	}
	var previous progressState
	if e := (progressStore{dir}).load("state", p.Previous, 128<<10, &previous); e != nil || previous.Sequence+1 != p.Sequence || progressWorkSequence(p)-progressWorkSequence(previous) > 32 {
		t.Fatal("batched head lost the preceding immutable checkpoint", e)
	}
	inspected, e := InspectResumableProgress(dir)
	if e != nil || !reflect.DeepEqual(inspected, out) || out.CurrentFullCustodyReverified {
		t.Fatal("offline inspection changed observation or claimed custody", e)
	}
	complete := completeProgress(t, s, dir)
	if !complete.RecordedTwoPassComplete || complete.RecordedSummary.CandidateCollisions != 2 || complete.RecordedSummary.ErasedSlots != 1 {
		t.Fatal("batched reducers changed their original evidence")
	}
}

func TestNamespaceProgressBatchKeepsUnpublishedStateUntrusted(t *testing.T) {
	for _, kind := range []string{"discard", "bounded-spill", "blocked-private-object", "CID-origin-closure"} {
		t.Run(kind, func(t *testing.T) {
			s, dir := pageFixture(t), chunkDir(t)
			before, e := BeginResumableCapture(context.Background(), s, s.id.Binding, dir, chunkFixtureLimits(), ProgressBudget{Steps: 1, SourcePages: 1})
			if e != nil {
				t.Fatal(e)
			}
			headBytes, e := ReadPrivateDocument(filepath.Join(dir, "progress-head.json"), 64<<10)
			if e != nil {
				t.Fatal(e)
			}
			ctx, batch := newProgressIndexContext(context.Background(), dir)
			defer batch.close()
			store := progressStore{dir}
			root := ""
			if kind == "bounded-spill" {
				// Smaller private limits exercise the real pressure-spill writer,
				// not a fake fsync or another persistence implementation.
				batch.maxBytes, batch.maxNodes = 1024, 4
			}
			for n := 0; n < 20; n++ {
				root, e = store.put(ctx, root, digest([]byte(fmt.Sprint(n))), map[string]string{"observed": strings.Repeat("synthetic", 16)})
				if e != nil || batch.bytes > batch.maxBytes || len(batch.pending) > batch.maxNodes {
					t.Fatal("bounded index spill lost valid entries", e)
				}
			}
			if kind == "bounded-spill" {
				files, _ := filepath.Glob(filepath.Join(dir, "index-*.json"))
				if len(files) == 0 {
					t.Fatal("actual immutable spill was not exercised")
				}
			}
			if kind == "blocked-private-object" {
				target := filepath.Join(t.TempDir(), "owned-sentinel")
				if os.WriteFile(target, []byte("unchanged"), 0600) != nil || os.Symlink(target, store.objectPath("index", root)) != nil {
					t.Fatal("private alias fixture failed")
				}
				if e := batch.flush(ctx, progressState{RowsRoot: root}); !errors.Is(e, ErrPrivate) {
					t.Fatal("alias was accepted by the original durable writer", e)
				}
				if raw, e := os.ReadFile(target); e != nil || string(raw) != "unchanged" {
					t.Fatal("unknown target was overwritten", e)
				}
			}
			if kind == "CID-origin-closure" {
				cid := digest([]byte("synthetic-cid"))
				key := digest([]byte("synthetic-origin"))
				originRoot, e := store.put(ctx, "", key, progressOrigin{CID: cid, Kind: "synthetic", SHA: key})
				if e != nil {
					t.Fatal(e)
				}
				cidRoot, e := store.put(ctx, "", cid, progressCID{CID: cid, Count: 1, Origins: originRoot})
				if e != nil || batch.flush(ctx, progressState{CIDRoot: cidRoot}) != nil {
					t.Fatal("CID closure was not durably materialized", e)
				}
				var stored progressCID
				var origin progressOrigin
				ok, e := store.get(context.Background(), cidRoot, cid, &stored)
				if !ok || e != nil || stored.Origins != originRoot {
					t.Fatal("persisted CID lost its origin root", e)
				}
				ok, e = store.get(context.Background(), stored.Origins, key, &origin)
				if !ok || e != nil || origin.CID != cid {
					t.Fatal("referenced origin index was pruned", e)
				}
			}
			batch.close()
			after, e := InspectResumableProgress(dir)
			if e != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("unpublished or spilled indexes enrolled new history", e)
			}
			actual, e := ReadPrivateDocument(filepath.Join(dir, "progress-head.json"), 64<<10)
			if e != nil || !bytes.Equal(actual, headBytes) {
				t.Fatal("unpublished work replaced a durable head", e)
			}
		})
	}
}

func TestNamespaceProgressCheckpointReadsLegacyAndRejectsWorkGap(t *testing.T) {
	for _, kind := range []string{"legacy-V2", "oversized-logical-gap"} {
		t.Run(kind, func(t *testing.T) {
			s, dir := pageFixture(t), chunkDir(t)
			var session chunkSession
			if kind == "legacy-V2" {
				session = chunkSession{Version: 1, ID: "00000000-0000-4000-8000-000000000001", Created: time.Now().UTC(), State: State, Identity: s.id, Limits: chunkFixtureLimits()}
				session.SHA = sessionSHA(session)
				if e := createJournal(dir, session); e != nil {
					t.Fatal(e)
				}
				p := initialProgress(session)
				p.Sequence = 1 // Historical state omits workSequence entirely.
				h, e := (progressStore{dir}).save("state", p)
				if e != nil {
					t.Fatal(e)
				}
				if _, e := writePrivateJSON(filepath.Join(dir, "progress-head.json"), progressHead{Version: 2, SessionSHA: session.SHA, StateSHA: h, Sequence: 1}); e != nil {
					t.Fatal(e)
				}
				original, _ := ReadPrivateDocument((progressStore{dir}).objectPath("state", h), 128<<10)
				if bytes.Contains(original, []byte("workSequence")) {
					t.Fatal("fixture does not reproduce the legacy JSON schema")
				}
				out, e := ResumeResumableCapture(context.Background(), s, s.id.Binding, dir, ProgressBudget{Steps: 32, SourcePages: 1})
				actual, readErr := ReadPrivateDocument((progressStore{dir}).objectPath("state", h), 128<<10)
				if e != nil || readErr != nil || out.WorkSequence <= 1 || !bytes.Equal(original, actual) {
					t.Fatal("legacy checkpoint was rejected or rewritten", e, readErr)
				}
				return
			}
			if _, e := BeginResumableCapture(context.Background(), s, s.id.Binding, dir, chunkFixtureLimits(), ProgressBudget{Steps: 1, SourcePages: 1}); e != nil {
				t.Fatal(e)
			}
			head, p := progressAt(t, dir)
			session, e := loadSession(dir)
			if e != nil {
				t.Fatal(e)
			}
			p.Previous, p.Sequence = head.StateSHA, head.Sequence+1
			p.WorkSequence += progressCheckpointSteps + 1
			h, e := (progressStore{dir}).save("state", p)
			if e != nil {
				t.Fatal(e)
			}
			forged := progressHead{Version: 2, SessionSHA: session.SHA, StateSHA: h, Sequence: p.Sequence}
			raw, e := json.Marshal(forged)
			if e != nil || os.WriteFile(filepath.Join(dir, "progress-head.json"), raw, 0600) != nil {
				t.Fatal("logical gap setup failed")
			}
			if _, _, e := loadProgress(dir, session); !errors.Is(e, ErrInvalid) {
				t.Fatal("valid-hash checkpoint skipped an unbounded amount of work", e)
			}
		})
	}
}

type progressCheckpointIdentityChange struct {
	source *pagedFixture
	calls  int
}

func (s *progressCheckpointIdentityChange) Identity(ctx context.Context) (Identity, error) {
	s.calls++
	id, e := s.source.Identity(ctx)
	if s.calls >= 6 {
		id.MongoProcess = strings.Repeat("d", 24)
	}
	return id, e
}
func (s *progressCheckpointIdentityChange) Page(ctx context.Context, c PageCursor, l Limits) (Page, error) {
	return s.source.Page(ctx, c, l)
}
func TestNamespaceProgressBatchRechecksIdentityBeforePublishing(t *testing.T) {
	s, dir := pageFixture(t), chunkDir(t)
	if _, e := BeginResumableCapture(context.Background(), s, s.id.Binding, dir, chunkFixtureLimits(), ProgressBudget{Steps: 1, SourcePages: 1}); e != nil {
		t.Fatal(e)
	}
	changed := &progressCheckpointIdentityChange{source: s}
	if _, e := ResumeResumableCapture(context.Background(), changed, s.id.Binding, dir, ProgressBudget{Steps: 32, SourcePages: 1}); !errors.Is(e, ErrIdentity) {
		t.Fatal("late identity change published staged rows", e)
	}
	_, p := progressAt(t, dir)
	if changed.calls != 6 || p.RawCount != 1 || p.RowsCount != 0 || p.Pending == nil || p.Row != 0 {
		t.Fatal("only the independently committed raw page may remain after rejection")
	}
}
