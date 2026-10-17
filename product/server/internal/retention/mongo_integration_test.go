package retention_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/retention"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mongotx"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	ut "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var clock = time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)

type mongoFixture struct {
	db    *mongo.Database
	store *retention.MongoStore
	dao   *mgo.ConversationMgo
	ctx   context.Context
}

const retentionMongoFixtureURI = "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration"

func allowedMongoFixtureURI(uri string) bool { return uri == retentionMongoFixtureURI }

// Only the task-owned local replica set is eligible. Every test proves its
// random DB absent, exclusively creates a task-claim collection, then owns that
// DB only after its claim insert ACK. Cleanup never drops an unclaimed DB.
func fixture(t *testing.T, monitor *event.CommandMonitor) *mongoFixture {
	t.Helper()
	uri := os.Getenv("LUOBO_RETENTION_TEST_MONGO")
	if uri == "" {
		t.Skip("explicit task replica-set fixture not authorized/configured")
	}
	if !allowedMongoFixtureURI(uri) {
		t.Fatal("exact task Mongo fixture URI required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	clientOptions := options.Client().ApplyURI(uri).SetDirect(true).SetServerSelectionTimeout(2 * time.Second).SetConnectTimeout(2 * time.Second).SetMonitor(monitor)
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		t.Fatal("task Mongo connection failed")
	}
	t.Cleanup(func() {
		end, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Disconnect(end); err != nil {
			t.Errorf("task Mongo disconnect unconfirmed: %v", err)
		}
	})
	var topology bson.M
	if err = client.Database("admin").RunCommand(ctx, bson.M{"hello": 1}).Decode(&topology); err != nil || topology["setName"] != "luoboIntegration" {
		t.Fatal("exact task replica set required; no standalone fallback")
	}
	var nonce [24]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal("fixture nonce failed")
	}
	name := "luobo_retention_test_" + hex.EncodeToString(nonce[:12])
	claim := hex.EncodeToString(nonce[12:])
	existing, err := client.ListDatabaseNames(ctx, bson.M{"name": name})
	if err != nil || len(existing) != 0 {
		t.Fatal("new task database absence unconfirmed; no write authorized")
	}
	db := client.Database(name)
	if err = db.CreateCollection(ctx, "retention_fixture_claim"); err != nil {
		t.Fatal("exclusive task claim collection creation failed; no database drop authorized")
	}
	if _, err = db.Collection("retention_fixture_claim").InsertOne(ctx, bson.M{"_id": "task-claim", "nonce": claim}); err != nil {
		t.Fatal("task ownership claim unconfirmed; database retained")
	}
	t.Cleanup(func() {
		end, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if db.Name() != name || !strings.HasPrefix(name, "luobo_retention_test_") || len(name) != len("luobo_retention_test_")+24 {
			t.Error("unsafe fixture cleanup refused")
			return
		}
		var record bson.M
		if err := db.Collection("retention_fixture_claim").FindOne(end, bson.M{"_id": "task-claim", "nonce": claim}).Decode(&record); err != nil {
			t.Errorf("task ownership unavailable; database retained: %v", err)
			return
		}
		if err := db.Drop(end); err != nil {
			t.Errorf("task database cleanup unconfirmed: %v", err)
			return
		}
		names, err := client.ListDatabaseNames(end, bson.M{"name": name})
		if err != nil || len(names) != 0 {
			t.Errorf("task database absence after cleanup unconfirmed: matches=%d error=%v", len(names), err)
		}
	})
	store := retention.NewMongoStore(db)
	store.Now = func() time.Time { return clock }
	// This new claimed random database has an explicit empty legacy Redis baseline.
	store.LegacyFloors = func(context.Context, string, string) (int64, int64, error) { return 0, 0, nil }
	if err = store.EnsureIndexes(ctx); err != nil {
		t.Fatal("fixture indexes failed")
	}
	dao, err := mgo.NewConversationMongo(db)
	if err != nil {
		t.Fatal("fixture conversation DAO failed")
	}
	return &mongoFixture{db: db, store: store, dao: dao, ctx: ctx}
}

