package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	ut "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func isolatedReceiptMongo(t *testing.T) *mongo.Database {
	t.Helper()
	uri := os.Getenv("LUOBO_INTEGRATION_MONGO_URI")
	if uri == "" {
		t.Skip("requires task-private Mongo replica set")
	}
	if !strings.HasPrefix(uri, "mongodb://127.0.0.1:37017/") {
		t.Fatal("only task-private Mongo fixture allowed")
	}
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database("luobo_receipts_test_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	t.Cleanup(func() { db.Drop(context.Background()); client.Disconnect(context.Background()) })
	return db
}

func TestMongoGroupReceiptsConcurrentSnapshotMarkRestartAndImmutableCohort(t *testing.T) {
	db := isolatedReceiptMongo(t)
	ctx := context.Background()
	store, err := NewPersistentMsgGroupReadDatabase(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	users := func(context.Context) ([]string, error) { return []string{"u1", "u2", "u3", "u1"}, nil }
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := store.EnsureGroupMessage(ctx, "sg_group", "same", users); err != nil {
				t.Error(err)
			}
			if err := store.EnsureGroupMessage(ctx, "sg_group", "independent-"+string(rune('a'+i)), users); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if n, _ := db.Collection(receiptMessages).CountDocuments(ctx, bson.M{}); n != 17 {
		t.Fatal("concurrent snapshots lost/duplicated message identities", n)
	}
	readAt := time.Now().Add(-time.Minute).Truncate(time.Millisecond).UTC()
	var marked atomic.Int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, err := store.MarkGroupMessageRead(ctx, "sg_group", "same", "u1", &readAt, users)
			if err != nil {
				t.Error(err)
			} else if state == constant.GroupMessageReadMarkNew {
				marked.Add(1)
			}
		}()
	}
	wg.Wait()
	if marked.Load() != 1 {
		t.Fatal("concurrent reads were not idempotent", marked.Load())
	}
	store, err = NewPersistentMsgGroupReadDatabase(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	read, unread, err := store.GetMsgNum(ctx, "sg_group", "same")
	if err != nil || read != 1 || unread != 2 {
		t.Fatal("restart lost persisted receipt counts", read, unread, err)
	}
	later := time.Now()
	if state, err := store.MarkGroupMessageRead(ctx, "sg_group", "same", "new-member", &later, users); err != nil || state != constant.GroupMessageReadMarked {
		t.Fatal("new history reader should not enlarge old receipt cohort", state, err)
	}
	if _, err := store.MarkGroupMessageRead(ctx, "sg_group", "same", "u1", &later, users); err != nil {
		t.Fatal(err)
	}
	count, rows, err := store.PageReadUserList(ctx, "sg_group", "same", true, 1, 10)
	if err != nil || count != 1 || len(rows) != 1 || rows[0].UserID != "u1" || rows[0].Time != readAt.UnixMilli() {
		t.Fatal("first read time/recipient pagination changed on retry", rows, err)
	}
	count, rows, err = store.PageReadUserList(ctx, "sg_group", "same", false, 1, 1)
	if err != nil || count != 2 || len(rows) != 1 || rows[0].UserID != "u2" || rows[0].Time != 0 {
		t.Fatal("unread stable pagination invalid", rows, err)
	}
	if t.Failed() {
		return
	}
	t.Log("LUOBO_GROUP_RECEIPT_PERSISTENT_CONCURRENCY_PASS")
}

