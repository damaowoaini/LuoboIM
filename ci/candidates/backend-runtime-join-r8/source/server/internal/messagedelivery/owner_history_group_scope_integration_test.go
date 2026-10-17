package messagedelivery

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func groupHistoryStaticRows(t *testing.T, ctx context.Context, db *mongo.Database) map[string]string {
	t.Helper()
	cursor, e := db.Collection(Sequences).Find(ctx, bson.M{"kind": bson.M{"$in": []string{"namespace-owner-live-history-v1", "namespace-owner-prospective-v1"}}})
	if e != nil {
		t.Fatal(e)
	}
	defer cursor.Close(ctx)
	rows := map[string]string{}
	for cursor.Next(ctx) {
		raw := cursor.Current
		id, ok := raw.Lookup("_id").StringValueOK()
		if !ok {
			t.Fatal("actual history identity")
		}
		rows[id] = string(raw)
	}
	if e = cursor.Err(); e != nil {
		t.Fatal(e)
	}
	return rows
}

func TestGroupHistoryMongoBroadcastDoesNotRewriteMemberProgress(t *testing.T) {
	ctx, authority, base, db, groups, members := originMongoFixture(t, nil, nil)
	info, created, e := applyNewGroupSeed(ctx, authority, base, groups, members, "g", nil)
	if e != nil {
		t.Fatal(e)
	}
	prepareCreatedMembershipFixture(t, ctx, base, db, info, created)
	s, e := NewMembershipBoundStore(db, base.epoch, base.activationSHA)
	if e != nil {
		t.Fatal(e)
	}
	ctx = context.WithValue(ctx, constant.OpUserID, "owner")
	before := groupHistoryStaticRows(t, ctx, db)
	if len(before) != 4 {
		t.Fatal("both actual initial owners and independent sg/n generations required", len(before))
	}
	policy, policySHA := kafkaPolicyFixture(t, s)
	for i := int64(0); i < 3; i++ {
		receipt, e := s.AcceptKafkaRecord(ctx, futureGroupRecord(t, ctx, s, 4800+i, false), policySHA, policy)
		if e != nil || receipt.Branches[0].Seq != i+1 {
			t.Fatal("actual positive source", receipt, e)
		}
	}
	after := groupHistoryStaticRows(t, ctx, db)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("each message rewrote per-member generation/prospective progress")
	}
	reader := &HistoryWindowReader{store: s}
	for _, owner := range []string{"owner", "member"} {
		allowed, e := reader.FilterNamespaceHistorySequences(ctx, "sg_g", owner, []int64{1, 2, 3})
		if e != nil || !allowed[1] || !allowed[2] || !allowed[3] {
			t.Fatal("real broadcasts or sender-sync=false lost history", owner, allowed, e)
		}
	}
}

