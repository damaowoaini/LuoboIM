package messagedelivery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
)

// This adapter counts the REAL concrete pager and original RS-derived proof
// reads. Its capture tokens/seal are test-only. The independent ownedcontrol
// FS test exercises the actual production materializer/snapshot binding.
// No always-true permission verifier, dummy pager or duplicate algorithm.
type countedOriginalPreparation struct {
	rows                                          originalOwnedSnapshot
	binding                                       namespaceimport.OriginalHistoryPreparationBinding
	watched                                       string
	candidateReads, sequenceReads, sourceRechecks int
	sealed                                        string
}

func (r *countedOriginalPreparation) ReadOriginalHistoryRecord(ctx context.Context, collection, id string) ([]byte, namespaceimport.SourceRef, error) {
	if collection == Sequences && id == r.watched {
		r.candidateReads++
	}
	return r.rows.ReadOriginalHistoryRecord(ctx, collection, id)
}
func (r *countedOriginalPreparation) ReadOriginalHistorySourceAtSequence(ctx context.Context, cid string, seq int64) ([]byte, namespaceimport.SourceRef, error) {
	r.sequenceReads++
	return r.rows.ReadOriginalHistorySourceAtSequence(ctx, cid, seq)
}
func (r *countedOriginalPreparation) ReadOriginalHistoryMessageDocument(ctx context.Context, id string) ([]byte, namespaceimport.SourceRef, error) {
	return r.rows.ReadOriginalHistoryMessageDocument(ctx, id)
}
func (r *countedOriginalPreparation) OriginalHistoryPreparationBinding(ctx context.Context) (namespaceimport.OriginalHistoryPreparationBinding, error) {
	if e := ctx.Err(); e != nil {
		return namespaceimport.OriginalHistoryPreparationBinding{}, e
	}
	if directHistoryDigest(r.rows) != r.sealed {
		return namespaceimport.OriginalHistoryPreparationBinding{}, ErrConflict
	}
	return r.binding, nil
}
func (r *countedOriginalPreparation) RecheckOriginalHistoryPreparedRefs(ctx context.Context, b namespaceimport.OriginalHistoryPreparationBinding, refs []namespaceimport.SourceRef) error {
	actual, e := r.OriginalHistoryPreparationBinding(ctx)
	if e != nil {
		return e
	}
	if actual != b {
		return ErrConflict
	}
	r.sourceRechecks++
	for _, ref := range refs {
		found := false
		for collection, rows := range r.rows {
			for id := range rows {
				_, actual, e := r.rows.ReadOriginalHistoryRecord(ctx, collection, id)
				if e != nil {
					return e
				}
				if actual == ref {
					found = true
				}
			}
		}
		if !found {
			return ErrConflict
		}
	}
	return nil
}
func TestOriginalHistoryMongoPreparedPagerReusesCandidateButNeverSequencePermission(t *testing.T) {
	ctx, s, db, _, frozen, _, _ := removalVisibilityFixture(t, groupauthority.NoticeKick, true, true)
	authority, e := groupauthority.NewStore(db)
	if e != nil {
		t.Fatal(e)
	}
	admission := jointAddMember(t, ctx, authority, s, db, "reader", false, nil)
	var notice Receipt
	if e = s.transaction(ctx, func(tx context.Context) error {
		input, e := s.ownedActionInput(tx, frozen.ID)
		if e != nil {
			return e
		}
		notice, e = s.acceptOwnedAction(tx, input)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	var first AcceptedBranch
	for _, branch := range notice.Branches {
		if branch.ConversationID == "sg_g" {
			first = branch
		}
	}
	ctx = context.WithValue(ctx, constant.OpUserID, "owner")
	policy, policySHA := kafkaPolicyFixture(t, s)
	broadcast, e := s.AcceptKafkaRecord(ctx, futureGroupRecord(t, ctx, s, 5800, false), policySHA, policy)
	if e != nil {
		t.Fatal(e)
	}
	last := broadcast.Branches[0]
	if first.Seq != 1 || last.Seq != 2 || !first.Persist || first.Hidden || !last.Persist || last.Hidden {
		t.Fatal("actual target-only source then ordinary broadcast required")
	}
	original := originalOwnedCapture(t, ctx, db)
	id := historyIntervalID(s.epoch, "namespace-owner-live-history-v1", "sg_g", "reader", admission.ID)
	var live ownerHistoryInterval
	if bson.Unmarshal(original[Sequences][id], &live) != nil {
		t.Fatal("real admitted generation")
	}
	// Reviewed OLD producer representation of the actual second receipt only.
	// It cannot grant seq1; the concrete per-seq source check must still decide.
	live.Upper = last.Seq
	live.Revision = 3
	live.SourceID = broadcast.SourceID
	live.BranchID = last.ID
	live.InputSHA = broadcast.InputSHA256
	live.ReceiptSHA = directHistoryDigest(broadcast)
	original[Sequences][id], e = bson.Marshal(live)
	if e != nil {
		t.Fatal(e)
	}
	fixture := func() (*OriginalHistoryVerifier, namespaceimport.Header, namespaceimport.Inventory, *countedOriginalPreparation) {
		v, h, i := originalTestVerifier(t, ctx, s)
		rows := originalOwnedSnapshot{}
		for collection, values := range original {
			rows[collection] = map[string]bson.Raw{}
			for key, raw := range values {
				rows[collection][key] = append(bson.Raw(nil), raw...)
			}
		}
		r := &countedOriginalPreparation{rows: rows, watched: id}
		r.binding = namespaceimport.OriginalHistoryPreparationBinding{Capture: r, Snapshot: &struct{ Value int }{1}, SnapshotSHA256: hash([]byte("test-only captured rows")), RawRootSHA256: i.RawRootSHA256, HeaderSHA256: h.SHA256(), LeaseID: i.LeaseID, HeldGuardSHA256: hash([]byte("test-only exclusion")), MetadataSHA256: hash([]byte("test-only complete seal"))}
		r.sealed = directHistoryDigest(rows)
		return v, h, i, r
	}
	t.Run("two-pages-and-verifier-build-once-still-check-three-sequences", func(t *testing.T) {
		v, h, i, r := fixture()
		first, e := v.DeriveOriginalHistorySegmentPage(ctx, h, i, s.epoch, live.Kind, id, r, "")
		if e != nil || first.Complete || first.Next == "" || len(first.Segments) != 0 || len(first.Pending) != 0 {
			t.Fatal("non-target notice was granted or pager stopped", e)
		}
		second, e := v.DeriveOriginalHistorySegmentPage(ctx, h, i, s.epoch, live.Kind, id, r, first.Next)
		if e != nil || !second.Complete || len(second.Segments) != 1 || second.Segments[0].Lower != 2 || second.Segments[0].Upper != 2 {
			t.Fatal("independent broadcast lost", second, e)
		}
		if e = v.VerifyOriginalHistorySegment(ctx, h, i, second.Segments[0], r); e != nil {
			t.Fatal(e)
		}
		if r.candidateReads != 1 || r.sequenceReads != 3 || r.sourceRechecks != 3 {
			t.Fatal("candidate preparation or per-seq verification count differs", r.candidateReads, r.sequenceReads, r.sourceRechecks)
		}
		second.Segments[0].Evidence[0].RecordSHA256 = hash([]byte("caller mutation"))
		again, e := v.DeriveOriginalHistorySegmentPage(ctx, h, i, s.epoch, live.Kind, id, r, first.Next)
		if e != nil || again.Segments[0].Evidence[0].RecordSHA256 == second.Segments[0].Evidence[0].RecordSHA256 {
			t.Fatal("caller mutated private prepared candidate", e)
		}
	})
	t.Run("missing-first-slot-pending-does-not-erase-next-broadcast", func(t *testing.T) {
		v, h, i, r := fixture()
		delete(r.rows[Effects], key(s.epoch, "branch", first.ID))
		r.sealed = directHistoryDigest(r.rows)
		a, e := v.DeriveOriginalHistorySegmentPage(ctx, h, i, s.epoch, live.Kind, id, r, "")
		if e != nil || len(a.Pending) != 1 || a.Pending[0].Sequence != 1 || a.Pending[0].Reason != "original-slot-scope-unavailable" {
			t.Fatal("unknown original slot hidden", a, e)
		}
		b, e := v.DeriveOriginalHistorySegmentPage(ctx, h, i, s.epoch, live.Kind, id, r, a.Next)
		if e != nil || !b.Complete || len(b.Segments) != 1 || b.Segments[0].Lower != 2 || r.candidateReads != 1 || r.sequenceReads != 2 {
			t.Fatal("pending gap or candidate repeated", b, e)
		}
	})
	t.Run("changed-final-inventory-and-new-reader-rebuild-original-chain", func(t *testing.T) {
		v, h, i, r := fixture()
		a, e := v.DeriveOriginalHistorySegmentPage(ctx, h, i, s.epoch, live.Kind, id, r, "")
		if e != nil {
			t.Fatal(e)
		}
		other := i
		other.PageCount = 1 // binding change only, not a signed Inventory proof
		if _, e = v.DeriveOriginalHistorySegmentPage(ctx, h, other, s.epoch, live.Kind, id, r, a.Next); e != nil || r.candidateReads != 2 {
			t.Fatal("final Inventory reused capture-stage candidate proof", e, r.candidateReads)
		}
		copy := *r
		copy.candidateReads = 0
		copy.binding.Capture = &copy
		if _, e = v.DeriveOriginalHistorySegmentPage(ctx, h, other, s.epoch, live.Kind, id, &copy, a.Next); e != nil || copy.candidateReads != 1 {
			t.Fatal("different original reader borrowed prepared proof", e, copy.candidateReads)
		}
	})
	t.Run("changed-seal-metadata-forces-full-candidate-rebuild", func(t *testing.T) {
		v, h, i, r := fixture()
		a, e := v.DeriveOriginalHistorySegmentPage(ctx, h, i, s.epoch, live.Kind, id, r, "")
		if e != nil {
			t.Fatal(e)
		}
		r.binding.MetadataSHA256 = hash([]byte("different immutable capture metadata"))
		if _, e = v.DeriveOriginalHistorySegmentPage(ctx, h, i, s.epoch, live.Kind, id, r, a.Next); e != nil || r.candidateReads != 2 {
			t.Fatal("different source binding reused prepared candidate", e, r.candidateReads)
		}
	})
	t.Run("changed-source-ref-or-trust-bytes-refuse-before-next-sequence", func(t *testing.T) {
		for _, change := range []string{"raw", "trust"} {
			t.Run(change, func(t *testing.T) {
				v, h, i, r := fixture()
				a, e := v.DeriveOriginalHistorySegmentPage(ctx, h, i, s.epoch, live.Kind, id, r, "")
				if e != nil {
					t.Fatal(e)
				}
				if change == "trust" {
					body, e := os.ReadFile(v.path)
					if e != nil {
						t.Fatal(e)
					}
					if e = os.WriteFile(v.path, append(body, ' '), 0600); e != nil {
						t.Fatal(e)
					}
				} else {
					r.rows[Effects][key(s.epoch, "branch", last.ID)] = append(r.rows[Effects][key(s.epoch, "branch", last.ID)], 0)
				}
				if _, e = v.DeriveOriginalHistorySegmentPage(ctx, h, i, s.epoch, live.Kind, id, r, a.Next); e == nil || r.sequenceReads != 1 {
					t.Fatal("changed source continued a prepared sequence", e, r.sequenceReads)
				}
			})
		}
	})
	t.Run("private-boundary-witness-bytes-are-also-rechecked", func(t *testing.T) {
		v, h, _, r := fixture()
		path := filepath.Join(originalHistoryPrivateFixtureDir(t), "original-witness.json")
		if e = os.WriteFile(path, []byte("{}"), 0600); e != nil {
			t.Fatal(e)
		}
		build := &originalHistoryPreparation{}
		chain := &originalHistoryChain{preparation: build}
		if _, e = chain.readPrivateProof(path, 1<<20); e != nil {
			t.Fatal(e)
		}
		entry := originalPreparedHistory{binding: r.binding, files: []originalPrivateProof{build.files[path]}}
		if e = v.recheckOriginalPreparation(ctx, h, r, entry); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, []byte("{ }"), 0600); e != nil {
			t.Fatal(e)
		}
		if e = v.recheckOriginalPreparation(ctx, h, r, entry); e == nil {
			t.Fatal("changed private witness skipped original bytes")
		}
	})
	t.Run("nonprepared-reader-remains-original-uncached-contract", func(t *testing.T) {
		v, h, i, r := fixture()
		a, e := v.DeriveOriginalHistorySegmentPage(ctx, h, i, s.epoch, live.Kind, id, r.rows, "")
		if e != nil {
			t.Fatal(e)
		}
		b, e := v.DeriveOriginalHistorySegmentPage(ctx, h, i, s.epoch, live.Kind, id, r.rows, a.Next)
		if e != nil || !b.Complete || len(b.Segments) != 1 || b.Segments[0].Lower != 2 {
			t.Fatal("old source reader behavior changed", e)
		}
		raw, e := json.Marshal(v.prepared)
		if e != nil || string(raw) != "null" {
			t.Fatal("nonprepared source entered cache", e)
		}
	})
}
