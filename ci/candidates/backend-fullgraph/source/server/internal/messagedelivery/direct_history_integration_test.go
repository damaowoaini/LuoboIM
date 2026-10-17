package messagedelivery

import (
	"context"
	"reflect"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

func directFutureFixture(t *testing.T) (context.Context, *Store, *mongo.Database) {
	t.Helper()
	ctx, s, db := ordinaryEphemeralMongoFixture(t)
	if _, e := mgo.NewConversationMongo(db); e != nil {
		t.Fatal(e)
	}
	return context.WithValue(ctx, constant.OpUserID, "owner"), s, db
}
func directFutureRecord(t *testing.T, ctx context.Context, s *Store, offset int64, history bool) KafkaRecord {
	t.Helper()
	m := namespaceMessage("owner", "peer", constant.SingleChatType, true, history)
	permit, e := s.ReserveMessageNamespaces(ctx, m)
	if e != nil || permit == nil || permit.Check(ctx, m) != nil {
		t.Fatal("actual direct pair reservation", e)
	}
	body, e := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	r := kafkaFixtureRecord(t, offset)
	r.Body = body
	r.Key = []byte(msgprocessor.GetConversationIDByMsg(m))
	return r
}
func TestDirectFutureMongoActualSourceReplayAndClearKeepOriginalAuthority(t *testing.T) {
	for _, name := range []string{"new-positive", "Seq0", "receipt-replay-after-clear", "new-positive-after-clear", "foreign-owner", "normal-notification-independent"} {
		t.Run(name, func(t *testing.T) {
			ctx, s, db := directFutureFixture(t)
			record := directFutureRecord(t, ctx, s, 3400, name != "Seq0")
			policy, policySHA := kafkaPolicyFixture(t, s)
			receipt, e := s.AcceptKafkaRecord(ctx, record, policySHA, policy)
			if e != nil {
				t.Fatal("actual private intake", e)
			}
			if len(receipt.Branches) != 1 {
				t.Fatal("fixture branch")
			}
			branch := receipt.Branches[0]
			if name == "Seq0" {
				if branch.Seq != 0 {
					t.Fatal("Seq0 allocated history")
				}
				n, e := db.Collection(Sequences).CountDocuments(ctx, bson.M{"kind": "namespace-owner-prospective-v1"})
				if e != nil || n != 0 {
					t.Fatal("Seq0 minted future admission", e)
				}
				return
			}
			reader := &HistoryWindowReader{store: s}
			allowed, e := reader.FilterNamespaceHistorySequences(ctx, branch.ConversationID, "owner", []int64{branch.Seq})
			if e != nil || !allowed[branch.Seq] {
				t.Fatal("actual new positive did not reopen future", e)
			}
			if name == "foreign-owner" {
				if _, e := reader.FilterNamespaceHistorySequences(ctx, branch.ConversationID, "stranger", []int64{branch.Seq}); e == nil {
					t.Fatal("CID spelling granted foreign principal")
				}
				return
			}
			if name == "normal-notification-independent" {
				other := msgprocessor.GetNotificationConversationIDByMsg(namespaceMessage("owner", "peer", constant.SingleChatType, false, true))
				var counter counter
				if e := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "sequence", other)}).Decode(&counter); e != nil || counter.Maximum != 0 {
					t.Fatal("normal source advanced independent n head", e)
				}
				if _, e := reader.FilterNamespaceHistorySequences(ctx, other, "owner", []int64{branch.Seq}); e == nil {
					t.Fatal("normal owner proof reused for n history")
				}
				return
			}
			if name == "new-positive" {
				var row bson.M
				if e := db.Collection("conversation").FindOne(ctx, bson.M{"owner_user_id": "owner", "conversation_id": branch.ConversationID}).Decode(&row); e != nil {
					t.Fatal("actual conversation projection missing", e)
				}
				return
			}
			var before namespaceOwnerProspective
			if e := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": namespaceOwnerProspectiveID(s.epoch, branch.ConversationID, "owner")}).Decode(&before); e != nil {
				t.Fatal(e)
			}
			below := branch.Seq + 1
			e = s.MutateMessageVisibility(ctx, db, []table.PendingVisibilityChange{{ConversationID: branch.ConversationID, Owners: []string{"owner"}, Below: below}}, func(tx context.Context) error {
				return unrelation.NewMsgMongoDriver(db).AdvanceMessageVisibility(tx, []table.MessageVisibilityFloor{{ConversationID: branch.ConversationID, UserID: "owner", MinSeq: below}})
			})
			if e != nil {
				t.Fatal("actual authoritative clear", e)
			}
			replay, e := s.AcceptKafkaRecord(ctx, record, policySHA, policy)
			if e != nil || !reflect.DeepEqual(replay, receipt) {
				t.Fatal("same source receipt changed", e)
			}
			var after namespaceOwnerProspective
			if e := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": before.ID}).Decode(&after); e != nil || !reflect.DeepEqual(after, before) {
				t.Fatal("old receipt reopened/retagged future", e)
			}
			allowed, e = reader.FilterNamespaceHistorySequences(ctx, branch.ConversationID, "owner", []int64{branch.Seq})
			if e != nil || allowed[branch.Seq] {
				t.Fatal("immutable proof resurrected cleared history", e)
			}
			if name == "new-positive-after-clear" {
				later := directFutureRecord(t, ctx, s, 3401, true)
				next, e := s.AcceptKafkaRecord(ctx, later, policySHA, policy)
				if e != nil {
					t.Fatal(e)
				}
				seq := next.Branches[0].Seq
				allowed, e = reader.FilterNamespaceHistorySequences(ctx, branch.ConversationID, "owner", []int64{branch.Seq, seq})
				if e != nil || allowed[branch.Seq] || !allowed[seq] {
					t.Fatal("new legitimate source failed to reopen only future", e)
				}
			}
		})
	}
}

