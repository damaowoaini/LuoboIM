package namespaceinventory

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// Prepared only: must be mandatory in a future targeted owned-fixture lane.
// This creates its OWN nonce DB and exact3 Redis claim; never an app DB/URI.
func TestNamespaceChunksOwnedPagingAndPrivateResume(t *testing.T) {
	mongoURI, redisURL, rs := os.Getenv("LUOBO_NAMESPACE_INVENTORY_TEST_MONGO_URI"), os.Getenv("LUOBO_NAMESPACE_INVENTORY_TEST_REDIS_URL"), os.Getenv("LUOBO_NAMESPACE_INVENTORY_TEST_REPLICA_SET")
	if mongoURI == "" && redisURL == "" && rs == "" {
		t.Skip("requires explicit fresh owned fixture; not integration PASS")
	}
	mu, e := url.Parse(mongoURI)
	if e != nil || mu.Scheme != "mongodb" || (mu.Host != "127.0.0.1:37017" && mu.Host != "127.0.0.1:38417") || !text(rs) {
		t.Fatal("owned Mongo fixture input invalid")
	}
	ru, e := url.Parse(redisURL)
	if e != nil || ru.Scheme != "redis" || ru.Host != "127.0.0.1:36379" {
		t.Fatal("owned Redis fixture input invalid")
	}
	rc, e := redis.ParseURL(redisURL)
	if e != nil || rc.DB != 15 {
		t.Fatal("owned Redis DB15 required")
	}
	rc.MaxRetries = -1
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	var active atomic.Bool
	var denied atomic.Int64
	monitor := &event.CommandMonitor{Started: func(_ context.Context, ev *event.CommandStartedEvent) {
		if active.Load() {
			switch ev.CommandName {
			case "hello", "find", "listCollections", "getMore", "killCursors":
			default:
				denied.Add(1)
			}
		}
	}}
	client, e := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI).SetReplicaSet(rs).SetDirect(false).SetReadPreference(readpref.Primary()).SetRetryReads(false).SetRetryWrites(false).SetMonitor(monitor))
	if e != nil {
		t.Fatal("owned Mongo connection failed")
	}
	t.Cleanup(func() {
		active.Store(false)
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if client.Disconnect(c) != nil {
			t.Error("owned Mongo disconnect failed")
		}
	})
	nonce := uuid.NewString()
	db := client.Database("namespace_chunks_" + strings.ReplaceAll(nonce, "-", ""))
	names, e := db.ListCollectionNames(ctx, bson.D{})
	if e != nil || len(names) != 0 {
		t.Fatal("fresh nonce DB required")
	}
	if _, e := db.Collection("_fixture_owner").InsertOne(ctx, bson.M{"_id": "claim", "nonce": nonce}); e != nil {
		t.Fatal("Mongo claim ACK required")
	}
	t.Cleanup(func() {
		active.Store(false)
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		var row struct {
			Nonce string `bson:"nonce"`
		}
		if db.Collection("_fixture_owner").FindOne(c, bson.M{"_id": "claim"}).Decode(&row) != nil || row.Nonce != nonce {
			t.Error("Mongo claim mismatch; retained")
			return
		}
		if db.Drop(c) != nil {
			t.Error("owned Mongo drop failed")
			return
		}
		names, e := client.ListDatabaseNames(c, bson.M{"name": db.Name()})
		if e != nil || len(names) != 0 {
			t.Error("owned DB remains")
		}
	})
	rdb := redis.NewClient(rc)
	rdb.AddHook(readonlyRedisHook{active: &active, denied: &denied})
	t.Cleanup(func() {
		active.Store(false)
		if rdb.Close() != nil {
			t.Error("owned Redis close failed")
		}
	})
	info, e := rdb.Info(ctx, "server").Result()
	runID, ok := infoValue(info, "run_id")
	if e != nil || !ok {
		t.Fatal("owned Redis identity required")
	}
	instance := uuid.NewString()
	if _, e := db.Collection("luobo_storage_identity").InsertOne(ctx, bson.M{"_id": "message-visibility-v1", "instance": instance, "tenant": "synthetic-tenant"}); e != nil {
		t.Fatal("identity setup failed")
	}
	ids := []interface{}{int32(7), int64(9), "z", primitive.NewObjectID()}
	docs := []interface{}{}
	for _, id := range ids {
		docs = append(docs, bson.M{"_id": id, "group_id": "synthetic-group", "marker": "original"})
	}
	if _, e := db.Collection("group").InsertMany(ctx, docs); e != nil {
		t.Fatal("mixed BSON _id setup failed")
	}
	keys := []string{"NAMESPACE_CHUNKS_FIXTURE:" + nonce, "MAX_SEQ:sg_" + nonce, "MESSAGE_CACHE:sg_" + nonce + "_1"}
	claim := `if redis.call('EXISTS',KEYS[1],KEYS[2],KEYS[3])~=0 then return 0 end redis.call('SET',KEYS[1],ARGV[1]);redis.call('SET',KEYS[2],'9');redis.call('RPUSH',KEYS[3],'synthetic-chunk-payload');return 1`
	if ack, e := rdb.Eval(ctx, claim, keys, nonce).Int64(); e != nil || ack != 1 {
		t.Fatal("Redis claim ACK missing; no cleanup grant")
	}
	t.Cleanup(func() {
		active.Store(false)
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		script := `if redis.call('GET',KEYS[1])~=ARGV[1] or redis.call('TYPE',KEYS[2]).ok~='string' or redis.call('GET',KEYS[2])~='9' or redis.call('TYPE',KEYS[3]).ok~='list' or redis.call('LLEN',KEYS[3])~=1 or redis.call('LINDEX',KEYS[3],0)~='synthetic-chunk-payload' then return -1 end return redis.call('DEL',KEYS[1],KEYS[2],KEYS[3])`
		if n, e := rdb.Eval(c, script, keys, nonce).Int64(); e != nil || n != 3 {
			t.Error("Redis exact cleanup failed; retained")
			return
		}
		if n, e := rdb.Exists(c, keys...).Result(); e != nil || n != 0 {
			t.Error("owned Redis keys remain")
		}
	})
	binding := Binding{Tenant: "synthetic-tenant", Database: db.Name(), StorageInstance: instance, ReplicaSet: rs, RedisRunID: runID, RedisDB: 15}
	limits := chunkFixtureLimits()
	source, e := NewLiveSource(db, rdb, binding, limits)
	if e != nil {
		t.Fatal("paged live source failed")
	}
	beforeNames, e := db.ListCollectionNames(ctx, bson.D{})
	if e != nil {
		t.Fatal("pre collection inventory failed")
	}
	sort.Strings(beforeNames)
	beforeDump, e := rdb.Dump(ctx, keys[2]).Result()
	if e != nil {
		t.Fatal("pre cache inventory failed")
	}
	t.Run("heterogeneous-id-pages-and-real-private-entry", func(t *testing.T) {
		dir := chunkDir(t)
		active.Store(true)
		out, e := BeginChunkCapture(ctx, source, binding, dir, limits, 1)
		for n := 0; e == nil && !out.ObservedComplete && n < 64; n++ {
			out, e = ResumeChunkCapture(ctx, source, binding, dir, 2)
		}
		active.Store(false)
		if e != nil || !out.ObservedComplete || out.MongoRows != int64(len(collections)+len(ids)+1) || out.RedisKeys != 2 || denied.Load() != 0 {
			t.Fatal("mixed-type keyset paging lost rows or attempted writes")
		}
		inspected, e := InspectChunks(ctx, dir)
		if e != nil || !reflect.DeepEqual(out, inspected) {
			t.Fatal("real page journal inspect failed")
		}
		private := privateFixtureDir(t)
		path := filepath.Join(private, "connection.json")
		output := filepath.Join(private, "operator-chunks")
		operatorMongo := *mu
		operatorMongo.Path = "/" + db.Name()
		q := operatorMongo.Query()
		for k := range q {
			if strings.EqualFold(k, "replicaSet") || strings.EqualFold(k, "directConnection") {
				q.Del(k)
			}
		}
		q.Set("replicaSet", rs)
		q.Set("directConnection", "false")
		operatorMongo.RawQuery = q.Encode()
		operatorRedis := *ru
		operatorRedis.Path = "/15"
		connection := PrivateConnection{Version: 1, Binding: binding, MongoURI: operatorMongo.String(), RedisURL: operatorRedis.String()}
		raw, e := json.Marshal(connection)
		if e != nil {
			t.Fatal("connection encoding failed")
		}
		writeFixture(t, path, raw)
		out, e = CaptureChunksPrivate(ctx, path, output, false, 1)
		if e != nil || out.ObservedComplete {
			t.Fatal("private chunk entry did not bound its first call")
		}
		out, e = CaptureChunksPrivate(ctx, path, output, true, 128)
		if e != nil || !out.ObservedComplete || out.State != State {
			t.Fatal("private resume did not finish untrusted observation")
		}
		inspected, e = InspectChunks(ctx, output)
		if e != nil || !reflect.DeepEqual(out, inspected) {
			t.Fatal("operator saved journal differs from inspect")
		}
		// These independent operator clients do not inherit the read-only
		// hooks. Their collection/cache/count preservation is checked below.
	})
	t.Run("real-write-between-passes-is-rejected", func(t *testing.T) {
		dir := chunkDir(t)
		active.Store(true)
		_, e := BeginChunkCapture(ctx, source, binding, dir, limits, 1)
		if e != nil {
			t.Fatal("drift capture start failed")
		}
		for n := 0; n < 64; n++ {
			s, e := loadSession(dir)
			if e != nil {
				t.Fatal(e)
			}
			p, e := replayJournal(ctx, dir, s, func(Page, int) error { return nil })
			if e != nil {
				t.Fatal(e)
			}
			if p.Pass == 2 {
				break
			}
			if _, e := ResumeChunkCapture(ctx, source, binding, dir, 1); e != nil {
				t.Fatal("first pass failed")
			}
			if n == 63 {
				t.Fatal("first pass did not finish")
			}
		}
		active.Store(false)
		changed, e := db.Collection("group").UpdateOne(ctx, bson.M{"_id": ids[0]}, bson.M{"$set": bson.M{"marker": "changed"}})
		if e != nil || changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
			t.Fatal("drift barrier prerequisite failed")
		}
		active.Store(true)
		_, e = ResumeChunkCapture(ctx, source, binding, dir, 128)
		active.Store(false)
		if !errors.Is(e, ErrChanged) || denied.Load() != 0 {
			t.Fatal("actual changed BSON was accepted or capture wrote data")
		}
		if _, e := InspectChunks(ctx, dir); e != nil {
			t.Fatal("failed verification destroyed committed first pass")
		}
	})
	t.Run("bounded-progress-private-entry", func(t *testing.T) {
		private := privateFixtureDir(t)
		path := filepath.Join(private, "connection.json")
		output := filepath.Join(private, "resumable")
		operatorMongo := *mu
		operatorMongo.Path = "/" + db.Name()
		q := operatorMongo.Query()
		for k := range q {
			if strings.EqualFold(k, "replicaSet") || strings.EqualFold(k, "directConnection") {
				q.Del(k)
			}
		}
		q.Set("replicaSet", rs)
		q.Set("directConnection", "false")
		operatorMongo.RawQuery = q.Encode()
		operatorRedis := *ru
		operatorRedis.Path = "/15"
		connection := PrivateConnection{Version: 1, Binding: binding, MongoURI: operatorMongo.String(), RedisURL: operatorRedis.String()}
		raw, e := json.Marshal(connection)
		if e != nil {
			t.Fatal("bounded connection encoding failed")
		}
		writeFixture(t, path, raw)
		out, e := CaptureResumablePrivate(ctx, path, output, false, 1)
		if e != nil || out.RecordedTwoPassComplete {
			t.Fatal("bounded private first invocation contract")
		}
		for n := 0; e == nil && !out.RecordedTwoPassComplete && n < 64; n++ {
			out, e = CaptureResumablePrivate(ctx, path, output, true, 32)
		}
		if e != nil || !out.RecordedTwoPassComplete || out.State != State || out.CurrentFullCustodyReverified || out.RecordedSummary.MongoRows != int64(len(collections)+len(ids)+1) || out.RecordedSummary.RedisKeys != 2 {
			t.Fatal("actual bounded private operator failed or claimed fresh custody/authority")
		}
		inspected, e := InspectResumableProgress(output)
		if e != nil || !reflect.DeepEqual(out, inspected) {
			t.Fatal("real operator progress head differs from inspect")
		}
		// The actual independent factory opens and closes its own connections;
		// it does not inherit the original hook. Common postchecks below prove
		// namespace/cache preservation, not a fake inherited hook assertion.
	})
	afterNames, e := db.ListCollectionNames(ctx, bson.D{})
	if e != nil {
		t.Fatal("post collection inventory failed")
	}
	sort.Strings(afterNames)
	afterDump, e := rdb.Dump(ctx, keys[2]).Result()
	if e != nil || beforeDump != afterDump || !reflect.DeepEqual(beforeNames, afterNames) {
		t.Fatal("paged/operator paths changed cache or collections")
	}
	if n, e := db.Collection("group").CountDocuments(ctx, bson.D{}); e != nil || n != int64(len(ids)) {
		t.Fatal("paged capture changed record count")
	}
}
