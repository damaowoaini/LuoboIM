package namespaceinventory

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

type readonlyRedisHook struct {
	active *atomic.Bool
	denied *atomic.Int64
}

func (h readonlyRedisHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h readonlyRedisHook) permit(cmd redis.Cmder) bool {
	name := strings.ToLower(cmd.Name())
	return name == "info" || name == "scan" || name == "eval_ro"
}
func (h readonlyRedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.active.Load() && !h.permit(cmd) {
			h.denied.Add(1)
			return ErrInvalid
		}
		return next(ctx, cmd)
	}
}
func (h readonlyRedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, commands []redis.Cmder) error {
		if h.active.Load() {
			for _, cmd := range commands {
				if !h.permit(cmd) {
					h.denied.Add(1)
					return ErrInvalid
				}
			}
		}
		return next(ctx, commands)
	}
}

// Prepared only. A future targeted controller must supply its fresh ephemeral
// RS/Redis fixture explicitly and make this test mandatory. No default endpoint
// or application config is read; omission is not a successful integration run.
func TestNamespaceInventoryOwnedStoresReadOnly(t *testing.T) {
	mongoURI, redisURL, rs := os.Getenv("LUOBO_NAMESPACE_INVENTORY_TEST_MONGO_URI"), os.Getenv("LUOBO_NAMESPACE_INVENTORY_TEST_REDIS_URL"), os.Getenv("LUOBO_NAMESPACE_INVENTORY_TEST_REPLICA_SET")
	if mongoURI == "" && redisURL == "" && rs == "" {
		t.Skip("requires owned ephemeral namespace inventory fixture; not integration PASS")
	}
	mu, e := url.Parse(mongoURI)
	if e != nil || mu.Scheme != "mongodb" || (mu.Host != "127.0.0.1:37017" && mu.Host != "127.0.0.1:38417") || !text(rs) {
		t.Fatal("invalid owned Mongo fixture input")
	}
	ru, e := url.Parse(redisURL)
	if e != nil || (ru.Scheme != "redis" && ru.Scheme != "rediss") || ru.Host != "127.0.0.1:36379" {
		t.Fatal("invalid owned Redis fixture input")
	}
	if host, _, e := net.SplitHostPort(mu.Host); e != nil || host != "127.0.0.1" {
		t.Fatal("Mongo fixture is not fixed loopback")
	}
	redisConfig, e := redis.ParseURL(redisURL)
	if e != nil || redisConfig.DB != 15 {
		t.Fatal("Redis fixture database must be 15")
	}
	redisConfig.MaxRetries = -1
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var active atomic.Bool
	var denied atomic.Int64
	monitor := &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if !active.Load() {
			return
		}
		switch e.CommandName {
		case "hello", "find", "listCollections", "getMore", "killCursors":
		default:
			denied.Add(1)
		}
	}}
	client, e := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI).SetReplicaSet(rs).SetDirect(false).SetReadPreference(readpref.Primary()).SetRetryReads(false).SetRetryWrites(false).SetMonitor(monitor))
	if e != nil {
		t.Fatal("owned Mongo connect failed")
	}
	t.Cleanup(func() {
		active.Store(false)
		closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if client.Disconnect(closeCtx) != nil {
			t.Error("owned Mongo disconnect failed")
		}
	})
	nonce := uuid.NewString()
	db := client.Database("namespace_inventory_" + strings.ReplaceAll(nonce, "-", ""))
	names, e := db.ListCollectionNames(ctx, bson.D{})
	if e != nil || len(names) != 0 {
		t.Fatal("fresh nonce database prerequisite failed")
	}
	if _, e := db.Collection("_fixture_owner").InsertOne(ctx, bson.M{"_id": "claim", "nonce": nonce}); e != nil {
		t.Fatal("owned Mongo claim ACK missing")
	}
	t.Cleanup(func() {
		active.Store(false)
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		var claim struct {
			Nonce string `bson:"nonce"`
		}
		if db.Collection("_fixture_owner").FindOne(cleanup, bson.M{"_id": "claim"}).Decode(&claim) != nil || claim.Nonce != nonce {
			t.Error("owned Mongo cleanup ownership mismatch; retained")
			return
		}
		if db.Drop(cleanup) != nil {
			t.Error("owned Mongo drop failed")
			return
		}
		remaining, e := client.ListDatabaseNames(cleanup, bson.M{"name": db.Name()})
		if e != nil || len(remaining) != 0 {
			t.Error("owned Mongo database remains")
		}
	})
	rdb := redis.NewClient(redisConfig)
	rdb.AddHook(readonlyRedisHook{active: &active, denied: &denied})
	t.Cleanup(func() {
		active.Store(false)
		if rdb.Close() != nil {
			t.Error("owned Redis close failed")
		}
	})
	server, e := rdb.Info(ctx, "server").Result()
	if e != nil {
		t.Fatal("owned Redis identity unavailable")
	}
	run, ok := infoValue(server, "run_id")
	if !ok {
		t.Fatal("owned Redis identity malformed")
	}
	instance := uuid.NewString()
	if _, e := db.Collection("luobo_storage_identity").InsertOne(ctx, bson.M{"_id": "message-visibility-v1", "instance": instance, "tenant": "synthetic-tenant"}); e != nil {
		t.Fatal("owned identity fixture failed")
	}
	gid := "capture_g_" + strings.ReplaceAll(nonce, "-", "")
	cid := "sg_" + gid
	doc := table.MsgDocModel{DocID: cid + ":0", Msg: []*table.MsgInfoModel{{Msg: &table.MsgDataModel{SendID: "synthetic-owner", RecvID: gid, GroupID: gid, SessionType: constant.SuperGroupChatType, Options: msgprocessor.NewOptions(msgprocessor.WithNotNotification(true)), Seq: 1}}, {}, nil}}
	if _, e := db.Collection("msg").InsertOne(ctx, doc); e != nil {
		t.Fatal("owned message fixture failed")
	}
	keys := []string{"NAMESPACE_INVENTORY_FIXTURE:" + nonce, "MAX_SEQ:" + cid, "MESSAGE_CACHE:" + cid + "_1"}
	claimScript := `if redis.call('EXISTS',KEYS[1],KEYS[2],KEYS[3])~=0 then return 0 end redis.call('SET',KEYS[1],ARGV[1]);redis.call('SET',KEYS[2],'9');redis.call('RPUSH',KEYS[3],'synthetic-private-body');return 1`
	ack, e := rdb.Eval(ctx, claimScript, keys, nonce).Int64()
	if e != nil || ack != 1 {
		t.Fatal("owned Redis claim ACK missing; no cleanup grant")
	}
	t.Cleanup(func() {
		active.Store(false)
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		script := `if redis.call('GET',KEYS[1])~=ARGV[1] or redis.call('TYPE',KEYS[2]).ok~='string' or redis.call('GET',KEYS[2])~='9' or redis.call('TYPE',KEYS[3]).ok~='list' or redis.call('LLEN',KEYS[3])~=1 or redis.call('LINDEX',KEYS[3],0)~='synthetic-private-body' then return -1 end return redis.call('DEL',KEYS[1],KEYS[2],KEYS[3])`
		n, e := rdb.Eval(cleanup, script, keys, nonce).Int64()
		if e != nil || n != 3 {
			t.Error("owned Redis exact cleanup failed; retained")
			return
		}
		if n, e := rdb.Exists(cleanup, keys...).Result(); e != nil || n != 0 {
			t.Error("owned Redis keys remain")
		}
	})
	beforeNames, e := db.ListCollectionNames(ctx, bson.D{})
	if e != nil {
		t.Fatal("fixture pre-inventory failed")
	}
	sort.Strings(beforeNames)
	beforeDump, e := rdb.Dump(ctx, keys[2]).Result()
	if e != nil {
		t.Fatal("fixture pre-cache read failed")
	}
	binding := Binding{Tenant: "synthetic-tenant", Database: db.Name(), StorageInstance: instance, ReplicaSet: rs, RedisRunID: run, RedisDB: 15}
	source, e := NewLiveSource(db, rdb, binding, DefaultLimits())
	if e != nil {
		t.Fatal("live source construction failed")
	}
	active.Store(true)
	snapshot, e := Capture(ctx, source, binding, DefaultLimits())
	active.Store(false)
	if e != nil {
		t.Fatal("real read-only capture failed")
	}
	if denied.Load() != 0 {
		t.Fatal("capture attempted a non-read command")
	}
	if snapshot.Public().State != State || snapshot.Public().ErasedSlots != 1 || len(snapshot.Redis) != 2 {
		t.Fatal("real current evidence contract failed")
	}
	found := false
	for _, o := range snapshot.Observations {
		if o.CID == cid && o.RawHead != nil {
			if *o.RawHead != 9 {
				t.Fatal("raw Redis head was replaced by observed message max")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("raw head missing")
	}
	// Exercise the actual independent operator entry, not a synthetic Source:
	// a 0600 explicit connection file opens its own clients, captures the same
	// nonce database/DB15, saves exclusively, and returns after checked closes.
	// The original LiveSource command monitor/read-only Redis hook above is
	// preserved; these entry clients are additionally checked by raw inventory
	// and cache/collection comparison, not claimed to inherit the old hooks.
	privateDir := privateFixtureDir(t)
	connectionPath := filepath.Join(privateDir, "connection.json")
	snapshotPath := filepath.Join(privateDir, "snapshot.json")
	operatorMongo := *mu
	operatorMongo.Path = "/" + db.Name()
	query := operatorMongo.Query()
	for name := range query {
		if strings.EqualFold(name, "replicaSet") || strings.EqualFold(name, "directConnection") {
			query.Del(name)
		}
	}
	query.Set("replicaSet", rs)
	query.Set("directConnection", "false")
	operatorMongo.RawQuery = query.Encode()
	operatorRedis := *ru
	operatorRedis.Path = "/15"
	connection := PrivateConnection{Version: 1, Binding: binding, MongoURI: operatorMongo.String(), RedisURL: operatorRedis.String()}
	connectionRaw, e := json.Marshal(connection)
	if e != nil {
		t.Fatal("private operator connection encoding failed")
	}
	writeFixture(t, connectionPath, connectionRaw)
	operatorSummary, e := CapturePrivate(ctx, connectionPath, snapshotPath)
	if e != nil {
		t.Fatal("private operator capture/save/close failed")
	}
	inspected, e := Inspect(snapshotPath)
	if e != nil || !reflect.DeepEqual(inspected, operatorSummary) || inspected.State != State {
		t.Fatal("private operator inspect summary or state mismatch")
	}
	savedRaw, e := ReadPrivateDocument(snapshotPath, MaxPrivateSnapshot)
	if e != nil {
		t.Fatal("private operator snapshot unavailable")
	}
	var saved Snapshot
	if DecodeStrict(savedRaw, &saved) != nil || saved.SHA != operatorSummary.SnapshotSHA || saved.State != State || saved.Identity.Binding != binding {
		t.Fatal("private operator snapshot digest/binding mismatch")
	}
	if !reflect.DeepEqual(saved.Mongo, snapshot.Mongo) || !reflect.DeepEqual(saved.Redis, snapshot.Redis) {
		t.Fatal("operator path changed or replaced current evidence")
	}
	afterNames, e := db.ListCollectionNames(ctx, bson.D{})
	if e != nil {
		t.Fatal("fixture post-inventory failed")
	}
	sort.Strings(afterNames)
	afterDump, e := rdb.Dump(ctx, keys[2]).Result()
	if e != nil || afterDump != beforeDump || !reflect.DeepEqual(beforeNames, afterNames) {
		t.Fatal("capture changed collections or cache")
	}
	if n, e := db.Collection("msg").CountDocuments(ctx, bson.D{}); e != nil || n != 1 {
		t.Fatal("capture changed message document count")
	}
}
