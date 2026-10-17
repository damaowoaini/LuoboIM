package groupcleanup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type cleanupFixture struct {
	ctx       context.Context
	db        *mongo.Database
	cleanup   *Store
	authority *groupauthority.Store
	delivery  *messagedelivery.Store
	effects   *groupeffects.Store
	groups    relation.GroupModelInterface
	members   relation.GroupMemberModelInterface
	frozen    groupeffects.Frozen
	intent    Intent
	gid       string
}

func cleanupMongo(t *testing.T) *cleanupFixture {
	t.Helper()
	raw := os.Getenv("LUOBO_STORAGE_TEST_MONGO_URI")
	if raw == "" {
		t.Skip("explicit ephemeral group cleanup Mongo fixture not selected")
	}
	if raw != "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration" {
		t.Fatal("exact task replica-set URI required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(raw).SetDirect(true).SetRetryWrites(false).SetServerSelectionTimeout(2*time.Second))
	if err != nil {
		t.Fatal("task Mongo unavailable")
	}
	t.Cleanup(func() {
		end, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := client.Disconnect(end); err != nil {
			t.Error("owned Mongo disconnect unconfirmed", err)
		}
	})
	var hello bson.M
	if client.Database("admin").RunCommand(ctx, bson.M{"hello": 1}).Decode(&hello) != nil || hello["setName"] != "luoboIntegration" || hello["isWritablePrimary"] != true {
		t.Fatal("task writable primary unavailable")
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("test nonce unavailable")
	}
	name, claim := "luobo_group_cleanup_test_"+hex.EncodeToString(nonce[:12]), hex.EncodeToString(nonce[12:])
	names, err := client.ListDatabaseNames(ctx, bson.M{"name": name})
	if err != nil || len(names) != 0 {
		t.Fatal("fresh random DB absence unconfirmed")
	}
	db := client.Database(name)
	if err := db.CreateCollection(ctx, "cleanup_fixture_claim"); err != nil {
		t.Fatal("exclusive test claim failed; no drop authorized")
	}
	if _, err := db.Collection("cleanup_fixture_claim").InsertOne(ctx, bson.M{"_id": "owned", "nonce": claim}); err != nil {
		t.Fatal("claim ACK unknown; retain test database")
	}
	t.Cleanup(func() {
		end, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		var own bson.M
		if err := db.Collection("cleanup_fixture_claim").FindOne(end, bson.M{"_id": "owned", "nonce": claim}).Decode(&own); err != nil {
			t.Error("test ownership unknown; retain DB", err)
			return
		}
		if err := db.Drop(end); err != nil {
			t.Error("exact test DB cleanup unconfirmed", err)
			return
		}
		names, err := client.ListDatabaseNames(end, bson.M{"name": name})
		if err != nil || len(names) != 0 {
			t.Error("exact DB absence unconfirmed", err)
		}
	})
	for _, name := range []string{"group", "group_member", "group_membership_history", groupauthority.InvalidationsCollection, groupeffects.Collection, messagedelivery.Sequences, messagedelivery.Receipts, messagedelivery.Effects, "msg", "message_visibility_floors", Collection} {
		if err := db.CreateCollection(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	groups, err := mgo.NewGroupMongoContext(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	members, err := mgo.NewGroupMemberContext(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("msg").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "doc_id", Value: 1}}, Options: options.Index().SetUnique(true)}); err != nil {
		t.Fatal(err)
	}
	epoch, activation := uuid.NewString(), digest([]byte("synthetic drained complete source roster"))
	for _, name := range []string{messagedelivery.Sequences, messagedelivery.Receipts, messagedelivery.Effects} {
		if _, err := db.Collection(name).InsertOne(ctx, bson.M{"_id": messagedelivery.SchemaID, "version": int32(1), "epoch": epoch, "activation_sha256": activation, "legacy_writers_retired": true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Collection(Collection).InsertOne(ctx, bson.M{"_id": SchemaID, "version": int32(1), "epoch": epoch, "activation_sha256": activation, "legacy_writers_retired": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection(groupeffects.Collection).InsertOne(ctx, bson.M{"_id": groupeffects.SchemaID, "kind": "group-effects-schema-v1", "version": int32(1)}); err != nil {
		t.Fatal(err)
	}
	gid := "cleanup_" + uuid.NewString()
	if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": gid, "status": int32(constant.GroupOk), groupauthority.RevisionField: int64(1)}); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{"a", "z", "owner"} {
		role := int32(20)
		if uid == "owner" {
			role = 100
		}
		if _, err := db.Collection("group_member").InsertOne(ctx, relation.GroupMemberModel{GroupID: gid, UserID: uid, RoleLevel: role, JoinTime: time.Now().Add(-time.Hour).UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	cleanup, err := NewStore(db, epoch, activation)
	if err != nil {
		t.Fatal(err)
	}
	message := &sdkws.MsgData{SendID: "owner", RecvID: gid, GroupID: gid, SessionType: constant.SuperGroupChatType, MsgFrom: constant.SysMsgType, ContentType: constant.GroupDismissedNotification, Content: []byte("synthetic dismissal notice"), Options: msgprocessor.NewOptions(msgprocessor.WithNotNotification(false), msgprocessor.WithHistory(true), msgprocessor.WithPersistent())}
	frozen, err := groupeffects.NewNotification(uuid.NewString(), 0, gid, "owner", message, time.Now().Add(-31*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	intent, err := IntentFromDismissNotification(frozen)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := groupeffects.PlanNotificationBranches(frozen)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plans {
		if err := cleanup.delivery.PrepareSequence(ctx, p.ConversationID, messagedelivery.Seed{MongoMax: 0, RedisMax: 0, MongoKnown: true, RedisKnown: true, EvidenceSHA256: digest([]byte("synthetic raw empty CID:" + p.ConversationID))}); err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanup.CheckReady(ctx); err != nil {
		t.Fatal(err)
	}
	f := &cleanupFixture{ctx: ctx, db: db, cleanup: cleanup, authority: cleanup.authority, delivery: cleanup.delivery, effects: cleanup.effects, groups: groups, members: members, frozen: frozen, intent: intent, gid: gid}
	actor := context.WithValue(ctx, constant.OpUserID, "owner")
	_, err = f.authority.Mutation(actor, []groupauthority.Target{{GroupID: gid, AllMembers: true}}, func(context.Context) error { return nil }, func(tx context.Context) error {
		if err := f.groups.UpdateStatus(tx, gid, constant.GroupStatusDismissed); err != nil {
			return err
		}
		if err := f.effects.Queue(tx, []groupeffects.Frozen{frozen}); err != nil {
			return err
		}
		var targets []groupeffects.NotificationTargets
		for _, p := range plans {
			targets = append(targets, groupeffects.NotificationTargets{BranchID: p.ID, UserIDs: []string{"a", "owner", "z"}})
		}
		if err := f.effects.FreezeNotificationTargets(tx, frozen.ID, targets); err != nil {
			return err
		}
		return f.cleanup.Queue(tx, intent)
	})
	if err != nil {
		t.Fatal("actual soft-dismiss/typed obligation transaction failed", err)
	}
	return f
}
