package messagedelivery

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
)

// Actual nonce-owned replica-set fixture and real private group command,
// Reserve/Accept, policy, receipt/effect, actual source trust and concrete
// producer/verifier. No fake cutover/closure, deleted marker, always-true
// validator, later roster grant or synthetic positive ACK is used.
func TestOriginalGroupHistoryMongoFiniteRangeCannotLaunderTargetedNotice(t *testing.T) {
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
	var noticeBranch AcceptedBranch
	for _, branch := range notice.Branches {
		if branch.ConversationID == "sg_g" {
			noticeBranch = branch
		}
	}
	if noticeBranch.Seq != 1 || !noticeBranch.Persist || noticeBranch.Hidden {
		t.Fatal("actual first visible target-only notice required", noticeBranch)
	}
	ctx = context.WithValue(ctx, constant.OpUserID, "owner")
	policy, policySHA := kafkaPolicyFixture(t, s)
	broadcast, e := s.AcceptKafkaRecord(ctx, futureGroupRecord(t, ctx, s, 5600, false), policySHA, policy)
	if e != nil {
		t.Fatal(e)
	}
	last := broadcast.Branches[0]
	if last.ConversationID != "sg_g" || last.Seq != 2 || !last.Persist || last.Hidden {
		t.Fatal("actual later ordinary broadcast required", last)
	}
	verifier, h, i := originalTestVerifier(t, ctx, s)
	original := originalOwnedCapture(t, ctx, db)
	id := historyIntervalID(s.epoch, "namespace-owner-live-history-v1", "sg_g", "reader", admission.ID)
	var live ownerHistoryInterval
	if bson.Unmarshal(original[Sequences][id], &live) != nil {
		t.Fatal("real admitted generation")
	}
	// Exact old storage-version representation of the ACTUAL endpoint. This
	// changes only a private capture copy's progress metadata, not authority or
	// the DB, and keeps all original scopes, required_from and command proofs.
	// The old producer's two actual source updates would leave revision3/Upper2.
	live.Upper = last.Seq
	live.Revision = 3
	live.SourceID = broadcast.SourceID
	live.BranchID = last.ID
	live.InputSHA = broadcast.InputSHA256
	live.ReceiptSHA = directHistoryDigest(broadcast)
	encoded, e := bson.Marshal(live)
	if e != nil {
		t.Fatal(e)
	}
	original[Sequences][id] = encoded
	clone := func() originalOwnedSnapshot {
		out := originalOwnedSnapshot{}
		for collection, rows := range original {
			out[collection] = map[string]bson.Raw{}
			for key, raw := range rows {
				out[collection][key] = append(bson.Raw(nil), raw...)
			}
		}
		return out
	}
	t.Run("concrete-finite-range-keeps-broadcast-and-excludes-nontarget-notice", func(t *testing.T) {
		snapshot := clone()
		rows, e := verifier.DeriveOriginalHistorySegments(ctx, h, i, s.epoch, "namespace-owner-live-history-v1", id, snapshot)
		if e != nil || len(rows) != 1 || rows[0].Lower != 2 || rows[0].Upper != 2 || rows[0].ScopeContract != namespaceimport.OriginalGroupSequenceScopeV1 {
			t.Fatal("last receipt laundered restricted seq1 into a generic [1,2] grant", rows, e)
		}
		if e = verifier.VerifyOriginalHistorySegment(ctx, h, i, rows[0], snapshot); e != nil {
			t.Fatal("real broadcast lost source proof", e)
		}
		forged := rows[0]
		forged.Lower = 1
		if e = verifier.VerifyOriginalHistorySegment(ctx, h, i, forged, snapshot); e == nil {
			t.Fatal("a later signature could enroll restricted seq1")
		}
		unscoped := rows[0]
		unscoped.ScopeContract = ""
		if e = verifier.VerifyOriginalHistorySegment(ctx, h, i, unscoped, snapshot); e == nil {
			t.Fatal("a dynamic range without proven source scope became a body grant")
		}
	})
	t.Run("real-retained-envelope-and-completed-broadcast-remain-recoverable", func(t *testing.T) {
		snapshot := clone()
		trust, e := verifier.checkTrust(h, s.epoch)
		if e != nil {
			t.Fatal(e)
		}
		chain := &originalHistoryChain{ctx: ctx, reader: snapshot, trust: trust, decoder: &Store{epoch: s.epoch, activationSHA: s.activationSHA}, refs: map[string]namespaceimport.SourceRef{}}
		d, e := chain.origin("sg_g")
		if e != nil {
			t.Fatal(e)
		}
		var targeted deliveryDuty
		if bson.Unmarshal(snapshot[Effects][key(s.epoch, "branch", noticeBranch.ID)], &targeted) != nil {
			t.Fatal("actual notice duty")
		}
		for _, owner := range []string{"owner", "reader"} {
			allowed, e := chain.legacyAcceptedGroupPermission(d, owner, notice, targeted, snapshot)
			if e != nil || allowed != (owner == "owner") {
				t.Fatal("original exact targets lost or widened", owner, allowed, e)
			}
		}
		var ordinary deliveryDuty
		if bson.Unmarshal(snapshot[Effects][key(s.epoch, "branch", last.ID)], &ordinary) != nil {
			t.Fatal("actual broadcast duty")
		}
		// Project/worker completion erases these real optional fields. The actual
		// original MsgDoc still has to match the immutable accepted payload SHA.
		ordinary.Payload = nil
		ordinary.Recipients = nil
		allowed, e := chain.legacyAcceptedGroupPermission(d, "reader", broadcast, ordinary, snapshot)
		if e != nil || !allowed {
			t.Fatal("recoverable completed ordinary broadcast was discarded", e)
		}
	})
	t.Run("same-count-forged-targets-cannot-pass-original-input-hash", func(t *testing.T) {
		snapshot := clone()
		key := key(s.epoch, "branch", noticeBranch.ID)
		var targeted deliveryDuty
		if bson.Unmarshal(snapshot[Effects][key], &targeted) != nil {
			t.Fatal("actual notice duty")
		}
		var fields bson.M
		if bson.Unmarshal(snapshot[Effects][key], &fields) != nil {
			t.Fatal("actual record")
		}
		fields["recipients"] = []string{"reader"}
		changed, e := bson.Marshal(fields)
		if e != nil {
			t.Fatal(e)
		}
		snapshot[Effects][key] = changed
		targeted.Recipients = []string{"reader"}
		trust, e := verifier.checkTrust(h, s.epoch)
		if e != nil {
			t.Fatal(e)
		}
		chain := &originalHistoryChain{ctx: ctx, reader: snapshot, trust: trust, decoder: &Store{epoch: s.epoch, activationSHA: s.activationSHA}, refs: map[string]namespaceimport.SourceRef{}}
		d, e := chain.origin("sg_g")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = chain.legacyAcceptedGroupPermission(d, "reader", notice, targeted, snapshot); e == nil {
			t.Fatal("same count/current member replaced frozen original target")
		}
	})
	t.Run("unresolved-original-slot-is-pending-while-next-broadcast-is-produced", func(t *testing.T) {
		snapshot := clone()
		delete(snapshot[Effects], key(s.epoch, "branch", noticeBranch.ID))
		cursor := ""
		known := []namespaceimport.OwnerHistorySegment{}
		missing := []namespaceimport.OriginalHistorySequencePending{}
		for {
			page, e := verifier.DeriveOriginalHistorySegmentPage(ctx, h, i, s.epoch, "namespace-owner-live-history-v1", id, snapshot, cursor)
			if e != nil {
				t.Fatal(e)
			}
			known = append(known, page.Segments...)
			missing = append(missing, page.Pending...)
			if page.Complete {
				break
			}
			if page.Next == "" || page.Next == cursor {
				t.Fatal("bounded original sequence cursor did not advance")
			}
			cursor = page.Next
		}
		if len(known) != 1 || known[0].Lower != 2 || known[0].Upper != 2 || known[0].ScopeContract != namespaceimport.OriginalGroupSequenceScopeV1 || len(missing) != 1 || missing[0].Sequence != 1 || missing[0].Reason != "original-slot-scope-unavailable" || len(missing[0].Evidence) == 0 {
			t.Fatal("unknown slot disappeared or erased independent known history", known, missing)
		}
		_, e := verifier.DeriveOriginalHistorySegments(ctx, h, i, s.epoch, "namespace-owner-live-history-v1", id, snapshot)
		var pending *namespaceimport.OriginalHistoryProofPending
		if !errors.As(e, &pending) {
			t.Fatal("legacy nonpaged caller silently treated unknown as empty/granted", e)
		}
	})
}
