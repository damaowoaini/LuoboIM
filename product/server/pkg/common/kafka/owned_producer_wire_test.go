package kafka

import (
	"context"
	"errors"
	"github.com/IBM/sarama"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"testing"
)

// This is an actual Sarama protocol mock broker, not a business Kafka service
// or an outer fake SyncProducer. Its queued later ACK is a regression trap:
// if Sarama retries the timed-out Produce, the API would falsely return nil.
func TestOwnedKafkaActualSaramaTimeoutSendsOneProduceAndCannotBorrowLaterOwner(t *testing.T) {
	for _, topic := range []string{"legacy-latest", "legacy-mongo", "legacy-push"} {
		t.Run(topic, func(t *testing.T) {
			prior := config.Config.Kafka
			t.Cleanup(func() { config.Config.Kafka = prior })
			config.Config.Kafka.TLS = nil
			config.Config.Kafka.Username = ""
			config.Config.Kafka.Password = ""
			config.Config.Kafka.ProducerAck = "wait_for_all"
			config.Config.Kafka.CompressType = "snappy"
			for _, key := range []string{"KAFKA_ADDRESS", "KAFKA_PORT", "KAFKA_USERNAME", "KAFKA_PASSWORD"} {
				t.Setenv(key, "")
			}
			broker := sarama.NewMockBroker(t, 1)
			defer broker.Close()
			metadata := sarama.NewMockMetadataResponse(t).SetBroker(broker.Addr(), broker.BrokerID()).SetLeader(topic, 0, broker.BrokerID())
			broker.SetHandlerByMap(map[string]sarama.MockResponse{
				"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t),
				"MetadataRequest":    metadata,
				"ProduceRequest":     sarama.NewMockSequence(sarama.NewMockProduceResponse(t).SetError(topic, 0, sarama.ErrRequestTimedOut), sarama.NewMockProduceResponse(t).SetError(topic, 0, sarama.ErrNoError)),
			})
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := &ownedmongo.Observer{}
			ctx, err := ownedmongo.Bind(parent, original)
			if err != nil {
				t.Fatal(err)
			}
			ctx = context.WithValue(ctx, constant.OperationID, "wire-original")
			ctx = context.WithValue(ctx, constant.OpUserID, "synthetic-sender")
			ctx = context.WithValue(ctx, constant.OpUserPlatform, "Android")
			ctx = context.WithValue(ctx, constant.ConnID, "synthetic-connection")
			producer, err := NewOwnedKafkaProducer(ctx, []string{broker.Addr()}, topic)
			if producer != nil {
				t.Cleanup(func() { _ = producer.Close() })
			}
			if err != nil {
				t.Fatal(err)
			}
			message := &sdkws.MsgData{SendID: "synthetic-sender", RecvID: "synthetic-peer", SessionType: constant.SingleChatType, ContentType: constant.Text, Content: []byte("synthetic")}
			if _, _, err := producer.SendMessage(ctx, "original-key", message); !errors.Is(err, sarama.ErrRequestTimedOut) {
				t.Fatal("actual timed-out Produce hidden by internal retry", err)
			}
			count := func() int {
				n := 0
				for _, event := range broker.History() {
					if req, ok := event.Request.(*sarama.ProduceRequest); ok {
						n++
						if req.RequiredAcks != sarama.WaitForAll {
							t.Error("original wire ACK policy changed")
						}
					}
				}
				return n
			}
			if count() != 1 || !original.Snapshot().SourceOutcomeUnconfirmed || producer.config.Producer.Compression != sarama.CompressionSnappy || producer.config.Producer.Retry.Max != 0 || producer.config.Metadata.Retry.Max != 0 {
				t.Fatal("actual internal retry/configuration/source outcome differs")
			}
			cancel()
			laterParent, laterCancel := context.WithCancel(context.Background())
			defer laterCancel()
			later := &ownedmongo.Observer{}
			next, err := ownedmongo.Bind(laterParent, later)
			if err != nil {
				t.Fatal(err)
			}
			next = context.WithValue(next, constant.OperationID, "wire-later")
			next = context.WithValue(next, constant.OpUserID, "synthetic-sender")
			next = context.WithValue(next, constant.OpUserPlatform, "Android")
			if _, _, err := producer.SendMessage(next, "original-key", message); err == nil {
				t.Fatal("later caller revived original closed owner")
			}
			if err := producer.Close(); err != nil {
				t.Fatal(err)
			}
			if count() != 1 || later.Snapshot().External.KafkaProducer.Started != 0 || !original.Snapshot().SourceOutcomeUnconfirmed {
				t.Fatal("Close or later owner changed original single-wire unknown")
			}
		})
	}
}