func TestRetentionMongoFixtureRejectsAlternateAuthorityOrOptionsWithoutConnecting(t *testing.T) {
	for _, uri := range []string{
		"mongodb://localhost:37017/?replicaSet=luoboIntegration",
		"mongodb://127.0.0.1:27017/?replicaSet=luoboIntegration",
		"mongodb://127.0.0.1:37017/business?replicaSet=luoboIntegration",
		"mongodb://127.0.0.1:37017/?replicaSet=other",
		"mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration&proxyHost=127.0.0.1",
		"mongodb://fixture:synthetic@127.0.0.1:37017/?replicaSet=luoboIntegration",
		retentionMongoFixtureURI + "#fragment",
	} {
		if allowedMongoFixtureURI(uri) {
			t.Fatal("nonexact task fixture URI allowed")
		}
	}
	if !allowedMongoFixtureURI(retentionMongoFixtureURI) {
		t.Fatal("exact task fixture URI rejected")
	}
}

func (f *mongoFixture) seed(t *testing.T) {
	t.Helper()
	p := &relation.ConversationModel{OwnerUserID: "alice", ConversationID: "si_alice_bob", UserID: "bob", ConversationType: 1, IsMsgDestruct: true, MsgDestructTime: 30}
	if err := f.dao.Create(f.ctx, []*relation.ConversationModel{p}); err != nil {
		t.Fatal("fixture policy create failed")
	}
	slots := make([]*ut.MsgInfoModel, 100)
	slots[0] = &ut.MsgInfoModel{Msg: &ut.MsgDataModel{Seq: 1, SendTime: clock.Add(-time.Minute).UnixMilli()}}
	slots[1] = &ut.MsgInfoModel{Msg: &ut.MsgDataModel{Seq: 2, SendTime: clock.UnixMilli()}}
	if _, err := f.db.Collection("msg").InsertOne(f.ctx, ut.MsgDocModel{DocID: "si_alice_bob:0", Msg: slots}); err != nil {
		t.Fatal("fixture metadata create failed")
	}
}

func (f *mongoFixture) plan(t *testing.T) retention.Plan {
	t.Helper()
	p, err := f.store.ReadPolicy(f.ctx, "alice", "si_alice_bob")
	if err != nil {
		t.Fatal("fixture policy read failed")
	}
	floor, err := f.store.Floor(f.ctx, p)
	if err != nil {
		t.Fatal("fixture floor read failed")
	}
	plan, err := retention.BuildPlan(f.ctx, f.store, p, floor, clock, retention.MaximumChunk)
	if err != nil {
		t.Fatal("fixture plan failed")
	}
	if plan.FromFloor != 1 || plan.ToFloor != 2 {
		t.Fatalf("unexpected synthetic prefix %d→%d", plan.FromFloor, plan.ToFloor)
	}
	return plan
}

func (f *mongoFixture) assertState(t *testing.T, wantFloor int64, wantEvents int64) {
	t.Helper()
	p, err := f.store.ReadPolicy(f.ctx, "alice", "si_alice_bob")
	if err != nil {
		t.Fatal("fixture policy read failed")
	}
	floor, err := f.store.Floor(f.ctx, p)
	if err != nil || floor != wantFloor {
		t.Fatalf("floor=%d want=%d error=%v", floor, wantFloor, err)
	}
	count, err := f.db.Collection(retention.OutboxCollection).CountDocuments(f.ctx, bson.M{})
	if err != nil || count != wantEvents {
		t.Fatalf("event count=%d want=%d", count, wantEvents)
	}
}

func TestRetentionMongoOffExtensionAndABADenyOldSnapshot(t *testing.T) {
	for _, action := range []string{"off", "extend", "off_on_ABA"} {
		t.Run(action, func(t *testing.T) {
			f := fixture(t, nil)
			f.seed(t)
			plan := f.plan(t)
			changes := []map[string]any{{"is_msg_destruct": false}}
			if action == "extend" {
				changes = []map[string]any{{"msg_destruct_time": int64(3600)}}
			}
			if action == "off_on_ABA" {
				changes = append(changes, map[string]any{"is_msg_destruct": true})
			}
			for _, fields := range changes {
				if _, err := f.dao.UpdateByMap(f.ctx, []string{"alice"}, "si_alice_bob", fields); err != nil {
					t.Fatal("synthetic policy update failed")
				}
			}
			if err := f.store.Commit(f.ctx, plan); !errors.Is(err, retention.ErrPolicyChanged) {
				t.Fatalf("old plan was not rejected: %T", err)
			}
			f.assertState(t, 1, 0)
		})
	}
}

