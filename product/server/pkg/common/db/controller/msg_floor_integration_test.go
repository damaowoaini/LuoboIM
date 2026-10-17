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
)

type floorCacheFailure struct{ cache.MsgModel }

func (floorCacheFailure) SetConversationUserMinSeq(context.Context, string, string, int64) error {
	return errors.New("fixture cache unavailable after durable commit")
}

type floorWriteFailure struct{ ut.MsgDocModelInterface }

func (floorWriteFailure) AdvanceMessageVisibility(context.Context, []ut.MessageVisibilityFloor) error {
	return errors.New("fixture no durable write")
}

func TestRealRetentionPersistsFloorBeforePhysicalRemovalAndRejectsStorageFailure(t *testing.T) {
	database := isolatedReceiptMongo(t)
	socket := os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET")
	if socket == "" {
		t.Skip("requires private Redis")
	}
	if !strings.HasSuffix(socket, "/references/integration/redis8.2/redis.sock") {
		t.Fatal("private fixture only")
	}
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 15})
	t.Cleanup(func() { rdb.Close() })
	conversation := "si_retention_" + uuid.NewString()
	messageCache := cache.NewMsgCacheModel(rdb)
	store := unrelation.NewMsgMongoDriver(database)
	writer := &commonMsgDatabase{cache: messageCache, msgDocDatabase: store, readOnly: true}
	previous := config.Config.MsgCacheTimeout
	config.Config.MsgCacheTimeout = 60
	t.Cleanup(func() { config.Config.MsgCacheTimeout = previous })
	t.Cleanup(func() {
		messageCache.DeleteMessages(ctx, conversation, []int64{1, 2})
		rdb.Del(ctx, "MAX_SEQ:"+conversation, "MIN_SEQ:"+conversation)
	})
	messages := []*sdkws.MsgData{{Seq: 1, SendTime: time.Now().Add(-2 * time.Hour).UnixMilli(), Content: []byte("old")}, {Seq: 2, SendTime: time.Now().UnixMilli(), Content: []byte("new")}}
	if err := writer.BatchInsertChat2DB(ctx, conversation, messages, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := messageCache.SetMessageToCache(ctx, conversation, messages); err != nil {
		t.Fatal(err)
	}
	messageCache.SetMaxSeq(ctx, conversation, 2)
	failing := &commonMsgDatabase{cache: messageCache, msgDocDatabase: floorWriteFailure{store}}
	if err := failing.DeleteConversationMsgsAndSetMinSeq(ctx, conversation, 60); err == nil {
		t.Fatal("retention acknowledged failed floor write")
	}
	var document ut.MsgDocModel
	if err := database.Collection("msg").FindOne(ctx, bson.M{"doc_id": conversation + ":0"}).Decode(&document); err != nil || document.Msg[0].Msg == nil {
		t.Fatal("retention removed source before floor commit", err)
	}
	if err := writer.DeleteConversationMsgsAndSetMinSeq(ctx, conversation, 60); err != nil {
		t.Fatal(err)
	}
	if floor, err := writer.GetMinSeq(ctx, conversation); err != nil || floor != 2 {
		t.Fatal("retention floor wrong", floor, err)
	}
	if _, err := messageCache.SetMessageToCache(ctx, conversation, messages); err != nil {
		t.Fatal(err)
	}
	_, _, visible, err := writer.GetMsgBySeqs(ctx, "owner", conversation, []int64{1, 2})
	if err != nil || len(visible) != 1 || visible[0].Seq != 2 {
		t.Fatal("retention resurrected cache body or removed unexpired message", err)
	}
	if err := writer.DeleteConversationMsgsAndSetMinSeq(ctx, conversation, 0); err != nil {
		t.Fatal(err)
	}
	if err := writer.BatchInsertChat2DB(ctx, conversation, messages, 0); err != nil {
		t.Fatal("late queue replay failed", err)
	}
	rdb.Del(ctx, "MIN_SEQ:"+conversation)
	_, _, visible, err = writer.GetMsgBySeqs(ctx, "owner", conversation, []int64{1, 2})
	if err != nil || len(visible) != 0 {
		t.Fatal("full retention clear resurrected late replay", err)
	}
	t.Log("LUOBO_RETENTION_DURABLE_ORDER_PASS")
}

