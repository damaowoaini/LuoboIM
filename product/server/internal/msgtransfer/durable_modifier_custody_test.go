package msgtransfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"google.golang.org/protobuf/proto"
)

type modifierCustodyBoundary struct {
	hold   func(context.Context, messagedelivery.KafkaOriginalRecord) (messagedelivery.UnsupportedCustodyReceipt, error)
	expire func(context.Context, int64) error
}

func (c *modifierCustodyBoundary) Hold(ctx context.Context, r messagedelivery.KafkaOriginalRecord) (messagedelivery.UnsupportedCustodyReceipt, error) {
	return c.hold(ctx, r)
}
func (*modifierCustodyBoundary) CheckReady(context.Context) error { return nil }
func (c *modifierCustodyBoundary) Expire(ctx context.Context, limit int64) error {
	if c.expire != nil {
		return c.expire(ctx, limit)
	}
	return nil
}

type modifierCustodySession struct {
	*intakeSessionStub
	held []*sarama.ConsumerMessage
}

func (s *modifierCustodySession) MarkMessage(m *sarama.ConsumerMessage, metadata string) {
	if metadata == "known-durable-unsupported-custody-v1" {
		s.mu.Lock()
		s.held = append(s.held, m)
		s.mu.Unlock()
		return
	}
	s.intakeSessionStub.MarkMessage(m, metadata)
}
func TestUnsupportedModifierRealKafkaHandlerSeparatesKnownCustodyFromUnknownAndOrdinary(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		name := "known-custody-then-ordinary"
		if unknown {
			name = "unknown-custody-no-mark-no-replay"
		}
		t.Run(name, func(t *testing.T) {
			legacy := intakeMessage(40)
			body, e := proto.Marshal(&sdkws.MsgData{SendID: "synthetic-owner", RecvID: "peer", SessionType: constant.SingleChatType, ContentType: constant.ReactionMessageModifier, ClientMsgID: "synthetic-old-intent"})
			if e != nil {
				t.Fatal(e)
			}
			legacy.Value = body
			ordinaryCalls, holdCalls := 0, 0
			boundary := &intakeBoundaryStub{accept: func(context.Context, messagedelivery.KafkaRecord, string) (messagedelivery.Receipt, error) {
				ordinaryCalls++
				return messagedelivery.Receipt{SourceID: "synthetic-known-ordinary"}, nil
			}}
			intake := intakeFixture(boundary)
			intake.custody = &modifierCustodyBoundary{hold: func(ctx context.Context, original messagedelivery.KafkaOriginalRecord) (messagedelivery.UnsupportedCustodyReceipt, error) {
				holdCalls++
				if original.Record.Offset != 40 || string(original.Record.Body) != string(legacy.Value) || string(original.Record.Key) != string(legacy.Key) || len(original.Headers) != 4 {
					t.Fatal("actual original record was truncated/re-encoded")
				}
				for i, h := range original.Headers {
					if string(h.Key) != string(legacy.Headers[i].Key) || string(h.Value) != string(legacy.Headers[i].Value) {
						t.Fatal("original header order/bytes lost")
					}
				}
				if unknown {
					return messagedelivery.UnsupportedCustodyReceipt{}, messagedelivery.ErrCommitUnconfirmed
				}
				raw, e := json.Marshal(original)
				if e != nil {
					t.Fatal(e)
				}
				sum := sha256.Sum256(raw)
				return messagedelivery.UnsupportedCustodyReceipt{Kind: "unsupported-modifier-custody-v1", Encoding: original.Encoding, SourceID: uuid.NewString(), RawSHA: hex.EncodeToString(sum[:]), RecordSHA: strings.Repeat("a", 64), PolicySHA: strings.Repeat("b", 64), RangeSHA: strings.Repeat("c", 64), AdmissionGeneration: 1, Topic: original.Record.Topic, Partition: original.Record.Partition, Offset: original.Record.Offset, State: "held-no-projection", Deadline: time.Now().UTC().Add(time.Hour)}, nil
			}}
			handler, e := NewDurableOnlineHistoryHandler(intake)
			if e != nil {
				t.Fatal(e)
			}
			session := &modifierCustodySession{intakeSessionStub: &intakeSessionStub{ctx: context.Background()}}
			e = handler.ConsumeClaim(session, intakeClaim(legacy, intakeMessage(41)))
			if unknown {
				if !errors.Is(e, messagedelivery.ErrCommitUnconfirmed) || len(session.held) != 0 || session.markCount() != 0 || ordinaryCalls != 0 || holdCalls != 1 {
					t.Fatal("unknown custody was ACKed or later work continued", e)
				}
				if e = handler.ConsumeClaim(session, intakeClaim(legacy)); !errors.Is(e, messagedelivery.ErrCommitUnconfirmed) || holdCalls != 1 {
					t.Fatal("failed instance automatically replayed unresolved modifier", e)
				}
			} else if e != nil || len(session.held) != 1 || session.held[0] != legacy || session.markCount() != 1 || ordinaryCalls != 1 || holdCalls != 1 {
				t.Fatal("known quarantine was treated as projection or poisoned ordinary", e)
			}
		})
	}
}

func TestUnsupportedCustodyCleanupJoinsBeforeOwnedPoolsMayClose(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var expirations, closes atomic.Int32
	intake := intakeFixture(&intakeBoundaryStub{})
	intake.custody = &modifierCustodyBoundary{expire: func(ctx context.Context, limit int64) error {
		if limit != 32 {
			return messagedelivery.ErrInvalid
		}
		expirations.Add(1)
		close(entered)
		<-release
		return ctx.Err()
	}}
	broker := &managedKafkaStub{consume: func(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler) error {
		<-ctx.Done()
		return ctx.Err()
	}, close: func() error { closes.Add(1); return nil }}
	managed, e := NewManagedOrdinaryKafka(intake, broker)
	if e != nil {
		t.Fatal(e)
	}
	if e = managed.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		end, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e := managed.Stop(end); e != nil {
			t.Error(e)
		}
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("owned raw cleanup did not start")
	}
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	if e = managed.Stop(stopped); !errors.Is(e, ErrOrdinaryKafkaShutdownPending) {
		t.Fatal("in-flight raw cleanup was declared joined", e)
	}
	close(release)
	end, finish := context.WithTimeout(context.Background(), time.Second)
	defer finish()
	if e = managed.Stop(end); e != nil || expirations.Load() != 1 || closes.Load() != 1 || managed.Failure() != nil {
		t.Fatal("owned cleanup was leaked, duplicated or failure fabricated", e)
	}
}