func TestOwnerFutureMongoTrueForbidHistoryRejoinRevokesOriginalSource(t *testing.T) {
	ctx, authority, base, db, groups, members := originMongoFixture(t, nil, nil)
	info, receipt, e := applyNewGroupSeed(ctx, authority, base, groups, members, "g", nil)
	if e != nil {
		t.Fatal(e)
	}
	prepareCreatedMembershipFixture(t, ctx, base, db, info, receipt)
	s, e := NewMembershipBoundStore(db, base.epoch, base.activationSHA)
	if e != nil {
		t.Fatal(e)
	}
	ctx = context.WithValue(ctx, constant.OpUserID, "owner")
	record := futureGroupRecord(t, ctx, s, 3500, true)
	policy, policySHA := kafkaPolicyFixture(t, s)
	// This uses actual typed group creation and actual broker-coordinate intake,
	// not a synthetic current-roster JSON used as an imported history grant.
	first, e := s.AcceptKafkaRecord(ctx, record, policySHA, policy)
	if e != nil {
		t.Fatal(e)
	}
	cid := first.Branches[0].ConversationID
	seq := first.Branches[0].Seq
	reader := &HistoryWindowReader{store: s}
	allowed, e := reader.FilterNamespaceHistorySequences(ctx, cid, "member", []int64{seq})
	if e != nil || !allowed[seq] {
		t.Fatal("first actual source unavailable", e)
	}
	if e = jointRemoveMember(ctx, authority, s, db, "member"); e != nil {
		t.Fatal("actual removal", e)
	}
	jointAddMember(t, ctx, authority, s, db, "member", true, nil)
	var row ownerHistoryFloor
	if e = db.Collection(Sequences).FindOne(ctx, bson.M{"_id": historyFloorID(s.epoch, cid, "member")}).Decode(&row); e != nil || row.Floor != seq+1 || row.ProofID == "" {
		t.Fatal("actual forbid-history command did not bind floor", e)
	}
	allowed, e = reader.FilterNamespaceHistorySequences(ctx, cid, "member", []int64{seq})
	if e != nil || allowed[seq] {
		t.Fatal("immutable source regranted revoked history", e)
	}
	later := futureGroupRecord(t, ctx, s, 3501, true)
	next, e := s.AcceptKafkaRecord(ctx, later, policySHA, policy)
	if e != nil {
		t.Fatal(e)
	}
	laterSeq := next.Branches[0].Seq
	allowed, e = reader.FilterNamespaceHistorySequences(ctx, cid, "member", []int64{seq, laterSeq})
	if e != nil || allowed[seq] || !allowed[laterSeq] {
		t.Fatal("rejoin blocked legitimate new source or revived old", e)
	}
}

