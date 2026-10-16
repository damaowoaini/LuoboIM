package mgo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Only the existing ephemeral cloud fixture is accepted. No production URI,
// discovery outside loopback, failpoint, global collection drop, or Redis access.
func membershipCommandMongo(t *testing.T) (context.Context, *groupauthority.Store, *mongo.Database) {
	t.Helper()
	raw := os.Getenv("LUOBO_STORAGE_TEST_MONGO_URI")
	if raw == "" {
		t.Skip("explicit ephemeral group authority Mongo fixture not selected")
	}
	if raw != "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration" {
		t.Fatal("exact task replica-set URI required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	t.Cleanup(cancel)
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(raw).SetDirect(true).SetServerSelectionTimeout(2*time.Second))
	if err != nil {
		t.Fatal("task Mongo connection failed")
	}
	t.Cleanup(func() {
		end, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Disconnect(end); err != nil {
			t.Error("task disconnect unconfirmed", err)
		}
	})
	var hello bson.M
	if err := client.Database("admin").RunCommand(ctx, bson.M{"hello": 1}).Decode(&hello); err != nil || hello["setName"] != "luoboIntegration" || hello["isWritablePrimary"] != true {
		t.Fatal("exact task writable primary unavailable")
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("task nonce unavailable")
	}
	name, claim := "luobo_group_history_scope_test_"+hex.EncodeToString(nonce[:12]), hex.EncodeToString(nonce[12:])
	names, err := client.ListDatabaseNames(ctx, bson.M{"name": name})
	if err != nil || len(names) != 0 {
		t.Fatal("fresh task database absence unconfirmed")
	}
	db := client.Database(name)
	if err := db.CreateCollection(ctx, "authority_fixture_claim"); err != nil {
		t.Fatal("exclusive task claim failed; no drop authorized")
	}
	if _, err := db.Collection("authority_fixture_claim").InsertOne(ctx, bson.M{"_id": "task-claim", "nonce": claim}); err != nil {
		t.Fatal("claim ACK unknown; retain task database")
	}
	t.Cleanup(func() {
		end, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var row bson.M
		if db.Name() != name {
			t.Error("task DB binding changed")
			return
		}
		if err := db.Collection("authority_fixture_claim").FindOne(end, bson.M{"_id": "task-claim", "nonce": claim}).Decode(&row); err != nil {
			t.Error("claim ownership unconfirmed; retain database", err)
			return
		}
		if err := db.Drop(end); err != nil {
			t.Error("exact task database cleanup unconfirmed", err)
			return
		}
		names, err := client.ListDatabaseNames(end, bson.M{"name": name})
		if err != nil || len(names) != 0 {
			t.Error("task database removal unconfirmed", err)
		}
	})
	// Explicit operator fixture preparation, never a runtime constructor effect.
	for _, collection := range []string{"group", "group_member", groupauthority.InvalidationsCollection} {
		if err := db.CreateCollection(ctx, collection); err != nil {
			t.Fatal("fixture preparation failed", err)
		}
	}
	if _, err := db.Collection("group").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "group_id", Value: 1}}, Options: options.Index().SetUnique(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("group_member").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "group_id", Value: 1}, {Key: "user_id", Value: 1}}, Options: options.Index().SetUnique(true)}); err != nil {
		t.Fatal(err)
	}
	// Exact task-owned raw setup only; actual u1 Create/Delete below always
	// use the real writer with authority scope and membership-history hooks.
	if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": "g1", "status": int32(constant.GroupOk), "group_type": int32(constant.WorkingGroup), groupauthority.RevisionField: int64(1)}); err != nil { t.Fatal(err) }
	if _, err := db.Collection("group_member").InsertOne(ctx, bson.M{"group_id": "g1", "user_id": "fixture-owner", "role_level": int32(constant.GroupOwner)}); err != nil { t.Fatal(err) }
	ctx = context.WithValue(ctx, constant.OpUserID, "fixture-owner")
	store, err := groupauthority.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, store, db
}