func TestGroupHistoryMongoNoticeScopeSurvivesLaterBroadcastAndFutureAdmission(t *testing.T) {
	ctx, s, db, _, frozen, _, _ := removalVisibilityFixture(t, groupauthority.NoticeKick, true, true)
	authority, e := groupauthority.NewStore(db)
	if e != nil {
		t.Fatal(e)
	}
	// The original notice already froze only owner for visible sg, and
	// owner+removed member for n. This new member is current at ACCEPT time.
	jointAddMember(t, ctx, authority, s, db, "reader", false, nil)
	var notice Receipt
	if e = s.transaction(ctx, func(tx context.Context) error {
		input, e := s.ownedActionInput(tx, frozen.ID)
		if e != nil {
			return e
		}
		notice, e = s.acceptOwnedAction(tx, input)
		return e
	}); e != nil {
		t.Fatal("actual ordered source receipt", e)
	}
	reader := &HistoryWindowReader{store: s}
	noticeSeq := map[string]int64{}
	for _, branch := range notice.Branches {
		if !branch.Persist || branch.Hidden || branch.Seq < 1 {
			t.Fatal("actual persisted fixture branches required", branch)
		}
		noticeSeq[branch.ConversationID] = branch.Seq
		for _, owner := range []string{"reader", "owner"} {
			allowed, e := reader.FilterNamespaceHistorySequences(ctx, branch.ConversationID, owner, []int64{branch.Seq})
			if e != nil || allowed[branch.Seq] != (owner == "owner") {
				t.Fatal("frozen target scope replaced by accept-time roster", owner, branch, allowed, e)
			}
		}
	}
	if len(noticeSeq) != 2 {
		t.Fatal("independent real sg/n notice branches required")
	}
	policy, policySHA := kafkaPolicyFixture(t, s)
	later, e := s.AcceptKafkaRecord(context.WithValue(ctx, constant.OpUserID, "owner"), futureGroupRecord(t, context.WithValue(ctx, constant.OpUserID, "owner"), s, 4900, false), policySHA, policy)
	if e != nil {
		t.Fatal(e)
	}
	actual := later.Branches[0]
	if actual.ConversationID != "sg_g" || actual.Seq <= noticeSeq["sg_g"] {
		t.Fatal("actual later broadcast")
	}
	// No blacklist of the old roster could protect this future member. Its
	// actual forbidHistory=false admission grants ordinary old history, while
	// the original positive notice ACL still excludes it.
	jointAddMember(t, ctx, authority, s, db, "future", false, nil)
	for _, owner := range []string{"reader", "future"} {
		allowed, e := reader.FilterNamespaceHistorySequences(ctx, "sg_g", owner, []int64{noticeSeq["sg_g"], actual.Seq})
		if e != nil || allowed[noticeSeq["sg_g"]] || !allowed[actual.Seq] {
			t.Fatal("later broadcast/admission filled restricted notice hole", owner, allowed, e)
		}
		allowed, e = reader.FilterNamespaceHistorySequences(ctx, "n_g", owner, []int64{noticeSeq["n_g"]})
		if e != nil || allowed[noticeSeq["n_g"]] {
			t.Fatal("independent notification scope widened", owner, allowed, e)
		}
	}
}

