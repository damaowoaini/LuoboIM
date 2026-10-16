package msgtransfer

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	pm "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	kfk "github.com/openimsdk/open-im-server/v3/pkg/common/kafka"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

type actualPersistenceObserver struct {
	controller.CommonMsgDatabase
	failed chan struct{}
	once   sync.Once
}

func (f *actualPersistenceObserver) BatchInsertChat2DB(ctx context.Context, conversation string, messages []*sdkws.MsgData, last int64) error {
	err := f.CommonMsgDatabase.BatchInsertChat2DB(ctx, conversation, messages, last)
	if err != nil {
		f.once.Do(func() { close(f.failed) })
	}
	return err
}

func TestActualKafkaMongoFailureRetainsOffsetAndCacheUntilRecovery(t *testing.T) {
	uri, socket, broker := os.Getenv("LUOBO_INTEGRATION_MONGO_URI"), os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET"), os.Getenv("LUOBO_INTEGRATION_KAFKA_BROKER")
	if uri == "" || socket == "" || broker == "" {
		t.Skip("requires explicit private Kafka+Mongo+Redis fixtures")
	}
	if !strings.HasPrefix(uri, "mongodb://127.0.0.1:37017/") || !strings.HasSuffix(socket, "/references/integration/redis8.2/redis.sock") || broker != "127.0.0.1:39092" {
		t.Fatal("only isolated task fixtures allowed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database("luobo_mq_persistence_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	defer client.Disconnect(context.Background())
	defer db.Drop(context.Background())
	if _, err := db.Collection("msg").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "doc_id", Value: 1}}, Options: options.Index().SetUnique(true)}); err != nil {
		t.Fatal(err)
	}
	validator := func(value bson.M) {
		t.Helper()
		if err := db.RunCommand(ctx, bson.D{{Key: "collMod", Value: "msg"}, {Key: "validator", Value: value}, {Key: "validationAction", Value: "error"}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	validator(bson.M{"reject_fixture": bson.M{"$exists": true}})
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 15})
	defer rdb.Close()
	previous := config.Config.MsgCacheTimeout
	config.Config.MsgCacheTimeout = 60
	defer func() { config.Config.MsgCacheTimeout = previous }()
	conversation := "sg_mq-persistence-" + uuid.NewString()
	messageCache := cache.NewMsgCacheModel(rdb)
	defer messageCache.DeleteMessages(context.Background(), conversation, []int64{1})
	message := &sdkws.MsgData{Seq: 1, SendID: "synthetic-owner", ClientMsgID: "synthetic-message", Content: []byte("synthetic payload"), KeyVersion: 7}
	if _, err := messageCache.SetMessageToCache(ctx, conversation, []*sdkws.MsgData{message}); err != nil {
		t.Fatal(err)
	}
	configuration := sarama.NewConfig()
	configuration.Version = sarama.V2_1_0_0
	configuration.Producer.Return.Successes = true
	configuration.Producer.RequiredAcks = sarama.WaitForAll
	admin, err := sarama.NewClusterAdmin([]string{broker}, configuration)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	topic := "luobo-persistence-test-" + uuid.NewString()
	if err := admin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: 1, ReplicationFactor: 1}, false); err != nil {
		t.Fatal(err)
	}
	defer admin.DeleteTopic(topic)
	group := topic + "-group"
	consumer := kfk.NewMConsumerGroup(&kfk.MConsumerGroupConfig{KafkaVersion: sarama.V2_1_0_0, OffsetsInitial: sarama.OffsetOldest}, []string{topic}, []string{broker}, group)
	consumeCtx, stop := context.WithCancel(ctx)
	defer consumer.Close()
	defer stop()
	store := controller.NewReadOnlyMessageReader(rdb, db).(controller.CommonMsgDatabase)
	observer := &actualPersistenceObserver{CommonMsgDatabase: store, failed: make(chan struct{})}
	handler := &OnlineHistoryMongoConsumerHandler{msgDatabase: observer, historyConsumerGroup: consumer}
	finished := make(chan error, 1)
	go func() { finished <- consumer.Consume(consumeCtx, []string{topic}, handler) }()
	producer, err := sarama.NewSyncProducer([]string{broker}, configuration)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	body, err := proto.Marshal(&pm.MsgDataToMongoByMQ{ConversationID: conversation, MsgData: []*sdkws.MsgData{message}})
	if err != nil {
		t.Fatal(err)
	}
	headers, err := kfk.GetMQHeaderWithContext(mcontext.WithMustInfoCtx([]string{"fixture-persistence", "synthetic-owner", "2", "synthetic-connection"}))
	if err != nil {
		t.Fatal(err)
	}
	_, offset, err := producer.SendMessage(&sarama.ProducerMessage{Topic: topic, Key: sarama.StringEncoder(conversation), Value: sarama.ByteEncoder(body), Headers: headers})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-observer.failed:
	case <-ctx.Done():
		t.Fatal("actual broker record did not reach rejected Mongo write")
	}
	committed := func() int64 {
		t.Helper()
		response, err := admin.ListConsumerGroupOffsets(group, map[string][]int32{topic: {0}})
		if err != nil || response.GetBlock(topic, 0) == nil {
			t.Fatal("cannot inspect own fixture group offset", err)
		}
		return response.GetBlock(topic, 0).Offset
	}
	if committed() > offset {
		t.Fatal("failed Mongo write advanced actual broker offset")
	}
	cached, _, err := messageCache.GetMessagesBySeq(ctx, conversation, []int64{1})
	if err != nil || len(cached) != 1 {
		t.Fatal("failed actual write removed Redis body", err)
	}
	if n, err := db.Collection("msg").CountDocuments(ctx, bson.M{}); err != nil || n != 0 {
		t.Fatal("failed actual Mongo write left unexpected data", n, err)
	}
	validator(bson.M{})
	for committed() <= offset {
		select {
		case <-ctx.Done():
			t.Fatal("recovered actual persistence was not acknowledged")
		case <-time.After(50 * time.Millisecond):
		}
	}
	if n, err := db.Collection("msg").CountDocuments(ctx, bson.M{"doc_id": conversation + ":0", "msgs.0.msg.key_version": 7}); err != nil || n != 1 {
		t.Fatal("acknowledged record is absent from actual Mongo", n, err)
	}
	if cached, _, err := messageCache.GetMessagesBySeq(ctx, conversation, []int64{1}); err != nil || len(cached) != 0 {
		t.Fatal("successful persistence did not retire Redis body", err)
	}
	stop()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("fixture consumer did not stop")
	}
	t.Log("LUOBO_ACTUAL_KAFKA_PERSISTENCE_RECOVERY_PASS")
}
