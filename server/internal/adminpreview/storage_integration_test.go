package adminpreview

import (
	"context"
	"encoding/json"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/convert"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	ut "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMongoRedisPreviewActualOpaqueCiphertextAndOwnerVisibility(t *testing.T) {
	uri, socket := os.Getenv("LUOBO_INTEGRATION_MONGO_URI"), os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET")
	if uri == "" || socket == "" {
		t.Skip("requires private Mongo replica and Redis")
	}
	if !strings.HasPrefix(uri, "mongodb://127.0.0.1:37017/") || !strings.Contains(socket, "/references/integration/redis8.2/") {
		t.Fatal("only private task fixtures permitted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	db := client.Database("luobo_preview_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	defer db.Drop(context.Background())
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 15})
	defer rdb.Close()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	user := "preview-" + suffix
	conversation := "si_" + user + "_zz-peer"
	defer func() {
		var cursor uint64
		for {
			keys, next, err := rdb.Scan(context.Background(), cursor, "*"+conversation+"*", 100).Result()
			if err != nil {
				return
			}
			if len(keys) > 0 {
				rdb.Del(context.Background(), keys...)
			}
			cursor = next
			if cursor == 0 {
				return
			}
		}
	}()
	row := &relation.ConversationModel{OwnerUserID: user, ConversationID: conversation, ConversationType: constant.SingleChatType, UserID: "zz-peer", MinSeq: 2}
	if _, err := db.Collection("conversation").InsertOne(ctx, row); err != nil {
		t.Fatal(err)
	}
	ciphertext, key, _ := legacyVector(t)
	raws := []*sdkws.MsgData{{Seq: 1, Content: []byte(`{"content":"before join"}`)}, {Seq: 2, Content: ciphertext, KeyVersion: 1}, {Seq: 3, Content: []byte(`{"content":"deleted cached"}`)}, {Seq: 4, Content: []byte(`{"content":"persisted only"}`)}, {Seq: 5, Content: []byte(`{"content":"latest cache"}`)}}
	block := &ut.MsgDocModel{DocID: ut.MsgDocModel{}.GetDocID(conversation, 1)}
	for _, message := range raws {
		message.ClientMsgID = "fixture-" + uuid.NewString()
		message.SendID = "zz-peer"
		message.RecvID = user
		message.ContentType = constant.Text
		message.SessionType = constant.SingleChatType
		message.MsgFrom = constant.UserMsgType
		message.SendTime = 1700000000000 + message.Seq
		block.Msg = append(block.Msg, &ut.MsgInfoModel{Msg: convert.MsgPb2DB(message)})
	}
	if _, err := db.Collection("msg").InsertOne(ctx, block); err != nil {
		t.Fatal("actual legacy binary ciphertext could not be stored")
	}
	cacheModel := cache.NewMsgCacheModel(rdb)
	oldTimeout := config.Config.MsgCacheTimeout
	config.Config.MsgCacheTimeout = 3600
	defer func() { config.Config.MsgCacheTimeout = oldTimeout }()
	if _, err := cacheModel.SetMessageToCache(ctx, conversation, []*sdkws.MsgData{raws[2], raws[4]}); err != nil {
		t.Fatal(err)
	}
	cacheModel.SetMaxSeq(ctx, conversation, 5)
	cacheModel.SetMinSeq(ctx, conversation, 1)
	cacheModel.SetConversationUserMinSeq(ctx, conversation, user, 2)
	if err := cacheModel.UserDeleteMsgs(ctx, conversation, []int64{3}, user); err != nil {
		t.Fatal(err)
	}
	if err := rdb.HSet(ctx, cache.EncryptionKeyPrefix+conversation, "1", key).Err(); err != nil {
		t.Fatal(err)
	}
	audit, err := NewMongoAudit(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	source := &DataSource{Database: db, Redis: rdb, Reader: controller.NewReadOnlyMessageReader(rdb, db)}
	// Exercise the exact-sequence reader directly with forbidden old and cached
	// deleted sequences, rather than relying on the preview range pre-filter.
	direct, err := source.Read(ctx, user, conversation, []int64{1, 2, 3, 4, 5})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, message := range direct {
		seen[message.Seq] = true
	}
	if len(seen) != 3 || seen[1] || seen[3] || !seen[2] || !seen[4] || !seen[5] {
		t.Fatal("actual Redis exact-sequence floor/deletion bypass")
	}
	service := &Service{Source: source, Audit: audit, CursorKey: []byte(strings.Repeat("k", 32))}
	req := Request{UserID: user, ConversationID: conversation, PageSize: 2}
	first, err := service.Search(ctx, "audited-admin", req)
	if err != nil || len(first.Messages) != 2 || first.Messages[0]["seq"] != int64(5) || first.Messages[1]["seq"] != int64(4) || !first.HasMore {
		t.Fatal("mixed Mongo/Redis history failed", err)
	}
	req.Cursor = first.NextCursor
	second, err := service.Search(ctx, "audited-admin", req)
	if err != nil || len(second.Messages) != 1 || second.Messages[0]["seq"] != int64(2) || second.Messages[0]["decryptionState"] != "decrypted" || second.HasMore {
		t.Fatal("owner floor/deletion/native decryption failed", err)
	}
	req.Cursor = ""
	req.Keyword = "synthetic"
	found, err := service.Search(ctx, "audited-admin", req)
	if err != nil || len(found.Messages) != 1 || found.Messages[0]["seq"] != int64(2) {
		t.Fatal("actual encrypted keyword search failed", err)
	}
	var stored ut.MsgDocModel
	if db.Collection("msg").FindOne(ctx, bson.M{"doc_id": block.DocID}).Decode(&stored) != nil || stored.Msg[1].Msg.KeyVersion != 1 || stored.Msg[1].Msg.Content != string(ciphertext) {
		t.Fatal("read mutated opaque ciphertext/version")
	}
	if count, err := db.Collection("admin_message_preview_audit").CountDocuments(ctx, bson.M{"actor": "audited-admin", "state": "complete"}); err != nil || count != 3 {
		t.Fatal("audit completion not persisted")
	}
	if _, err := service.Search(ctx, "audited-admin", Request{UserID: "victim", ConversationID: conversation}); err == nil {
		t.Fatal("foreign target reused owned conversation")
	}
	// A missing key cannot be reported as a successful exhaustive keyword search.
	rdb.HDel(ctx, cache.EncryptionKeyPrefix+conversation, "1")
	missing, err := service.Search(ctx, "audited-admin", req)
	if err != nil || len(missing.Messages) != 0 || missing.UnsearchableCount != 1 || missing.SearchComplete {
		t.Fatal("missing-key search fabricated completeness", err)
	}
	// Inspect only synthetic data and ensure audit does not contain keys/content.
	records, err := db.Collection("admin_message_preview_audit").Find(ctx, bson.M{})
	if err != nil {
		t.Fatal(err)
	}
	var values []bson.M
	records.All(ctx, &values)
	encoded, _ := json.Marshal(values)
	if strings.Contains(string(encoded), key) || strings.Contains(string(encoded), "Synthetic encryption fixture") {
		t.Fatal("audit leaked plaintext or key")
	}
}
