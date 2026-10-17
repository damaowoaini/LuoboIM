package sessionlogout

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestRealLogoutOutboxRestartOnlyRevokesExactToken(t *testing.T) {
	uri, socket := os.Getenv("LUOBO_INTEGRATION_MONGO_URI"), os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET")
	if uri == "" || socket == "" {
		t.Skip("private Mongo/Redis fixtures required")
	}
	if !strings.HasPrefix(uri, "mongodb://127.0.0.1:37017/") || !strings.HasSuffix(socket, "/references/integration/redis8.2/redis.sock") {
		t.Fatal("private fixtures only")
	}
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database("luobo_logout_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	t.Cleanup(func() { db.Drop(ctx); client.Disconnect(ctx) })
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 15})
	t.Cleanup(func() { rdb.Close() })
	tokens := cache.NewMsgCacheModel(rdb)
	user := "logout-" + uuid.NewString()
	foreign := user + "-peer"
	t.Cleanup(func() {
		tokens.DeleteAllTokensByUidPid(ctx, user, 2)
		tokens.DeleteAllTokensByUidPid(ctx, user, 5)
		tokens.DeleteAllTokensByUidPid(ctx, foreign, 2)
	})
	for _, row := range []struct {
		user     string
		platform int
		token    string
	}{{user, 2, "synthetic-old"}, {user, 2, "synthetic-new"}, {user, 5, "synthetic-other-platform"}, {foreign, 2, "synthetic-peer"}} {
		if err := tokens.AddTokenFlag(ctx, row.user, row.platform, row.token, 0); err != nil {
			t.Fatal(err)
		}
	}
	var fail atomic.Bool
	fail.Store(true)
	kick := func(_ context.Context, u string, p int, digest string) error {
		if u != user || p != 2 || digest != Digest("synthetic-old") {
			t.Fatal("kick scope changed")
		}
		if fail.Load() {
			return errors.New("fixture gateway unavailable")
		}
		return nil
	}
	service, err := New(ctx, db, rdb, kick)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := service.Begin(ctx, user, 2, "synthetic-old"); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n, err := db.Collection("session_logout_jobs").CountDocuments(ctx, bson.M{}); err != nil || n != 1 {
		t.Fatal("duplicate logout jobs", n, err)
	}
	job, err := service.Begin(ctx, user, 2, "synthetic-old")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Process(ctx, job); err == nil {
		t.Fatal("unavailable gateway became completed logout")
	}
	remaining, err := tokens.GetTokensWithoutError(ctx, user, 2)
	if err != nil || len(remaining) != 1 || remaining["synthetic-new"] != 0 {
		t.Fatal("revoked newer same-platform token", err)
	}
	for _, row := range []struct {
		user     string
		platform int
	}{{user, 5}, {foreign, 2}} {
		remaining, err := tokens.GetTokensWithoutError(ctx, row.user, row.platform)
		if err != nil || len(remaining) != 1 {
			t.Fatal("revoked unrelated session", err)
		}
	}
	var raw bson.M
	if err := db.Collection("session_logout_jobs").FindOne(ctx, bson.M{}).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	for _, value := range raw {
		if text, ok := value.(string); ok && strings.Contains(text, "synthetic-old") {
			t.Fatal("outbox persisted raw credential")
		}
	}
	fail.Store(false)
	restarted, err := New(ctx, db, rdb, kick)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Reconcile(ctx); err != nil {
		t.Fatal("restart did not complete pending logout", err)
	}
	if n, err := db.Collection("session_logout_jobs").CountDocuments(ctx, bson.M{"completed_at": bson.M{"$ne": nil}}); err != nil || n != 1 {
		t.Fatal("logout not durably completed", err)
	}
	if _, err := restarted.BeginDigest(ctx, user, 2, "invalid"); err == nil {
		t.Fatal("invalid digest accepted")
	}
	t.Log("LUOBO_EXACT_SESSION_LOGOUT_MONGO_REDIS_PASS")
}