func futureGroupRecord(t *testing.T, ctx context.Context, s *Store, offset int64, senderSync bool) KafkaRecord {
	t.Helper()
	record := kafkaFixtureRecord(t, offset)
	var m sdkws.MsgData
	if proto.Unmarshal(record.Body, &m) != nil {
		t.Fatal("actual typed group record")
	}
	m.Options[constant.IsSenderSync] = senderSync
	permit, e := s.ReserveMessageNamespaces(ctx, &m)
	if e != nil || permit == nil || permit.Check(ctx, &m) != nil {
		t.Fatal("actual authorized pre-enqueue pair", e)
	}
	record.Body, e = proto.MarshalOptions{Deterministic: true}.Marshal(&m)
	if e != nil {
		t.Fatal(e)
	}
	return record
}
func TestOwnerHistoryMongoRealAdmissionKeepsIndependentGenerations(t *testing.T) {
	for _, name := range []string{"forbid-false-bounded-old-history", "forbid-true-revokes-all-old", "sender-sync-false-is-not-history-revocation"} {
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
			first, e := s.AcceptKafkaRecord(ctx, futureGroupRecord(t, ctx, s, 3600, name != "sender-sync-false-is-not-history-revocation"), policySHA, policy)
			if e != nil {
				t.Fatal(e)
			}
			cid := first.Branches[0].ConversationID
			reader := &HistoryWindowReader{store: s}
			if name == "sender-sync-false-is-not-history-revocation" {
				allowed, e := reader.FilterNamespaceHistorySequences(ctx, cid, "owner", []int64{1})
				if e != nil || !allowed[1] {
					t.Fatal("online self exclusion erased legitimate history", e)
				}
				return
			}
			if e = jointRemoveMember(ctx, authority, s, db, "member"); e != nil {
				t.Fatal(e)
			}
			second, e := s.AcceptKafkaRecord(ctx, futureGroupRecord(t, ctx, s, 3601, true), policySHA, policy)
			if e != nil || second.Branches[0].Seq != 2 {
				t.Fatal("actual leave-gap source", e)
			}
			var closed ownerHistoryClosure
			if e = db.Collection(Receipts).FindOne(ctx, bson.M{"_id": historyClosureID(s.epoch, cid, "member", info.ID)}).Decode(&closed); e != nil || closed.Upper != 1 {
				t.Fatal("absent member future crossed leave gap", e)
			}
			forbid := name == "forbid-true-revokes-all-old"
			command := jointAddMember(t, ctx, authority, s, db, "member", forbid, nil)
			third, e := s.AcceptKafkaRecord(ctx, futureGroupRecord(t, ctx, s, 3602, true), policySHA, policy)
			if e != nil || third.Branches[0].Seq != 3 {
				t.Fatal("actual new-generation source", e)
			}
			allowed, e := reader.FilterNamespaceHistorySequences(ctx, cid, "member", []int64{1, 2, 3})
			if e != nil || !allowed[3] || allowed[1] == forbid || allowed[2] == forbid {
				t.Fatal("actual admission history policy", allowed, e)
			}
			var live ownerHistoryInterval
			if e = db.Collection(Sequences).FindOne(ctx, bson.M{"_id": historyIntervalID(s.epoch, "namespace-owner-live-history-v1", cid, "member", command.ID)}).Decode(&live); e != nil || live.Lower != 3 || live.Upper != 0 || live.Revision != 1 {
				t.Fatal("new generation lost static original bound", e)
			}
			if e = s.transaction(ctx, func(tx context.Context) error {
				raw, e := bson.Marshal(live)
				if e != nil {
					return e
				}
				effective, e := s.readHistoryInterval(tx, NamespaceOriginDescriptor{CID: cid, Kind: "group", GroupID: "g", OriginIDs: []string{"g"}, SHA256: live.OriginSHA}, "member", raw)
				if e != nil {
					return e
				}
				if effective.Upper != 3 {
					return ErrConflict
				}
				return nil
			}); e != nil {
				t.Fatal("actual receipt-backed generation head", e)
			}
			count, e := db.Collection(Sequences).CountDocuments(ctx, bson.M{"kind": "namespace-owner-history-admission-v1", "conversation_id": cid, "user_id": "member"})
			if e != nil || forbid && count != 0 || !forbid && count != 1 {
				t.Fatal("bounded explicit admission source missing/invented", e)
			}
		})
	}
}
