package msgtransfer

import (
	"context"
	"errors"
	"github.com/IBM/sarama"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/utils"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/kafka"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"google.golang.org/protobuf/proto"
	"sync"
	"sync/atomic"
)

var errOwnedLegacyTransfer = errors.New("original owned legacy transfer source is unconfirmed")

type ownedLegacySource struct {
	parent   context.Context
	cancel   context.CancelFunc
	failed   atomic.Bool
	observer *ownedmongo.Observer
}

func (s *ownedLegacySource) Fail() { s.failed.Store(true); s.cancel() }
func (s *ownedLegacySource) Unconfirmed() bool {
	return s.failed.Load() || (s.observer != nil && s.observer.Snapshot().SourceOutcomeUnconfirmed)
}

// Fixed 100 original routing lanes preserve same-key ordering across claims;
// no unowned distributor/ticker/channel goroutine is created. The claim itself
// owns the complete processing and cannot mark before it finishes.
// Locks are finite fixed lanes, not a growing map keyed by private CID.
type ownedLegacyLanes struct{ locks [ChannelNum]sync.Mutex }

func (l *ownedLegacyLanes) process(key string, work func()) {
	lane := &l.locks[utils.GetHashCode(key)%ChannelNum]
	lane.Lock()
	defer lane.Unlock()
	work()
}

type ownedLegacyClaim struct {
	source       *ownedLegacySource
	run          *kafka.OwnedConsumerRun
	redisHandler *OnlineHistoryRedisConsumerHandler
	mongoHandler *OnlineHistoryMongoConsumerHandler
	lanes        *ownedLegacyLanes
}

func (*ownedLegacyClaim) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (*ownedLegacyClaim) Cleanup(sarama.ConsumerGroupSession) error { return nil }
func (h *ownedLegacyClaim) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case <-h.source.parent.Done():
			return nil
		case <-session.Context().Done():
			return nil
		case record, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			if !h.run.Admitted() || h.source.parent.Err() != nil || session.Context().Err() != nil {
				return nil
			}
			current, cancel := context.WithCancel(h.source.parent)
			stop := context.AfterFunc(session.Context(), cancel)
			ctx, err := kafka.OwnedMQContext(current, record.Headers)
			if err == nil && h.redisHandler != nil {
				h.lanes.process(string(record.Key), func() {
					if ctx.Err() != nil {
						return
					}
					err = h.processRedis(ctx, record)
				})
			} else if err == nil && h.mongoHandler != nil {
				err = h.mongoHandler.handleChatWs2Mongo(ctx, record, string(record.Key), session)
			}
			stop()
			cancel()
			if h.source.parent.Err() != nil {
				if h.source.Unconfirmed() {
					h.run.Fail()
				}
				return nil
			}
			if session.Context().Err() != nil {
				h.source.Fail()
				h.run.Fail()
				return nil
			}
			if err != nil || h.source.Unconfirmed() {
				h.source.Fail()
				h.run.Fail()
				return errOwnedLegacyTransfer
			}
			// No background aggregation queue survives this point. Cancellation or a
			// failed write leaves the original record unmarked for explicit inspection.
			session.MarkMessage(record, "")
		}
	}
}
func (h *ownedLegacyClaim) processRedis(ctx context.Context, record *sarama.ConsumerMessage) error {
	if h.source.Unconfirmed() || ctx.Err() != nil {
		return errOwnedLegacyTransfer
	}
	var msg sdkws.MsgData
	if proto.Unmarshal(record.Value, &msg) != nil {
		return errOwnedLegacyTransfer
	}
	if err := messagedelivery.CheckSupportedMessageContentType(msg.ContentType); err != nil {
		return err
	}
	values := []*ContextMsg{{message: &msg, ctx: ctx}}
	storage, ephemeral, notices, ephemeralNotices, modifiers := h.redisHandler.getPushStorageMsgList(values)
	if len(modifiers) != 0 {
		return errOwnedLegacyTransfer
	}
	key := string(record.Key)
	h.redisHandler.handleMsg(ctx, key, msgprocessor.GetChatConversationIDByMsg(&msg), storage, ephemeral)
	if h.source.Unconfirmed() || ctx.Err() != nil {
		return errOwnedLegacyTransfer
	}
	h.redisHandler.handleNotification(ctx, key, msgprocessor.GetNotificationConversationIDByMsg(&msg), notices, ephemeralNotices)
	if h.source.Unconfirmed() || ctx.Err() != nil {
		return errOwnedLegacyTransfer
	}
	return nil
}
