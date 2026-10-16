package controller

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	ut "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type deletionReadBarrier struct {
	cache.MsgModel
	once             sync.Once
	fetched, release chan struct{}
}

func (b *deletionReadBarrier) GetMessagesBySeq(ctx context.Context, conversation string, seqs []int64) ([]*sdkws.MsgData, []int64, error) {
	messages, missing, err := b.MsgModel.GetMessagesBySeq(ctx, conversation, seqs)
	b.once.Do(func() { close(b.fetched); <-b.release })
	return messages, missing, err
}

type deletionMetadataFailure struct{ ut.MsgDocModelInterface }

func (deletionMetadataFailure) HiddenMessageSequences(context.Context, string, string, []int64) ([]int64, error) {
	return nil, errors.New("fixture durable visibility unavailable")
}

func TestRealMessageDeletionSurvivesInflightCacheReadRehydrationAndQueueReplay(t *testing.T) {
	db := isolatedReceiptMongo(t)
	socket := os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET")
	if socket == "" {
		t.Skip("requires private Redis fixture")
	}
	if !strings.HasSuffix(socket, "/references/integration/redis8.2/redis.sock") {
		t.Fatal("only private Redis allowed")
	}
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 15})
	t.Cleanup(func() { rdb.Close() })
	ctx := context.Background()
	conversation := "si_deletion_" + uuid.NewString()
	if _, err := db.Collection("msg").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "doc_id", Value: 1}}, Options: options.Index().SetUnique(true)}); err != nil {
		t.Fatal(err)
	}
	previous := config.Config.MsgCacheTimeout
	config.Config.MsgCacheTimeout = 60
	t.Cleanup(func() { config.Config.MsgCacheTimeout = previous })
	messageCache := cache.NewMsgCacheModel(rdb)
	t.Cleanup(func() {
		messageCache.DeleteMessages(ctx, conversation, []int64{1, 2})
		rdb.Del(ctx, "MAX_SEQ:"+conversation, "MIN_SEQ:"+conversation)
	})
	messages := []*sdkws.MsgData{{Seq: 1, ClientMsgID: "deleted", SendID: "owner", RecvID: "peer", SessionType: 1, Content: []byte("opaque-one"), KeyVersion: 7}, {Seq: 2, ClientMsgID: "control", SendID: "owner", RecvID: "peer", SessionType: 1, Content: []byte("opaque-two"), KeyVersion: 8}}
	cacheBodies := func() {
		t.Helper()
		if _, err := messageCache.SetMessageToCache(ctx, conversation, messages); err != nil {
			t.Fatal(err)
		}
	}
	cacheBodies()
	if err := messageCache.SetMaxSeq(ctx, conversation, 2); err != nil {
		t.Fatal(err)
	}
	if err := messageCache.SetMinSeq(ctx, conversation, 1); err != nil {
		t.Fatal(err)
	}
	store := unrelation.NewMsgMongoDriver(db)
	writer := &commonMsgDatabase{cache: messageCache, msgDocDatabase: store, readOnly: true}
	barrier := &deletionReadBarrier{MsgModel: messageCache, fetched: make(chan struct{}), release: make(chan struct{})}
	reader := &commonMsgDatabase{cache: barrier, msgDocDatabase: store, readOnly: true}
	type outcome struct {
		messages []*sdkws.MsgData
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		_, _, messages, err := reader.GetMsgBySeqs(ctx, "owner", conversation, []int64{1, 2})
		done <- outcome{messages, err}
	}()
	select {
	case <-barrier.fetched:
	case <-time.After(3 * time.Second):
		t.Fatal("inflight read did not reach deterministic cache barrier")
	}
	if err := writer.DeleteMsgsPhysicalBySeqs(ctx, conversation, []int64{1}); err != nil {
		close(barrier.release)
		t.Fatal(err)
	}
	close(barrier.release)
	result := <-done
	if result.err != nil || len(result.messages) != 1 || result.messages[0].Seq != 2 {
		t.Fatal("cached read returned body after durable tombstone committed", result)
	}
	cacheBodies() // Deliberately restore an old Redis body after deletion.
	if err := writer.BatchInsertChat2DB(ctx, conversation, messages, 0); err != nil {
		t.Fatal("late queue replay should be safely idempotent", err)
	}
	var persisted ut.MsgDocModel
	if err := db.Collection("msg").FindOne(ctx, bson.M{"doc_id": conversation + ":0"}).Decode(&persisted); err != nil || !persisted.Msg[0].GlobalDeleted || persisted.Msg[0].Msg != nil || persisted.Msg[1].Msg.KeyVersion != 8 {
		t.Fatal("late queue replay resurrected tombstoned payload or lost legitimate key version", err)
	}
	if err := writer.DeleteMsgsPhysicalBySeqs(ctx, conversation, []int64{1}); err != nil {
		t.Fatal("repeated deletion must be idempotent", err)
	}
	cacheBodies()
	_, _, exact, err := writer.GetMsgBySeqs(ctx, "owner", conversation, []int64{1, 2})
	if err != nil || len(exact) != 1 || exact[0].Seq != 2 {
		t.Fatal("rehydrated cache bypassed exact-sequence tombstone", err)
	}
	_, _, ranged, err := writer.GetMsgBySeqsRange(ctx, "owner", conversation, 1, 2, 2, 0)
	if err != nil || len(ranged) != 1 || ranged[0].Seq != 2 {
		t.Fatal("range read bypassed tombstone", err)
	}
	summaryCtx := context.WithValue(ctx, constant.OpUserID, "owner")
	summaries, err := writer.FindOneByDocIDs(summaryCtx, []string{conversation}, map[string]int64{conversation: 1})
	if err != nil || len(summaries) != 0 {
		t.Fatal("conversation summary bypassed tombstone", err)
	}
	if err := writer.DeleteUserMsgsBySeqs(ctx, "owner", conversation, []int64{2}); err != nil {
		t.Fatal(err)
	}
	cacheBodies()
	_, _, own, err := writer.GetMsgBySeqs(ctx, "owner", conversation, []int64{2})
	if err != nil || len(own) != 0 {
		t.Fatal("stale cache resurrected personal deletion", err)
	}
	_, _, peer, err := writer.GetMsgBySeqs(ctx, "peer", conversation, []int64{2})
	if err != nil || len(peer) != 1 || peer[0].KeyVersion != 8 {
		t.Fatal("personal deletion hid peer's legitimate message", err)
	}
	failing := &commonMsgDatabase{cache: messageCache, msgDocDatabase: deletionMetadataFailure{store}, readOnly: true}
	if _, _, _, err := failing.GetMsgBySeqs(ctx, "peer", conversation, []int64{2}); err == nil {
		t.Fatal("metadata outage allowed cached body")
	}
	if _, _, _, err := failing.GetMsgBySeqsRange(ctx, "peer", conversation, 1, 2, 2, 0); err == nil {
		t.Fatal("metadata outage became successful history page")
	}
	if _, err := failing.FindOneByDocIDs(summaryCtx, []string{conversation}, map[string]int64{conversation: 2}); err == nil {
		t.Fatal("metadata outage allowed summary body")
	}
	t.Log("LUOBO_DURABLE_DELETION_INTERLEAVING_PASS")
}