type floorStorageFailure struct{ ut.MsgDocModelInterface }

func (floorStorageFailure) ReadMessageVisibility(context.Context, []ut.MessageVisibilityFloor) ([]ut.MessageVisibilityFloor, error) {
	return nil, errors.New("fixture durable floor unavailable")
}
func (floorStorageFailure) AdvanceMessageVisibility(context.Context, []ut.MessageVisibilityFloor) error {
	return errors.New("fixture durable floor unavailable")
}

func TestRealClearVisibilityFloorSurvivesCacheLossConcurrencyAndInflightReads(t *testing.T) {
	database := isolatedReceiptMongo(t)
	socket := os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET")
	if socket == "" {
		t.Skip("requires private Redis fixture")
	}
	if !strings.HasSuffix(socket, "/references/integration/redis8.2/redis.sock") {
		t.Fatal("private fixture only")
	}
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 15})
	t.Cleanup(func() { rdb.Close() })
	conversation := "si_clear_" + uuid.NewString()
	messageCache := cache.NewMsgCacheModel(rdb)
	store := unrelation.NewMsgMongoDriver(database)
	writer := &commonMsgDatabase{cache: messageCache, msgDocDatabase: store, readOnly: true}
	previous := config.Config.MsgCacheTimeout
	config.Config.MsgCacheTimeout = 60
	t.Cleanup(func() { config.Config.MsgCacheTimeout = previous })
	keys := []string{"MAX_SEQ:" + conversation, "MIN_SEQ:" + conversation, "CON_USER_MIN_SEQ:" + conversation + "u:owner", "CON_USER_MIN_SEQ:" + conversation + "u:peer"}
	t.Cleanup(func() { messageCache.DeleteMessages(ctx, conversation, []int64{1, 2, 3}); rdb.Del(ctx, keys...) })
	cacheBodies := func() {
		t.Helper()
		_, err := messageCache.SetMessageToCache(ctx, conversation, []*sdkws.MsgData{{Seq: 1, SendID: "owner", RecvID: "peer", Content: []byte("one")}, {Seq: 2, SendID: "peer", RecvID: "owner", Content: []byte("two")}, {Seq: 3, SendID: "owner", RecvID: "peer", Content: []byte("three")}})
		if err != nil {
			t.Fatal(err)
		}
	}
	cacheBodies()
	if err := messageCache.SetMaxSeq(ctx, conversation, 3); err != nil {
		t.Fatal(err)
	}
	if err := messageCache.SetMinSeq(ctx, conversation, 1); err != nil {
		t.Fatal(err)
	}
	barrier := &deletionReadBarrier{MsgModel: messageCache, fetched: make(chan struct{}), release: make(chan struct{})}
	reader := &commonMsgDatabase{cache: barrier, msgDocDatabase: store, readOnly: true}
	type outcome struct {
		messages []*sdkws.MsgData
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		_, _, messages, err := reader.GetMsgBySeqs(ctx, "owner", conversation, []int64{1, 2, 3})
		done <- outcome{messages, err}
	}()
	<-barrier.fetched
	if err := writer.SetConversationUserMinSeq(ctx, conversation, "owner", 3); err != nil {
		close(barrier.release)
		t.Fatal(err)
	}
	close(barrier.release)
	result := <-done
	if result.err != nil || len(result.messages) != 1 || result.messages[0].Seq != 3 {
		t.Fatal("inflight cached read ignored committed clear", result.err)
	}
	// Concurrent lower and higher clears cannot roll back the durable boundary.
	var wg sync.WaitGroup
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			failures <- writer.SetConversationUserMinSeq(ctx, conversation, "owner", int64(i%4+1))
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if floor, err := writer.GetConversationUserMinSeq(ctx, conversation, "owner"); err != nil || floor != 4 {
		t.Fatal("concurrent floor decreased", floor, err)
	}
	// Simulate only the affected Redis keys being lost; no global cache clear.
	if err := rdb.Del(ctx, keys[1:]...).Err(); err != nil {
		t.Fatal(err)
	}
	cacheBodies()
	restarted := &commonMsgDatabase{cache: messageCache, msgDocDatabase: unrelation.NewMsgMongoDriver(database), readOnly: true}
	if floor, err := restarted.GetConversationUserMinSeq(ctx, conversation, "owner"); err != nil || floor != 4 {
		t.Fatal("restart/cache loss lost personal clear", floor, err)
	}
	_, _, messages, err := restarted.GetMsgBySeqs(ctx, "owner", conversation, []int64{1, 2, 3})
	if err != nil || len(messages) != 0 {
		t.Fatal("personal clear resurrected", err)
	}
	_, _, messages, err = restarted.GetMsgBySeqs(ctx, "peer", conversation, []int64{1, 2, 3})
	if err != nil || len(messages) != 3 {
		t.Fatal("personal clear affected peer", err)
	}
	_, _, messages, err = restarted.GetMsgBySeqsRange(ctx, "owner", conversation, 1, 3, 3, 0)
	if err != nil || len(messages) != 0 {
		t.Fatal("range crossed durable clear", err)
	}
	summaries, err := restarted.FindOneByDocIDs(context.WithValue(ctx, constant.OpUserID, "owner"), []string{conversation}, map[string]int64{conversation: 3})
	if err != nil || len(summaries) != 0 {
		t.Fatal("summary resurrected clear", err)
	}
	// Cache write failure after Mongo commit is an error, and retains the floor.
	failingCache := &commonMsgDatabase{cache: floorCacheFailure{messageCache}, msgDocDatabase: store}
	if err := failingCache.SetConversationUserMinSeq(ctx, conversation, "peer", 2); err == nil {
		t.Fatal("cache failure acknowledged")
	}
	if floor, err := restarted.GetConversationUserMinSeq(ctx, conversation, "peer"); err != nil || floor != 2 {
		t.Fatal("cache error lost durable write", floor, err)
	}
	// Durable storage failure cannot acknowledge a Redis-only restriction.
	failingStore := &commonMsgDatabase{cache: messageCache, msgDocDatabase: floorStorageFailure{store}}
	if err := failingStore.SetConversationUserMinSeq(ctx, conversation, "peer", 4); err == nil {
		t.Fatal("durable failure acknowledged")
	}
	if _, _, _, err := failingStore.GetMsgBySeqs(ctx, "peer", conversation, []int64{3}); err == nil {
		t.Fatal("floor metadata outage exposed cache")
	}
	if err := writer.SetMinSeq(ctx, conversation, 3); err != nil {
		t.Fatal(err)
	}
	if err := writer.SetMinSeq(ctx, conversation, 1); err != nil {
		t.Fatal(err)
	}
	rdb.Del(ctx, keys[1:]...)
	cacheBodies()
	if floor, err := restarted.GetMinSeq(ctx, conversation); err != nil || floor != 3 {
		t.Fatal("global clear decreased/lost", floor, err)
	}
	_, _, messages, err = restarted.GetMsgBySeqs(ctx, "peer", conversation, []int64{1, 2, 3})
	if err != nil || len(messages) != 1 || messages[0].Seq != 3 {
		t.Fatal("global floor ignored", err)
	}
	if err := writer.SetMinSeq(ctx, "", 3); err == nil {
		t.Fatal("invalid tuple accepted")
	}
	if err := writer.SetConversationUserMinSeq(ctx, conversation, "", 3); err == nil {
		t.Fatal("personal floor accepted global owner")
	}
	if err := writer.SetMinSeq(ctx, conversation, -1); err == nil {
		t.Fatal("negative floor accepted")
	}
	t.Log("LUOBO_DURABLE_CLEAR_VISIBILITY_PASS")
}