func TestRetentionMongoRealPolicyDocumentConflictPreventsWriteSkew(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	monitor := &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName != "update" {
			return
		}
		updates, ok := e.Command.Lookup("updates").ArrayOK()
		if !ok {
			return
		}
		values, err := updates.Values()
		if err != nil {
			return
		}
		for _, v := range values {
			q, ok := v.Document().Lookup("q").DocumentOK()
			if !ok {
				return
			}
			if _, err = q.LookupErr("retention_policy_revision"); err != nil {
				continue
			}
			if _, err = q.LookupErr("is_msg_destruct"); err != nil {
				continue
			}
			once.Do(func() { close(entered); <-release })
		}
	}}
	f := fixture(t, monitor)
	f.seed(t)
	plan := f.plan(t)
	result := make(chan error, 1)
	go func() { result <- f.store.Commit(f.ctx, plan) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("policy CAS was not reached")
	}
	_, err := f.dao.UpdateByMap(f.ctx, []string{"alice"}, "si_alice_bob", map[string]any{"is_msg_destruct": false})
	close(release)
	if err != nil {
		t.Fatal("winning disable failed")
	}
	select {
	case err := <-result:
		if !errors.Is(err, retention.ErrPolicyChanged) {
			t.Fatalf("stale transaction survived: %T", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bounded commit did not settle")
	}
	f.assertState(t, 1, 0)
}

func TestRetentionMongoCommittedPrefixSurvivesLaterDisableAndOtherOwnerUnchanged(t *testing.T) {
	f := fixture(t, nil)
	f.seed(t)
	plan := f.plan(t)
	if err := f.store.Commit(f.ctx, plan); err != nil {
		t.Fatal("synthetic commit failed")
	}
	if _, err := f.dao.UpdateByMap(f.ctx, []string{"alice"}, "si_alice_bob", map[string]any{"is_msg_destruct": false}); err != nil {
		t.Fatal("synthetic disable failed")
	}
	if err := f.store.Commit(f.ctx, plan); !errors.Is(err, retention.ErrPolicyChanged) {
		t.Fatal("old chunk survived disable")
	}
	f.assertState(t, 2, 1)
	count, err := f.db.Collection("message_visibility_floors").CountDocuments(f.ctx, bson.M{"user_id": bson.M{"$ne": "alice"}})
	if err != nil || count != 0 {
		t.Fatal("peer/global floor changed")
	}
}

func TestRetentionMongoOutboxFailureRollsBackPolicyFloorAndSourceFence(t *testing.T) {
	f := fixture(t, nil)
	f.seed(t)
	plan := f.plan(t)
	b, _ := json.Marshal([]any{plan.Policy.Owner, plan.Policy.Conversation, plan.Policy.Revision, plan.Policy.Generation, plan.ToFloor})
	sum := sha256.Sum256(b)
	if _, err := f.db.Collection(retention.OutboxCollection).InsertOne(f.ctx, bson.M{"_id": hex.EncodeToString(sum[:])}); err != nil {
		t.Fatal("synthetic collision setup failed")
	}
	if err := f.store.Commit(f.ctx, plan); err == nil {
		t.Fatal("injected outbox failure accepted")
	}
	f.assertState(t, 1, 1)
	p, err := f.store.ReadPolicy(f.ctx, "alice", "si_alice_bob")
	if err != nil || p.Generation != 0 || !p.LastScan.IsZero() {
		t.Fatal("policy progress escaped rollback")
	}
	count, err := f.db.Collection("msg").CountDocuments(f.ctx, bson.M{"retention_scan_fence": bson.M{"$exists": true}})
	if err != nil || count != 0 {
		t.Fatal("source fence escaped rollback")
	}
}

type effectsFixture struct {
	fail                 bool
	syncs, notifications int
}

func (e *effectsFixture) SyncFloor(context.Context, string, string, int64) error {
	e.syncs++
	if e.fail {
		return errors.New("synthetic cache offline")
	}
	return nil
}
func (e *effectsFixture) Notify(context.Context, string, string, []int64) error {
	e.notifications++
	return nil
}

func TestRetentionMongoPendingOutboxRecoversAfterRestartWithoutPolicyReplay(t *testing.T) {
	f := fixture(t, nil)
	f.seed(t)
	plan := f.plan(t)
	if err := f.store.Commit(f.ctx, plan); err != nil {
		t.Fatal("synthetic commit failed")
	}
	effects := &effectsFixture{fail: true}
	n, err := f.store.Drain(f.ctx, effects, 10)
	if err == nil || n != 0 || effects.notifications != 0 {
		t.Fatal("failed cache falsely completed notification")
	}
	restarted := retention.NewMongoStore(f.db)
	restarted.Now = func() time.Time { return clock.Add(31 * time.Second) }
	effects.fail = false
	n, err = restarted.Drain(f.ctx, effects, 10)
	if err != nil || n != 1 || effects.notifications != 1 {
		t.Fatal("durable pending event not recovered")
	}
	n, err = restarted.Drain(f.ctx, effects, 10)
	if err != nil || n != 0 {
		t.Fatal("completed event replayed")
	}
	p, err := f.store.ReadPolicy(f.ctx, "alice", "si_alice_bob")
	if err != nil || p.Generation != 1 || p.Revision != 1 {
		t.Fatal("outbox replay changed policy")
	}
}

func TestRetentionMongoFullDAOUpdateAndStableOwnerCIDPaging(t *testing.T) {
	f := fixture(t, nil)
	f.seed(t)
	full := &relation.ConversationModel{OwnerUserID: "alice", ConversationID: "si_alice_bob", UserID: "bob", ConversationType: 1, IsMsgDestruct: true, MsgDestructTime: 3600, LatestMsgDestructTime: clock.Add(24 * time.Hour)}
	if err := f.dao.Update(f.ctx, full); err != nil {
		t.Fatal("full model update rejected server-owned field adaptation")
	}
	p, err := f.store.ReadPolicy(f.ctx, "alice", "si_alice_bob")
	if err != nil || p.Revision != 2 || p.Duration != 3600 || !p.LastScan.IsZero() {
		t.Fatal("full setter lost revision/progress boundary")
	}
	rows := make([]*relation.ConversationModel, 0, 130)
	for i := 0; i < 130; i++ {
		rows = append(rows, &relation.ConversationModel{OwnerUserID: fmt.Sprintf("fixture_%03d", i), ConversationID: fmt.Sprintf("shared_%d", i%3), IsMsgDestruct: true, MsgDestructTime: 60})
	}
	if err := f.dao.Create(f.ctx, rows); err != nil {
		t.Fatal("paging fixture create failed")
	}
	seen := map[string]bool{}
	cursor := retention.Cursor{}
	for i := 0; i < 5; i++ {
		page, err := f.store.Page(f.ctx, cursor, 50)
		if err != nil {
			t.Fatal("stable page failed")
		}
		if len(page) == 0 {
			break
		}
		for _, row := range page {
			key := row.Owner + "/" + row.Conversation
			if seen[key] {
				t.Fatal("owner/CID duplicated across pages")
			}
			seen[key] = true
			cursor.Owner, cursor.Conversation = row.Owner, row.Conversation
		}
	}
	if len(seen) != 131 {
		t.Fatalf("stable pages covered %d, want131", len(seen))
	}
	count, err := f.dao.GetAllConversationIDsNumber(f.ctx)
	if err != nil || count != 4 {
		t.Fatalf("distinctCID count=%d want4", count)
	}
}

func TestRetentionMongoUnmigratedLegacyFloorDeniesPlanAndPreparation(t *testing.T) {
	f := fixture(t, nil)
	f.seed(t)
	if _, err := f.db.Collection("conversation").UpdateOne(f.ctx, bson.M{"owner_user_id": "alice"}, bson.M{"$unset": bson.M{"retention_policy_revision": ""}}); err != nil {
		t.Fatal("synthetic old row setup failed")
	}
	f.store.LegacyFloors = func(context.Context, string, string) (int64, int64, error) { return 500, 0, nil }
	p, err := f.store.ReadPolicy(f.ctx, "alice", "si_alice_bob")
	if err != nil {
		t.Fatal("old fixture read failed")
	}
	if _, err = f.store.Floor(f.ctx, p); !errors.Is(err, retention.ErrUnavailable) {
		t.Fatal("unmigrated Redis restriction was ignored")
	}
	_, changed, err := f.store.PrepareLegacyPage(f.ctx, retention.Cursor{}, 50)
	if !errors.Is(err, retention.ErrUnavailable) || changed != 0 {
		t.Fatal("unknown legacy floor was silently prepared")
	}
	p, err = f.store.ReadPolicy(f.ctx, "alice", "si_alice_bob")
	if err != nil || p.Revision != 0 {
		t.Fatal("preparation changed an unverified row")
	}
}

type noCache struct {
	cache.ConversationCache
	execs int
	after func(context.Context) error
}

func (c *noCache) NewCache() cache.ConversationCache                              { return c }
func (c *noCache) DelConversations(string, ...string) cache.ConversationCache     { return c }
func (c *noCache) DelUsersConversation(string, ...string) cache.ConversationCache { return c }
func (c *noCache) DelConversationIDs(...string) cache.ConversationCache           { return c }
func (c *noCache) DelUserConversationIDsHash(...string) cache.ConversationCache   { return c }
func (c *noCache) DelConversationNotReceiveMessageUserIDs(...string) cache.ConversationCache {
	return c
}
func (c *noCache) ExecDel(ctx context.Context, _ ...bool) error {
	c.execs++
	if c.after != nil {
		return c.after(ctx)
	}
	return nil
}

func TestRetentionMongoFullControllerSetterUpdatesExistingAndCreatesMissingOnce(t *testing.T) {
	f := fixture(t, nil)
	f.seed(t)
	db := controller.NewConversationDatabase(f.dao, &noCache{}, mongotx.New(f.db.Client()))
	rows := []*relation.ConversationModel{
		{OwnerUserID: "alice", ConversationID: "si_alice_bob", UserID: "bob", ConversationType: 1, IsMsgDestruct: true, MsgDestructTime: 3600, LatestMsgDestructTime: clock},
		{OwnerUserID: "alice", ConversationID: "si_alice_charlie", UserID: "charlie", ConversationType: 1},
	}
	if err := db.SetUserConversations(f.ctx, "alice", rows); err != nil {
		t.Fatal("full controller setter did not complete")
	}
	for _, tc := range []struct {
		cid      string
		revision int64
	}{{"si_alice_bob", 2}, {"si_alice_charlie", 1}} {
		p, err := f.store.ReadPolicy(f.ctx, "alice", tc.cid)
		if err != nil || p.Revision != tc.revision || !p.LastScan.IsZero() {
			t.Fatal("full setter did not preserve server metadata")
		}
	}
	count, err := f.db.Collection("conversation").CountDocuments(f.ctx, bson.M{})
	if err != nil || count != 2 {
		t.Fatal("mixed existing/new rows duplicated")
	}
}

func TestRetentionMongoTwoWorkersCommitOneChunkOnly(t *testing.T) {
	f := fixture(t, nil)
	f.seed(t)
	plan := f.plan(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; results <- f.store.Commit(f.ctx, plan) }()
	}
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err == nil {
				success++
			} else if !errors.Is(err, retention.ErrPolicyChanged) {
				t.Fatalf("unexpected concurrent outcome: %T", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not settle")
		}
	}
	if success != 1 {
		t.Fatalf("successful commits=%d want1", success)
	}
	f.assertState(t, 2, 1)
}