func TestMongoGroupReceiptSnapshotAndReadFailuresNeverBecomeSuccess(t *testing.T) {
	db := isolatedReceiptMongo(t)
	ctx := context.Background()
	store, err := NewPersistentMsgGroupReadDatabase(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RunCommand(ctx, bson.D{{Key: "collMod", Value: receiptRecipients}, {Key: "validator", Value: bson.M{"user_id": bson.M{"$ne": "reject-fixture"}}}, {Key: "validationAction", Value: "error"}}).Err(); err != nil {
		t.Fatal(err)
	}
	users := func(context.Context) ([]string, error) { return []string{"u1", "reject-fixture", "u3"}, nil }
	if err := store.EnsureGroupMessage(ctx, "sg_group", "atomic", users); err == nil {
		t.Fatal("injected recipient insert failure reported success")
	}
	for _, name := range []string{receiptMessages, receiptRecipients} {
		if n, _ := db.Collection(name).CountDocuments(ctx, bson.M{}); n != 0 {
			t.Fatal("snapshot transaction left partial metadata/recipients", name, n)
		}
	}
	if err := db.RunCommand(ctx, bson.D{{Key: "collMod", Value: receiptRecipients}, {Key: "validator", Value: bson.M{}}, {Key: "validationAction", Value: "error"}}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureGroupMessage(ctx, "sg_group", "atomic", users); err != nil {
		t.Fatal(err)
	}
	if err := db.RunCommand(ctx, bson.D{{Key: "collMod", Value: receiptRecipients}, {Key: "validator", Value: bson.M{"read_at": nil}}, {Key: "validationAction", Value: "error"}}).Err(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := store.MarkGroupMessageRead(ctx, "sg_group", "atomic", "u1", &now, users); err == nil {
		t.Fatal("failed persistent read update reported success")
	}
	read, unread, err := store.GetMsgNum(ctx, "sg_group", "atomic")
	if err != nil || read != 0 || unread != 3 {
		t.Fatal("failed update leaked an acknowledged read", read, unread, err)
	}
	if err := db.RunCommand(ctx, bson.D{{Key: "collMod", Value: receiptRecipients}, {Key: "validator", Value: bson.M{}}, {Key: "validationAction", Value: "error"}}).Err(); err != nil {
		t.Fatal(err)
	}
	state, err := store.MarkGroupMessageRead(ctx, "sg_group", "atomic", "u1", &now, users)
	if err != nil || state != constant.GroupMessageReadMarkNew {
		t.Fatal("retry after storage recovery did not persist read", state, err)
	}
}

func TestMongoLegacyGroupReceiptImportFiltersUntrustedRecipientsAndPreservesSource(t *testing.T) {
	db := isolatedReceiptMongo(t)
	ctx := context.Background()
	stamp := time.Now().Add(-time.Hour).Truncate(time.Millisecond).UTC()
	legacy := bson.M{"conversation_id": "sg_group", "index": 1, "msgs": bson.M{"legacy": bson.M{"u1": stamp, "u2": nil, "outsider": stamp, "unproven-former": nil}}}
	if _, err := db.Collection("group_msg_read").InsertOne(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	store, err := NewPersistentMsgGroupReadDatabase(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureGroupMessage(ctx, "sg_group", "legacy", func(context.Context) ([]string, error) { return []string{"u1", "u2"}, nil }); err != nil {
		t.Fatal(err)
	}
	read, unread, err := store.GetMsgNum(ctx, "sg_group", "legacy")
	if err != nil || read != 1 || unread != 1 {
		t.Fatal("legacy import trusted outsider or lost valid read", read, unread, err)
	}
	var meta struct {
		Legacy   bool `bson:"legacy_imported"`
		Filtered int  `bson:"legacy_filtered_recipient_count"`
	}
	if err := db.Collection(receiptMessages).FindOne(ctx, bson.M{"client_msg_id": "legacy"}).Decode(&meta); err != nil || !meta.Legacy || meta.Filtered != 2 {
		t.Fatal("legacy provenance/filtering evidence missing", meta, err)
	}
	var original bson.M
	if err := db.Collection("group_msg_read").FindOne(ctx, bson.M{"conversation_id": "sg_group"}).Decode(&original); err != nil || original["msgs"] == nil {
		t.Fatal("legacy source was removed", err)
	}
	_, rows, err := store.PageReadUserList(ctx, "sg_group", "legacy", true, 1, 10)
	if err != nil || len(rows) != 1 || rows[0].Time != stamp.UnixMilli() {
		t.Fatal("valid legacy timestamp changed", err)
	}
}

func TestRealMongoRedisClientMessageLookupEnforcesVisibilityAndStoredIdentity(t *testing.T) {
	db := isolatedReceiptMongo(t)
	socket := os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET")
	if socket == "" {
		t.Skip("requires private Redis fixture")
	}
	if !strings.HasSuffix(socket, "/references/integration/redis8.2/redis.sock") {
		t.Fatal("only private Redis fixture allowed")
	}
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 15})
	t.Cleanup(func() { rdb.Close() })
	ctx := context.Background()
	conversationID := "sg_receipt-" + uuid.NewString()
	previous := config.Config.MsgCacheTimeout
	config.Config.MsgCacheTimeout = 60
	t.Cleanup(func() { config.Config.MsgCacheTimeout = previous })
	messageCache := cache.NewMsgCacheModel(rdb)
	messages := []*sdkws.MsgData{
		{ClientMsgID: "old-hidden", SendID: "sender", GroupID: strings.TrimPrefix(conversationID, "sg_"), Seq: 1, SessionType: 3},
		{ClientMsgID: "cached-visible", SendID: "sender", GroupID: strings.TrimPrefix(conversationID, "sg_"), Seq: 3, SessionType: 3},
	}
	if _, err := messageCache.SetMessageToCache(ctx, conversationID, messages); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		keys := []string{"MAX_SEQ:" + conversationID, "MIN_SEQ:" + conversationID, "CON_USER_MIN_SEQ:" + conversationID + "u:reader"}
		for _, message := range messages {
			data, _ := json.Marshal([2]string{conversationID, message.ClientMsgID})
			digest := sha256.Sum256(data)
			keys = append(keys, "MESSAGE_CLIENT_SEQ:"+hex.EncodeToString(digest[:]))
		}
		messageCache.DeleteMessages(ctx, conversationID, []int64{1, 3})
		rdb.Del(ctx, keys...)
	})
	if err := messageCache.SetMaxSeq(ctx, conversationID, 3); err != nil {
		t.Fatal(err)
	}
	if err := messageCache.SetMinSeq(ctx, conversationID, 1); err != nil {
		t.Fatal(err)
	}
	if err := messageCache.SetConversationUserMinSeq(ctx, conversationID, "reader", 2); err != nil {
		t.Fatal(err)
	}
	stored := &ut.MsgDocModel{DocID: conversationID + ":0", Msg: []*ut.MsgInfoModel{{Msg: &ut.MsgDataModel{ClientMsgID: "persisted-visible", Seq: 2, SendID: "sender", GroupID: strings.TrimPrefix(conversationID, "sg_"), SessionType: 3}}}}
	// Storage slots are sequence-addressed; seq2 belongs at array index1.
	stored.Msg = append([]*ut.MsgInfoModel{nil}, stored.Msg...)
	if _, err := db.Collection("msg").InsertOne(ctx, stored); err != nil {
		t.Fatal(err)
	}
	reader := NewReadOnlyMessageIdentityReader(rdb, db)
	result, err := reader.GetMsgByClientMsgIDs(ctx, "reader", conversationID, []string{"cached-visible", "persisted-visible"})
	if err != nil || len(result) != 2 || result["cached-visible"].Seq != 3 || result["persisted-visible"].Seq != 2 {
		t.Fatal("cache+persisted identity resolution failed", result, err)
	}
	if _, err := reader.GetMsgByClientMsgIDs(ctx, "reader", conversationID, []string{"old-hidden"}); err == nil {
		t.Fatal("identity lookup bypassed visible user floor")
	}
	if _, err := reader.GetMsgByClientMsgIDs(ctx, "reader", conversationID, []string{"invented"}); err == nil {
		t.Fatal("invented client message ID accepted")
	}
	data, _ := json.Marshal([2]string{conversationID, "cached-visible"})
	digest := sha256.Sum256(data)
	if err := rdb.Set(ctx, "MESSAGE_CLIENT_SEQ:"+hex.EncodeToString(digest[:]), 2, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.GetMsgByClientMsgIDs(ctx, "reader", conversationID, []string{"cached-visible"}); err == nil {
		t.Fatal("forged locator bypassed actual stored message identity")
	}
	t.Log("LUOBO_GROUP_RECEIPT_REAL_MESSAGE_IDENTITY_PASS")
}