func TestMongoMessageBatchHandlesLeadingNilAndRejectsInvalidSequenceBeforeWrite(t *testing.T) {
	db := isolatedReceiptMongo(t)
	ctx := context.Background()
	writer := &commonMsgDatabase{msgDocDatabase: unrelation.NewMsgMongoDriver(db)}
	batch := []*sdkws.MsgData{nil, {Seq: 2, SendID: "synthetic-owner", Content: []byte("kept"), KeyVersion: 9}, nil, {Seq: 4, SendID: "synthetic-owner", Content: []byte("kept-four")}}
	if err := writer.BatchInsertChat2DB(ctx, "si_nil-fixture", batch, 0); err != nil {
		t.Fatal("leading nil placeholder must not panic or discard later messages", err)
	}
	var row ut.MsgDocModel
	if err := db.Collection("msg").FindOne(ctx, bson.M{"doc_id": "si_nil-fixture:0"}).Decode(&row); err != nil || row.Msg[0].Msg != nil || row.Msg[1].Msg.KeyVersion != 9 || row.Msg[2].Msg != nil || row.Msg[3].Msg.Seq != 4 {
		t.Fatal("batch sequence slots or key version were lost", err)
	}
	if err := writer.BatchInsertChat2DB(ctx, "si_invalid-fixture", []*sdkws.MsgData{{Seq: 1}, {Seq: 3}}, 0); err == nil {
		t.Fatal("invalid batch sequence accepted")
	}
	if n, err := db.Collection("msg").CountDocuments(ctx, bson.M{"doc_id": "si_invalid-fixture:0"}); err != nil || n != 0 {
		t.Fatal("invalid batch partially wrote data", n, err)
	}
}