func TestGroupHistoryMongoOriginalGenerationExportsClosedRangeWithoutLeaveGap(t *testing.T) {
	ctx, authority, base, db, groups, members := originMongoFixture(t, nil, nil)
	info, created, e := applyNewGroupSeed(ctx, authority, base, groups, members, "g", nil)
	if e != nil {
		t.Fatal(e)
	}
	prepareCreatedMembershipFixture(t, ctx, base, db, info, created)
	s, e := NewMembershipBoundStore(db, base.epoch, base.activationSHA)
	if e != nil {
		t.Fatal(e)
	}
	ctx = context.WithValue(ctx, constant.OpUserID, "owner")
	policy, policySHA := kafkaPolicyFixture(t, s)
	if _, e = s.AcceptKafkaRecord(ctx, futureGroupRecord(t, ctx, s, 5000, true), policySHA, policy); e != nil {
		t.Fatal(e)
	}
	if e = jointRemoveMember(ctx, authority, s, db, "member"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcceptKafkaRecord(ctx, futureGroupRecord(t, ctx, s, 5001, true), policySHA, policy); e != nil {
		t.Fatal(e)
	}
	oldID := historyIntervalID(s.epoch, "namespace-owner-live-history-v1", "sg_g", "member", info.ID)
	v, h, inventory := originalTestVerifier(t, ctx, s)
	snapshot := originalOwnedCapture(t, ctx, db)
	rows, e := v.DeriveOriginalHistorySegments(ctx, h, inventory, s.epoch, "namespace-owner-live-history-v1", oldID, snapshot)
	if e != nil || len(rows) != 1 || rows[0].Lower != 1 || rows[0].Upper != 1 {
		t.Fatal("real removal failed to close old span before gap", rows, e)
	}
	command := jointAddMember(t, ctx, authority, s, db, "member", true, nil)
	if _, e = s.AcceptKafkaRecord(ctx, futureGroupRecord(t, ctx, s, 5002, true), policySHA, policy); e != nil {
		t.Fatal(e)
	}
	snapshot = originalOwnedCapture(t, ctx, db)
	// True forbid-history clips all old generations. It is not imported Lower
	// and must not be bypassed when exporting the otherwise valid closed span.
	rows, e = v.DeriveOriginalHistorySegments(ctx, h, inventory, s.epoch, "namespace-owner-live-history-v1", oldID, snapshot)
	if e != nil || len(rows) != 0 {
		t.Fatal("old segment resurrected after true forbid", rows, e)
	}
	currentID := historyIntervalID(s.epoch, "namespace-owner-live-history-v1", "sg_g", "member", command.ID)
	rows, e = v.DeriveOriginalHistorySegments(ctx, h, inventory, s.epoch, "namespace-owner-live-history-v1", currentID, snapshot)
	if e != nil || len(rows) != 1 || rows[0].Lower != 3 || rows[0].Upper != 3 {
		t.Fatal("new generation merged leave gap or lost actual receipt head", rows, e)
	}
	if e = v.VerifyOriginalHistorySegment(ctx, h, inventory, rows[0], snapshot); e != nil {
		t.Fatal("actual original source verifier", e)
	}
	reader := &HistoryWindowReader{store: s}
	allowed, e := reader.FilterNamespaceHistorySequences(ctx, "sg_g", "member", []int64{1, 2, 3})
	if e != nil || allowed[1] || allowed[2] || !allowed[3] {
		t.Fatal("final public reader diverged from true generation/floor", allowed, e)
	}
}

func TestGroupHistoryMongoMissingOrCopiedSlotScopeCannotBecomeLegacy(t *testing.T) {
	for _, name := range []string{"missing-scope", "copied-other-sequence-scope"} {
		t.Run(name, func(t *testing.T) {
			ctx, authority, base, db, groups, members := originMongoFixture(t, nil, nil)
			info, created, e := applyNewGroupSeed(ctx, authority, base, groups, members, "g", nil)
			if e != nil {
				t.Fatal(e)
			}
			prepareCreatedMembershipFixture(t, ctx, base, db, info, created)
			s, e := NewMembershipBoundStore(db, base.epoch, base.activationSHA)
			if e != nil {
				t.Fatal(e)
			}
			ctx = context.WithValue(ctx, constant.OpUserID, "owner")
			policy, policySHA := kafkaPolicyFixture(t, s)
			for i := int64(0); i < 2; i++ {
				if _, e = s.AcceptKafkaRecord(ctx, futureGroupRecord(t, ctx, s, 5100+i, true), policySHA, policy); e != nil {
					t.Fatal(e)
				}
			}
			model := table.MsgDocModel{}
			first, second := fmt.Sprintf("msgs.%d", model.GetMsgIndex(1)), fmt.Sprintf("msgs.%d", model.GetMsgIndex(2))
			update := bson.M{"$unset": bson.M{second + ".history_scope_id": "", second + ".history_scope_sha256": ""}}
			if name == "copied-other-sequence-scope" {
				var raw bson.Raw
				if e = db.Collection(model.TableName()).FindOne(ctx, bson.M{"doc_id": model.GetDocID("sg_g", 1)}).Decode(&raw); e != nil {
					t.Fatal(e)
				}
				slots, e := raw.Lookup("msgs").Array().Values()
				if e != nil {
					t.Fatal(e)
				}
				slot := slots[model.GetMsgIndex(1)].Document()
				update = bson.M{"$set": bson.M{second + ".history_scope_id": slot.Lookup("history_scope_id").StringValue(), second + ".history_scope_sha256": slot.Lookup("history_scope_sha256").StringValue()}}
			}
			changed, e := db.Collection(model.TableName()).UpdateOne(ctx, bson.M{"doc_id": model.GetDocID("sg_g", 2), first + ".msg.seq": int64(1)}, update)
			if e != nil || changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
				t.Fatal("precise controlled metadata corruption", e)
			}
			reader := &HistoryWindowReader{store: s}
			if _, e = reader.FilterNamespaceHistorySequences(ctx, "sg_g", "member", []int64{2}); e == nil {
				t.Fatal("unproved positive scope authorized a physical message", name)
			}
		})
	}
}
