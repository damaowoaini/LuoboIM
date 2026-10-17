package msgtransfer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IBM/sarama"
	pm "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	kfk "github.com/openimsdk/open-im-server/v3/pkg/common/kafka"
	"google.golang.org/protobuf/proto"
)

type persistenceStoreFixture struct {
	controller.CommonMsgDatabase
	writeFailures, cacheFailures int
	writes, retired, cleaned     int
}

func (f *persistenceStoreFixture) BatchInsertChat2DB(context.Context, string, []*sdkws.MsgData, int64) error {
	f.writes++
	if f.writeFailures > 0 {
		f.writeFailures--
		return errors.New("fixture durable write failure")
	}
	return nil
}
func (f *persistenceStoreFixture) DeleteMessagesFromCache(context.Context, string, []int64) error {
	f.retired++
	if f.cacheFailures > 0 {
		f.cacheFailures--
		return errors.New("fixture cache retirement failure")
	}
	return nil
}
func (f *persistenceStoreFixture) DelUserDeleteMsgsList(context.Context, string, []int64) error {
	f.cleaned++
	return nil
}

type persistenceSession struct {
	sarama.ConsumerGroupSession
	ctx    context.Context
	marked []*sarama.ConsumerMessage
}

func (f *persistenceSession) Context() context.Context { return f.ctx }
func (f *persistenceSession) MarkMessage(record *sarama.ConsumerMessage, _ string) {
	f.marked = append(f.marked, record)
}

type persistenceClaim struct {
	sarama.ConsumerGroupClaim
	records chan *sarama.ConsumerMessage
}

func (*persistenceClaim) Topic() string                              { return "fixture-persistence" }
func (*persistenceClaim) Partition() int32                           { return 0 }
func (*persistenceClaim) HighWaterMarkOffset() int64                 { return 2 }
func (f *persistenceClaim) Messages() <-chan *sarama.ConsumerMessage { return f.records }

func persistenceRecord(t *testing.T) *sarama.ConsumerMessage {
	t.Helper()
	body, err := proto.Marshal(&pm.MsgDataToMongoByMQ{ConversationID: "si_fixture_a_b", LastSeq: 0, MsgData: []*sdkws.MsgData{{Seq: 1, ClientMsgID: "fixture", SendID: "a", RecvID: "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	return &sarama.ConsumerMessage{Topic: "fixture-persistence", Offset: 1, Value: body}
}

func TestPersistenceFailureRetainsCacheAndRetriesBeforeAcknowledgement(t *testing.T) {
	store := &persistenceStoreFixture{writeFailures: 1, cacheFailures: 1}
	handler := &OnlineHistoryMongoConsumerHandler{msgDatabase: store, historyConsumerGroup: &kfk.MConsumerGroup{}}
	record := persistenceRecord(t)
	if err := handler.handleChatWs2Mongo(context.Background(), record, "fixture", nil); err == nil || store.retired != 0 || store.cleaned != 0 {
		t.Fatal("failed durable write retired cache or reported success")
	}
	session := &persistenceSession{ctx: context.Background()}
	claim := &persistenceClaim{records: make(chan *sarama.ConsumerMessage, 1)}
	claim.records <- record
	close(claim.records)
	if err := handler.ConsumeClaim(session, claim); err != nil {
		t.Fatal(err)
	}
	if store.writes != 3 || store.retired != 2 || store.cleaned != 1 || len(session.marked) != 1 || session.marked[0].Offset != 1 {
		t.Fatal("recovered persistence/cleanup/offset sequence invalid", store, len(session.marked))
	}
}

func TestPendingPersistenceAndPoisonRecordNeverAdvanceOffset(t *testing.T) {
	for _, poison := range []bool{false, true} {
		store := &persistenceStoreFixture{writeFailures: 100}
		handler := &OnlineHistoryMongoConsumerHandler{msgDatabase: store, historyConsumerGroup: &kfk.MConsumerGroup{}}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		session := &persistenceSession{ctx: ctx}
		claim := &persistenceClaim{records: make(chan *sarama.ConsumerMessage, 2)}
		record := persistenceRecord(t)
		if poison {
			record.Value = []byte{255}
		}
		claim.records <- record
		claim.records <- persistenceRecord(t)
		close(claim.records)
		err := handler.ConsumeClaim(session, claim)
		cancel()
		if !errors.Is(err, context.Canceled) || len(session.marked) != 0 || store.retired != 0 || store.cleaned != 0 {
			t.Fatal("pending/poison record was acknowledged or lost cache", poison, err, store)
		}
	}
}
