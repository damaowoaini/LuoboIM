package kafka

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/IBM/sarama"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
)

type observedProducer struct {
	sarama.SyncProducer
	calls, closes atomic.Int32
}

func (p *observedProducer) SendMessage(*sarama.ProducerMessage) (int32, int64, error) {
	if p.calls.Add(1) == 1 {
		return 0, 0, errors.New("synthetic unknown broker result")
	}
	return 1, 2, nil
}
func (p *observedProducer) Close() error { p.closes.Add(1); return nil }

func TestActualOwnedProducerSendErrorCannotBecomeConfirmedByCloseOrLaterOwnerACK(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	if producer, err := NewOwnedKafkaProducer(parent, []string{"127.0.0.1:1"}, "synthetic"); producer != nil || !errors.Is(err, ownedsource.ErrOwner) {
		t.Fatal("missing source owner reached constructor", err)
	}
	original, replacement := &ownedmongo.Observer{}, &ownedmongo.Observer{}
	first, err := ownedmongo.Bind(parent, original)
	if err != nil {
		t.Fatal(err)
	}
	next, err := ownedmongo.Bind(parent, replacement)
	if err != nil {
		t.Fatal(err)
	}
	transport := &observedProducer{}
	next = context.WithValue(next, constant.OperationID, "synthetic-operation")
	next = context.WithValue(next, constant.OpUserID, "synthetic-sender")
	next = context.WithValue(next, constant.OpUserPlatform, "Android")
	next = context.WithValue(next, constant.ConnID, "synthetic-connection")
	// Exercise the production SendMessage path at the actual Sarama boundary.
	// The constructor retains this original source; call-time next must not lease it.
	producer := &Producer{topic: "original-topic", producer: transport, ownedSource: ownedsource.FromContext(first), ownedCtx: first, ownedClose: func() error { return nil }}
	message := &sdkws.MsgData{SendID: "synthetic-sender", RecvID: "synthetic-peer", SessionType: constant.SingleChatType, ContentType: constant.Text, Content: []byte("synthetic")}
	if _, _, err := producer.SendMessage(next, "original-key", message); err == nil {
		t.Fatal("actual broker error was hidden")
	}
	if _, _, err := producer.SendMessage(next, "original-key", message); err != nil {
		t.Fatal("later independent ACK failed", err)
	}
	if err := producer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := producer.Close(); err != nil {
		t.Fatal(err)
	}
	prior, later := original.Snapshot(), replacement.Snapshot()
	if transport.calls.Load() != 2 || transport.closes.Load() != 1 || !prior.SourceOutcomeUnconfirmed || prior.External.KafkaProducer.Started != 2 || prior.External.KafkaProducer.Unconfirmed != 1 || prior.External.KafkaProducer.Succeeded != 1 || later.SourceOutcomeUnconfirmed {
		t.Fatal("successful Close/later ACK relabeled unknown or borrowed new owner")
	}
}