func TestRetentionMongoMessageWriteAfterPlanningConflictsBeforeFloor(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	monitor := &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName != "update" {
			return
		}
		updates, ok := e.Command.Lookup("updates").ArrayOK()
		if !ok {
			return
		}
		values, err := updates.Values()
		if err != nil {
			return
		}
		for _, v := range values {
			q, ok := v.Document().Lookup("q").DocumentOK()
			if !ok {
				return
			}
			if _, err = q.LookupErr("is_msg_destruct"); err == nil {
				once.Do(func() { close(entered); <-release })
			}
		}
	}}
	f := fixture(t, monitor)
	f.seed(t)
	plan := f.plan(t)
	result := make(chan error, 1)
	go func() { result <- f.store.Commit(f.ctx, plan) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("policy CAS was not reached")
	}
	_, err := f.db.Collection("msg").UpdateOne(f.ctx, bson.M{"doc_id": "si_alice_bob:0"}, bson.M{"$set": bson.M{"msgs.0.msg.send_time": clock.UnixMilli()}})
	close(release)
	if err != nil {
		t.Fatal("synthetic concurrent persistence failed")
	}
	select {
	case err := <-result:
		if !errors.Is(err, retention.ErrSourceChanged) {
			t.Fatalf("changed message source was not rejected: %T", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bounded source conflict did not settle")
	}
	f.assertState(t, 1, 0)
}

func TestRetentionMongoExplicitlyCoveredLegacyFloorPreparesMetadataOnly(t *testing.T) {
	f := fixture(t, nil)
	f.seed(t)
	if _, err := f.db.Collection("conversation").UpdateOne(f.ctx, bson.M{"owner_user_id": "alice"}, bson.M{"$unset": bson.M{"retention_policy_revision": ""}}); err != nil {
		t.Fatal("old synthetic row setup failed")
	}
	encoded, _ := json.Marshal([]any{"si_alice_bob", "alice"})
	id := sha256.Sum256(encoded)
	if _, err := f.db.Collection("message_visibility_floors").InsertOne(f.ctx, bson.M{"_id": hex.EncodeToString(id[:]), "conversation_id": "si_alice_bob", "user_id": "alice", "min_seq": int64(500)}); err != nil {
		t.Fatal("explicit synthetic floor migration failed")
	}
	f.store.LegacyFloors = func(context.Context, string, string) (int64, int64, error) { return 500, 0, nil }
	_, changed, err := f.store.PrepareLegacyPage(f.ctx, retention.Cursor{}, 50)
	if err != nil || changed != 1 {
		t.Fatal("covered legacy metadata preparation failed")
	}
	p, err := f.store.ReadPolicy(f.ctx, "alice", "si_alice_bob")
	if err != nil || p.Revision != 1 || p.Duration != 30 || !p.Enabled || !p.LastScan.IsZero() {
		t.Fatal("preparation changed preference/progress")
	}
	f.assertState(t, 500, 0)
}

func TestRetentionMongoMixedPeerAndPolicyWritesCommitTogetherAndEffectsWait(t *testing.T) {
	for _, outcome := range []string{"abort_after_peer", "commit", "cache_failure_after_commit"} {
		t.Run(outcome, func(t *testing.T) {
			f := fixture(t, nil)
			rows := []*relation.ConversationModel{
				{OwnerUserID: "alice", ConversationID: "si_alice_bob", UserID: "bob", ConversationType: 1, MsgDestructTime: 30},
				{OwnerUserID: "bob", ConversationID: "si_alice_bob", UserID: "alice", ConversationType: 1, MsgDestructTime: 30},
			}
			if err := f.dao.Create(f.ctx, rows); err != nil {
				t.Fatal("synthetic pair create failed")
			}
			cached := &noCache{}
			db := controller.NewConversationDatabase(f.dao, cached, mongotx.New(f.db.Client()))
			provider, ok := db.(interface {
				RetentionTransaction(context.Context, func(context.Context) error) error
			})
			if !ok {
				t.Fatal("strong transaction adapter missing")
			}
			injected := errors.New("synthetic second-stage failure")
			cached.after = func(after context.Context) error {
				// Read outside the transaction: both policy records must already be committed.
				for _, owner := range []string{"alice", "bob"} {
					p, err := f.store.ReadPolicy(after, owner, "si_alice_bob")
					if err != nil || !p.Private {
						return errors.New("cache effect preceded commit")
					}
				}
				if outcome == "cache_failure_after_commit" {
					return errors.New("synthetic cache unavailable")
				}
				return nil
			}
			err := provider.RetentionTransaction(f.ctx, func(current context.Context) error {
				if err := db.SyncPeerUserPrivateConversationTx(current, []*relation.ConversationModel{{OwnerUserID: "alice", UserID: "bob", ConversationID: "si_alice_bob", ConversationType: 1, IsPrivateChat: true}}); err != nil {
					return err
				}
				if cached.execs != 0 {
					return errors.New("peer cache invalidated before commit")
				}
				if err := db.SetUsersConversationFiledTx(current, []string{"alice"}, rows[0], map[string]any{"is_msg_destruct": true, "msg_destruct_time": int64(3600)}); err != nil {
					return err
				}
				if outcome == "abort_after_peer" {
					return injected
				}
				return nil
			})
			if outcome == "abort_after_peer" && !errors.Is(err, injected) {
				t.Fatalf("abort result: %T", err)
			}
			if outcome == "commit" && err != nil {
				t.Fatalf("commit result: %T", err)
			}
			if outcome == "cache_failure_after_commit" && !errors.Is(err, retention.ErrSyncPending) {
				t.Fatalf("committed synchronization failure: %T", err)
			}
			for _, owner := range []string{"alice", "bob"} {
				p, readErr := f.store.ReadPolicy(f.ctx, owner, "si_alice_bob")
				committed := outcome != "abort_after_peer"
				if readErr != nil || p.Private != committed {
					t.Fatal("peer update escaped outer transaction")
				}
				if owner == "alice" && (p.Enabled != committed || (committed && (p.Duration != 3600 || p.Revision != 2)) || (!committed && (p.Duration != 30 || p.Revision != 1))) {
					t.Fatal("preference update escaped outer transaction")
				}
			}
			if (outcome == "abort_after_peer" && cached.execs != 0) || (outcome != "abort_after_peer" && cached.execs != 2) {
				t.Fatalf("cache executions=%d", cached.execs)
			}
		})
	}
}

func TestRetentionMongoPolicyWritersRejectWeakExternalSessionBeforeAnyWrite(t *testing.T) {
	f := fixture(t, nil)
	f.seed(t)
	cached := &noCache{}
	db := controller.NewConversationDatabase(f.dao, cached, mongotx.New(f.db.Client()))
	session, err := f.db.Client().StartSession()
	if err != nil {
		t.Fatal("fixture session failed")
	}
	defer session.EndSession(context.Background())
	if err = session.StartTransaction(); err != nil {
		t.Fatal("fixture weak transaction failed")
	}
	weak := mongo.NewSessionContext(f.ctx, session)
	calls := []struct {
		name string
		run  func() error
	}{
		{"patch", func() error {
			return db.UpdateUsersConversationFiled(weak, []string{"alice"}, "si_alice_bob", map[string]any{"is_msg_destruct": false})
		}},
		{"full", func() error {
			return db.SetUserConversations(weak, "alice", []*relation.ConversationModel{{OwnerUserID: "alice", ConversationID: "si_alice_bob", UserID: "bob", ConversationType: 1}})
		}},
		{"peer", func() error {
			return db.SyncPeerUserPrivateConversationTx(weak, []*relation.ConversationModel{{OwnerUserID: "alice", ConversationID: "si_alice_bob", UserID: "bob", ConversationType: 1, IsPrivateChat: true}})
		}},
		{"enabled_create", func() error {
			return db.CreateConversation(weak, []*relation.ConversationModel{{OwnerUserID: "alice", ConversationID: "si_alice_new", UserID: "new", ConversationType: 1, IsMsgDestruct: true, MsgDestructTime: 30}})
		}},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, retention.ErrUnavailable) {
				t.Fatalf("weak outer session accepted: %T", err)
			}
		})
	}
	if cached.execs != 0 {
		t.Fatal("weak transaction published cache effect")
	}
	if err = session.AbortTransaction(f.ctx); err != nil {
		t.Fatal("fixture abort failed")
	}
	p, err := f.store.ReadPolicy(f.ctx, "alice", "si_alice_bob")
	if err != nil || p.Revision != 1 || !p.Enabled || p.Private {
		t.Fatal("weak session altered existing policy")
	}
	count, err := f.db.Collection("conversation").CountDocuments(f.ctx, bson.M{})
	if err != nil || count != 1 {
		t.Fatal("enabled create escaped outer transaction")
	}
}
