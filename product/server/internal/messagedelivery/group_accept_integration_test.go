package messagedelivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func prepareGroupNotification(t *testing.T, ctx context.Context, authority *groupauthority.Store, s *Store, db *mongo.Database, targets bool) groupeffects.Frozen {
	t.Helper()
	if err := db.CreateCollection(ctx, groupeffects.Collection); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection(groupeffects.Collection).InsertOne(ctx, bson.M{"_id": groupeffects.SchemaID, "kind": "group-effects-schema-v1", "version": int32(1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": "g", groupauthority.RevisionField: int64(1), "status": constant.GroupOk}); err != nil {
		t.Fatal(err)
	}
	msg := &sdkws.MsgData{SendID: "owner", RecvID: "g", GroupID: "g", SessionType: constant.SuperGroupChatType, MsgFrom: constant.SysMsgType, ContentType: constant.GroupDismissedNotification, Content: []byte("synthetic frozen group tip"), Options: msgprocessor.NewOptions(msgprocessor.WithHistory(true), msgprocessor.WithPersistent(), msgprocessor.WithNotNotification(false))}
	frozen, err := groupeffects.NewNotification(uuid.NewString(), 0, "g", "owner", msg, time.Now().Add(-31*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := groupeffects.PlanNotificationBranches(frozen)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plans {
		if err := s.PrepareSequence(ctx, p.ConversationID, Seed{MongoMax: 0, RedisMax: 0, MongoKnown: true, RedisKnown: true, EvidenceSHA256: hash([]byte("synthetic raw empty group branch:" + p.ConversationID))}); err != nil {
			t.Fatal(err)
		}
	}
	effectStore, err := groupeffects.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	actor := context.WithValue(ctx, constant.OpUserID, "owner")
	_, err = authority.Mutation(actor, []groupauthority.Target{{GroupID: "g", Members: []string{"a", "z", "owner"}}}, func(context.Context) error { return nil }, func(tx context.Context) error {
		if err := effectStore.Queue(tx, []groupeffects.Frozen{frozen}); err != nil {
			return err
		}
		if !targets {
			return nil
		}
		var rosters []groupeffects.NotificationTargets
		for _, p := range plans {
			rosters = append(rosters, groupeffects.NotificationTargets{BranchID: p.ID, UserIDs: []string{"a"}})
		}
		return effectStore.FreezeNotificationTargets(tx, frozen.ID, rosters)
	})
	if err != nil {
		t.Fatal(err)
	}
	return frozen
}
func assertMinimalGroupSource(t *testing.T, ctx context.Context, db *mongo.Database, id string) {
	t.Helper()
	var r bson.M
	if err := db.Collection(groupeffects.Collection).FindOne(ctx, bson.M{"_id": id}).Decode(&r); err != nil {
		t.Fatal(err)
	}
	if r["state"] != "done" || r["decision"] != "receiver-ack" || r["payload"] != nil || r["delivery_targets"] != nil || r["delivery_receipt_sha256"] == nil {
		t.Fatal("accepted parent notification retained plaintext/recipient copy or lost receipt binding")
	}
}

func TestDeliveryMongoGroupAcceptanceRetiresParentAndKeepsOneStableReceipt(t *testing.T) {
	ctx, authority, s, db := sequenceMongo(t, nil)
	frozen := prepareGroupNotification(t, ctx, authority, s, db, true)
	receipt, err := s.AcceptGroupNotificationCommitted(ctx, frozen.ID)
	if err != nil || len(receipt.Branches) != 1 || receipt.Branches[0].Seq != 1 {
		t.Fatal("typed group notification not accepted", err)
	}
	assertMinimalGroupSource(t, ctx, db, frozen.ID)
	// Erase after acceptance but before sender's success can be used. The parent
	// is already minimal, and a restart can resolve without any original body.
	driver, err := unrelation.NewMsgMongoDriverWithDeliveryVisibility(db, s)
	if err != nil {
		t.Fatal(err)
	}
	cid := receipt.Branches[0].ConversationID
	if err := driver.AdvanceMessageVisibility(ctx, []table.MessageVisibilityFloor{{ConversationID: cid, MinSeq: 2}}); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewStore(db, s.epoch, s.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := restarted.AcceptGroupNotificationCommitted(ctx, frozen.ID)
	if err != nil || replay.Branches[0].Seq != 1 || replay.ID != receipt.ID {
		t.Fatal("group sender restart rebuilt an accepted source", err)
	}
	d, err := restarted.readDuty(ctx, receipt.Branches[0].ID)
	if err != nil || len(d.Payload) != 0 || d.PushPending {
		t.Fatal("group source replay revived erased child payload", err)
	}
	count, err := db.Collection(Receipts).CountDocuments(ctx, bson.M{"source_id": frozen.ID})
	if err != nil || count != 1 {
		t.Fatal("duplicate child acceptance receipt", err)
	}
	assertMinimalGroupSource(t, ctx, db, frozen.ID)
}

func TestDeliveryMongoGroupAcceptanceRequiresFrozenAuthorizedRoster(t *testing.T) {
	ctx, authority, s, db := sequenceMongo(t, nil)
	frozen := prepareGroupNotification(t, ctx, authority, s, db, false)
	if _, err := s.AcceptGroupNotificationCommitted(ctx, frozen.ID); err == nil {
		t.Fatal("missing frozen roster silently used live member cache")
	}
	count, err := db.Collection(Receipts).CountDocuments(ctx, bson.M{"_id": bson.M{"$ne": SchemaID}})
	if err != nil || count != 0 {
		t.Fatal("unprepared group target allocated a sequence", err)
	}
	if _, err := s.AcceptGroupNotificationCommitted(ctx, uuid.NewString()); err == nil {
		t.Fatal("caller UUID without typed durable source was accepted")
	}
	plans, err := groupeffects.PlanNotificationBranches(frozen)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := groupeffects.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	actor := context.WithValue(ctx, constant.OpUserID, "owner")
	_, err = authority.Mutation(actor, []groupauthority.Target{{GroupID: "g", Members: []string{"a"}}}, func(context.Context) error { return nil }, func(tx context.Context) error {
		var targets []groupeffects.NotificationTargets
		for _, p := range plans {
			targets = append(targets, groupeffects.NotificationTargets{BranchID: p.ID, UserIDs: []string{"outside-authorized-scope"}})
		}
		return effects.FreezeNotificationTargets(tx, frozen.ID, targets)
	})
	if !errors.Is(err, groupauthority.ErrInvalidScope) {
		t.Fatal("recipient proof accepted a UID outside the private authorized target scope", err)
	}
}

func TestDeliveryMongoGroupCommitLostACKRestartsFromMinimalBoundReceipt(t *testing.T) {
	drop := &commitReplyDropper{all: true, requests: map[int32]bool{}}
	ctx, authority, s, db := sequenceMongoTransport(t, drop.monitor(), drop)
	frozen := prepareGroupNotification(t, ctx, authority, s, db, true)
	marked := context.WithValue(ctx, deliveryDropCommitKey{}, true)
	bounded, cancel := context.WithTimeout(marked, 3*time.Second)
	result, err := s.AcceptGroupNotificationCommitted(bounded, frozen.ID)
	cancel()
	if !errors.Is(err, ErrCommitUnconfirmed) || result.ID != "" {
		t.Fatal("lost actual commit ACK returned local success", err)
	}
	assertSingleCommittedDeliveryWire(t, drop)
	assertMinimalGroupSource(t, ctx, db, frozen.ID)
	restarted, err := NewStore(db, s.epoch, s.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := restarted.AcceptGroupNotificationCommitted(ctx, frozen.ID)
	if err != nil || receipt.Branches[0].Seq != 1 {
		t.Fatal("restart did not recover original parent/child receipt", err)
	}
	count, err := db.Collection(Effects).CountDocuments(ctx, bson.M{"source_id": frozen.ID})
	if err != nil || count != 1 {
		t.Fatal("unknown ACK generated extra push duty", err)
	}
}
