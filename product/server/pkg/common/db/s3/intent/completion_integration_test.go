package intent

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func completionFixtureMongoURIAllowed(uri string) bool {
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

func TestCompletionMongoFixtureURIGuard(t *testing.T) {
	for _, uri := range []string{"mongodb://127.0.0.1:37017/", "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration"} {
		if !completionFixtureMongoURIAllowed(uri) {
			t.Fatal("task fixture rejected")
		}
	}
	for _, uri := range []string{"mongodb+srv://127.0.0.1/", "mongodb://127.0.0.1:37017@external.invalid/", "mongodb://127.0.0.1:37017,external.invalid:27017/", "mongodb://external.invalid:37017/", "mongodb://127.0.0.1:27017/", "mongodb://user:password@127.0.0.1:37017/", "mongodb://127.0.0.1:37017/luobo_stack", "mongodb://127.0.0.1:37017/?proxyHost=external.invalid", "mongodb://127.0.0.1:37017/?replicaSet=other", "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration&replicaSet=luoboIntegration", "mongodb://127.0.0.1:37017/#fragment"} {
		if completionFixtureMongoURIAllowed(uri) {
			t.Fatal("nonfixture URI accepted")
		}
	}
}

func completionDB(t *testing.T) (context.Context, *mongo.Database) {
	t.Helper()
	uri := os.Getenv("LUOBO_STORAGE_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("explicit isolated LUOBO_STORAGE_TEST_MONGO_URI required")
	}
	if !completionFixtureMongoURIAllowed(uri) {
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
	db := client.Database("luobo_storage_v2_test_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := db.Drop(cleanup); err != nil {
			t.Error("isolated storage test database cleanup failed")
		}
	})
	return context.WithValue(ctx, constant.OpUserID, "synthetic-storage-owner"), db
}

func TestCompletionNamespaceConcurrentRestartAndInvalidState(t *testing.T) {
	ctx, db := completionDB(t)
	var wg sync.WaitGroup
	scopes := make(chan Scope, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store, err := New(ctx, db)
			if err != nil {
				t.Error(err)
				return
			}
			scope, err := store.Scope(ctx, "fixture")
			if err != nil {
				t.Error(err)
				return
			}
			scopes <- scope
		}()
	}
	wg.Wait()
	close(scopes)
	first := ""
	count := 0
	for s := range scopes {
		count++
		if first != "" && first != s.ID {
			t.Fatal("concurrent startup changed namespace")
		}
		first = s.ID
	}
	if count != 12 {
		t.Fatal("startup failed")
	}
	rows, err := db.Collection("object_storage_namespaces").CountDocuments(ctx, bson.M{})
	if err != nil || rows != 1 {
		t.Fatal("namespace is not a singleton", err)
	}
	store, err := New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.Scope(ctx, "fixture")
	if err != nil || again.ID != first {
		t.Fatal("restart scope changed", err)
	}
	// Malformed persisted state is an operator error. It cannot rotate scope.
	if _, err = db.Collection("object_storage_namespaces").UpdateOne(ctx, bson.M{"_id": namespaceDocumentID}, bson.M{"$set": bson.M{"namespace": "invalid"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = New(ctx, db); err == nil {
		t.Fatal("invalid namespace silently replaced")
	}
	var persisted namespaceRecord
	if err = db.Collection("object_storage_namespaces").FindOne(ctx, bson.M{"_id": namespaceDocumentID}).Decode(&persisted); err != nil || persisted.Namespace != "invalid" {
		t.Fatal("invalid namespace was mutated", err)
	}
}

func TestCompletionProofConcurrentCommitPersistsBeyondIntentTTL(t *testing.T) {
	ctx, db := completionDB(t)
	s, err := New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	parts := []string{completionMD5("fixed synthetic bytes")}
	wire := completionMD5(parts[0])
	scope, err := s.Scope(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	capability, err := s.Create(ctx, "multipart", "fixture", map[string]string{"synthetic": "payload"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan *CompletionProof, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := s.CommitCompleted(ctx, "fixture", wire, 21, scope.ObjectPrefix(wire, 21)+uuid.NewString(), parts[0], parts)
			if err != nil {
				t.Error(err)
				return
			}
			results <- p
		}()
	}
	wg.Wait()
	close(results)
	winner := ""
	count := 0
	for p := range results {
		count++
		if winner != "" && winner != p.Key {
			t.Fatal("concurrent commit overwrote winner")
		}
		winner = p.Key
	}
	if count != 12 {
		t.Fatal("commit failed")
	}
	if n, err := s.proofs.CountDocuments(ctx, bson.M{}); err != nil || n != 1 {
		t.Fatal("multiple proof rows", err)
	}
	if _, err = s.collection.DeleteOne(ctx, bson.M{"_id": capability}); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	p, err := restarted.FindCompleted(ctx, "fixture", wire, 21)
	if err != nil || p.Key != winner || p.HashContract != HashContract || len(p.PartHashes) != 1 {
		t.Fatal("permanent proof lost with intent", err)
	}
	indexes, err := s.proofs.Indexes().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer indexes.Close(ctx)
	for indexes.Next(ctx) {
		var index bson.M
		if err := indexes.Decode(&index); err != nil {
			t.Fatal(err)
		}
		if _, ok := index["expireAfterSeconds"]; ok {
			t.Fatal("completion proof has TTL")
		}
	}
	if err := indexes.Err(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitCompleted(ctx, "fixture", wire, 21, "openim/data/hash/"+wire, parts[0], parts); err == nil {
		t.Fatal("legacy key acquired proof")
	}
	if _, err = s.CommitCompleted(ctx, "fixture", wire, 21, scope.ObjectPrefix(wire, 21)+uuid.NewString(), parts[0], []string{completionMD5("different")}); err == nil {
		t.Fatal("incorrect wire contract acquired proof")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.FindCompleted(cancelled, "fixture", wire, 21); err == nil {
		t.Fatal("cancelled proof read was accepted")
	}
}
