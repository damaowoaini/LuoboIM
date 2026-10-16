package mongotx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/tools/tx"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

func TestTransactionsUseEachRequestContext(t *testing.T) {
	uri := os.Getenv("LUOBO_MONGO_TEST_URI")
	if uri == "" {
		t.Skip("requires explicit isolated loopback replica set")
	}
	parsed, err := url.Parse(uri)
	if err != nil || net.ParseIP(parsed.Hostname()) == nil || !net.ParseIP(parsed.Hostname()).IsLoopback() {
		t.Fatal("fixture must use loopback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		t.Fatal(err)
	}
	database := client.Database(fmt.Sprintf("luobo_tx_request_%d", time.Now().UnixNano()))
	defer database.Drop(context.Background())
	if err := database.CreateCollection(ctx, "events"); err != nil {
		t.Fatal(err)
	}
	collection := database.Collection("events")
	insert := func(id string) func(context.Context) error {
		return func(current context.Context) error {
			_, err := collection.InsertOne(current, bson.M{"_id": id})
			return err
		}
	}
	// Demonstrate the pinned helper's captured-context failure on the real
	// replica set, then exercise the replacement under the same request order.
	legacy := tx.NewMongo(client)
	first, stop := context.WithCancel(ctx)
	if err := legacy.Transaction(first, insert("legacy-first")); err != nil {
		t.Fatal(err)
	}
	stop()
	if err := legacy.Transaction(ctx, insert("legacy-second")); !errors.Is(err, context.Canceled) {
		t.Fatalf("legacy reproduction changed: %v", err)
	}
	current := New(client)
	first, stop = context.WithCancel(ctx)
	if err := current.Transaction(first, insert("first")); err != nil {
		t.Fatal(err)
	}
	stop()
	if err := current.Transaction(ctx, insert("second")); err != nil {
		t.Fatalf("new request reused canceled predecessor: %v", err)
	}
	injected := errors.New("injected rollback")
	err = current.Transaction(ctx, func(current context.Context) error {
		if err := insert("rolled-back")(current); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("rollback result: %v", err)
	}
	if err := collection.FindOne(ctx, bson.M{"_id": "rolled-back"}).Err(); !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf("failed transaction committed: %v", err)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	called := false
	if err := current.Transaction(canceled, func(context.Context) error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
		t.Fatal("canceled request executed callback")
	}
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			request, stop := context.WithCancel(ctx)
			defer stop()
			failures <- current.Transaction(request, insert(fmt.Sprintf("parallel-%d", i)))
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count, err := collection.CountDocuments(ctx, bson.M{}); err != nil || count != 11 {
		t.Fatalf("lost/extra commits: count=%d err=%v", count, err)
	}
}
