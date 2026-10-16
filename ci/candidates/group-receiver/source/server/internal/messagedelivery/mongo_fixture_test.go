package messagedelivery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type sequenceFixtureOwnershipKey struct{}
type sequenceFixtureOwnership struct{ dropAllowed atomic.Bool }

// Only the existing ephemeral cloud fixture is accepted. No production URI,
// discovery outside loopback, failpoint, global collection drop, or Redis access.
func sequenceMongo(t *testing.T, monitor *event.CommandMonitor) (context.Context, *groupauthority.Store, *Store, *mongo.Database) {
	return sequenceMongoTransport(t, monitor, nil)
}

func sequenceMongoTransport(t *testing.T, monitor *event.CommandMonitor, dialer options.ContextDialer) (context.Context, *groupauthority.Store, *Store, *mongo.Database) {
	t.Helper()
	raw := os.Getenv("LUOBO_STORAGE_TEST_MONGO_URI")
	if raw == "" {
		t.Skip("explicit ephemeral group authority Mongo fixture not selected")
	}
	if raw != "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration" {
		t.Fatal("exact task replica-set URI required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	ownership := &sequenceFixtureOwnership{}
	ownership.dropAllowed.Store(true)
	ctx = context.WithValue(ctx, sequenceFixtureOwnershipKey{}, ownership)
	t.Cleanup(cancel)
	clientOptions := options.Client().ApplyURI(raw).SetDirect(true).SetRetryWrites(false).SetServerSelectionTimeout(2 * time.Second).SetMonitor(monitor)
	if dialer != nil {
		clientOptions.SetDialer(dialer)
	}
	client, err := mongo.Connect(ctx, clientOptions)
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
	name, claim := "luobo_message_delivery_test_"+hex.EncodeToString(nonce[:12]), hex.EncodeToString(nonce[12:])
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
		if !ownership.dropAllowed.Load() {
			t.Error("owned test worker drain unconfirmed; retaining task database")
			return
		}
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
	for _, collection := range []string{"group", "group_member", groupauthority.InvalidationsCollection, Sequences, Receipts, Effects, "msg", "message_visibility_floors"} {
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
	store, err := groupauthority.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, gid := range []string{"init", "left", "right"} {
		if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": gid, groupauthority.RevisionField: int64(1)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Collection("msg").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "doc_id", Value: 1}}, Options: options.Index().SetUnique(true)}); err != nil {
		t.Fatal(err)
	}
	effects, err := NewStore(db, "c2fb972f-c1ae-4d34-b937-2ec837d7529d", hash([]byte("synthetic exclusive writer activation")))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{Sequences, Receipts, Effects} {
		if _, err := db.Collection(name).InsertOne(ctx, bson.M{"_id": SchemaID, "version": int32(1), "epoch": effects.epoch, "activation_sha256": effects.activationSHA, "legacy_writers_retired": true}); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, store, effects, db
}

// Each test group is only a disjoint foundation transaction fence, not a
// statement that an ordinary message belongs to that group. Different fences
// permit real contention on the SAME message counter under separate sessions.
func sequenceTxn(ctx context.Context, authority *groupauthority.Store, fence string, work func(context.Context) error) error {
	_, err := authority.Mutation(ctx, []groupauthority.Target{{GroupID: fence}}, func(context.Context) error { return nil }, work)
	return err
}
func initializeSequence(t *testing.T, ctx context.Context, authority *groupauthority.Store, s *Store, cid string, maximum int64) {
	t.Helper()
	if err := sequenceTxn(ctx, authority, "init", func(tx context.Context) error {
		return s.InitializeSequence(tx, cid, Seed{MongoMax: maximum, RedisMax: maximum, MongoKnown: true, RedisKnown: true, EvidenceSHA256: hash([]byte("synthetic exact prior floor:" + cid))})
	}); err != nil {
		t.Fatal(err)
	}
}
