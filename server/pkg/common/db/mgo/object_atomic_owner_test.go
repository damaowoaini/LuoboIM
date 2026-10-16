package mgo

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func objectAtomicMongoURIAllowed(uri string) bool {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "mongodb" || u.Host != "127.0.0.1:37017" || u.User != nil || u.Path != "/" || u.RawPath != "" || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	if len(query) == 0 {
		return true
	}
	values := query["replicaSet"]
	return len(query) == 1 && len(values) == 1 && values[0] == "luoboIntegration"
}

func TestObjectMetadataMongoFixtureURIGuard(t *testing.T) {
	for _, uri := range []string{"mongodb://127.0.0.1:37017/", "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration"} {
		if !objectAtomicMongoURIAllowed(uri) {
			t.Fatal("task fixture rejected")
		}
	}
	for _, uri := range []string{"mongodb+srv://127.0.0.1/", "mongodb://127.0.0.1:37017@external.invalid/", "mongodb://127.0.0.1:37017,external.invalid:27017/", "mongodb://external.invalid:37017/", "mongodb://127.0.0.1:27017/", "mongodb://user:password@127.0.0.1:37017/", "mongodb://127.0.0.1:37017/luobo_stack", "mongodb://127.0.0.1:37017/?proxyHost=external.invalid", "mongodb://127.0.0.1:37017/?replicaSet=other", "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration&replicaSet=luoboIntegration", "mongodb://127.0.0.1:37017/#fragment"} {
		if objectAtomicMongoURIAllowed(uri) {
			t.Fatal("nonfixture URI accepted")
		}
	}
}

// Temporary Mongo metadata tests only. No object provider or hash-claim HTTP
// request is used, and no live fixture metadata is opened or changed.
func objectAtomicDB(t *testing.T) (context.Context, *mongo.Database, relation.ObjectInfoModelInterface) {
	t.Helper()
	uri := os.Getenv("LUOBO_STORAGE_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("explicit isolated LUOBO_STORAGE_TEST_MONGO_URI required")
	}
	if !objectAtomicMongoURIAllowed(uri) {
		t.Fatal("only task-private loopback Mongo fixture permitted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal("cannot initialize task-private Mongo client")
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.Disconnect(cleanup); err != nil {
			t.Error("task-private Mongo disconnect failed")
		}
	})
	if err := client.Ping(ctx, nil); err != nil {
		t.Fatal("cannot connect to task-private Mongo fixture")
	}
	db := client.Database("luobo_object_atomic_test_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := db.Drop(cleanup); err != nil {
			t.Error("isolated object metadata test database cleanup failed")
		}
	})
	s, err := NewS3Mongo(db)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, db, s
}

func TestObjectMetadataSameOwnerConcurrentUpsertAndForeignCAS(t *testing.T) {
	ctx, db, store := objectAtomicDB(t)
	base := relation.ObjectModel{Name: "synthetic-owner/file", UserID: "synthetic-owner", Engine: "fixture", Key: "synthetic-verified-location", Hash: "synthetic-only", Size: 12, CompletionProofID: "synthetic-proof", CreateTime: time.Now().UTC()}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value := base
			if err := store.SetObject(ctx, &value); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := store.Take(ctx, base.Engine, base.Name)
	if err != nil || got.UserID != base.UserID || got.Key != base.Key || got.CompletionProofID != base.CompletionProofID {
		t.Fatal("same owner upsert lost fields", err)
	}
	before, err := db.Collection("s3").FindOne(ctx, bson.M{"name": base.Name}).DecodeBytes()
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*relation.ObjectModel){func(o *relation.ObjectModel) { o.UserID = "different-owner" }, func(o *relation.ObjectModel) { o.Engine = "different-engine" }, func(o *relation.ObjectModel) { o.UserID = "" }} {
		invalid := base
		change(&invalid)
		invalid.Key = "untrusted-change"
		if err := store.SetObject(ctx, &invalid); err == nil {
			t.Fatal("owner/engine CAS accepted invalid overwrite")
		}
		after, err := db.Collection("s3").FindOne(ctx, bson.M{"name": base.Name}).DecodeBytes()
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("CAS rejection mutated original row", err)
		}
	}
	if count, err := db.Collection("s3").CountDocuments(ctx, bson.M{}); err != nil || count != 1 {
		t.Fatal("global name uniqueness lost", err)
	}
	indexes, err := db.Collection("s3").Indexes().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer indexes.Close(ctx)
	uniqueName := false
	for indexes.Next(ctx) {
		var index bson.M
		if err := indexes.Decode(&index); err != nil {
			t.Fatal(err)
		}
		if index["name"] == "name_1" && index["unique"] == true {
			uniqueName = true
		}
	}
	if !uniqueName {
		t.Fatal("original name unique index was replaced")
	}
}

func TestObjectMetadataUnknownLegacyOwnerRemainsReadOnly(t *testing.T) {
	ctx, db, store := objectAtomicDB(t)
	legacy := bson.M{"name": "synthetic-legacy/file", "engine": "fixture", "key": "openim/data/hash/legacy-retained", "hash": "historical-value", "size": int64(12), "create_time": time.Now().UTC()}
	if _, err := db.Collection("s3").InsertOne(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	before, err := db.Collection("s3").FindOne(ctx, bson.M{"name": legacy["name"]}).DecodeBytes()
	if err != nil {
		t.Fatal(err)
	}
	read, err := store.Take(ctx, "fixture", legacy["name"].(string))
	if err != nil || read.Key != legacy["key"] || read.UserID != "" {
		t.Fatal("legacy lookup compatibility changed", err)
	}
	claim := *read
	claim.UserID = "synthetic-new-owner"
	claim.Key = "replacement"
	if err := store.SetObject(ctx, &claim); !errs.ErrNoPermission.Is(err) {
		t.Fatal("unknown legacy owner was adopted", err)
	}
	after, err := db.Collection("s3").FindOne(ctx, bson.M{"name": legacy["name"]}).DecodeBytes()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("legacy raw metadata changed", err)
	}
	if err := store.SetObject(ctx, nil); err == nil {
		t.Fatal("nil metadata accepted")
	}
}
