package msgtransfer

import (
	"context"
	"errors"
	"github.com/IBM/sarama"
	"github.com/OpenIMSDK/protocol/constant"
	pbmsg "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/kafka"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"google.golang.org/protobuf/proto"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Only Sarama transport/storage are replaced. The actual OwnedConsumerRun gate,
// original Redis storage splitter/handlers and original Mongo processor execute.
type legacySession struct {
	sarama.ConsumerGroupSession
	ctx   context.Context
	marks chan *sarama.ConsumerMessage
}

func (s legacySession) Context() context.Context                        { return s.ctx }
func (s legacySession) MarkMessage(m *sarama.ConsumerMessage, _ string) { s.marks <- m }

type legacyClaim struct {
	sarama.ConsumerGroupClaim
	messages chan *sarama.ConsumerMessage
}

func (c legacyClaim) Messages() <-chan *sarama.ConsumerMessage { return c.messages }

type legacyGroup struct {
	sarama.ConsumerGroup
	messages      chan *sarama.ConsumerMessage
	marks         chan *sarama.ConsumerMessage
	closes        atomic.Int32
	closeErr      error
	mu            sync.Mutex
	sessionCancel context.CancelFunc
}

func (g *legacyGroup) Consume(ctx context.Context, _ []string, h sarama.ConsumerGroupHandler) error {
	current, cancel := context.WithCancel(ctx)
	defer cancel()
	g.mu.Lock()
	g.sessionCancel = cancel
	g.mu.Unlock()
	session := legacySession{ctx: current, marks: g.marks}
	if err := h.Setup(session); err != nil {
		return err
	}
	return errors.Join(h.ConsumeClaim(session, legacyClaim{messages: g.messages}), h.Cleanup(session))
}
func (g *legacyGroup) Errors() <-chan error { return nil }
func (g *legacyGroup) Close() error         { g.closes.Add(1); return g.closeErr }
func (g *legacyGroup) rebalance() {
	g.mu.Lock()
	cancel := g.sessionCancel
	g.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func legacySource(t *testing.T) *ownedLegacySource {
	t.Helper()
	parent, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	observer := &ownedmongo.Observer{}
	ctx, err := ownedmongo.Bind(parent, observer)
	if err != nil {
		t.Fatal(err)
	}
	return &ownedLegacySource{parent: ctx, cancel: cancel, observer: observer}
}
func startLegacyClaim(t *testing.T, source *ownedLegacySource, handler *ownedLegacyClaim) (*kafka.OwnedConsumerRun, *legacyGroup) {
	t.Helper()
	group := &legacyGroup{messages: make(chan *sarama.ConsumerMessage, 1), marks: make(chan *sarama.ConsumerMessage, 2)}
	run, err := kafka.NewOwnedConsumerRun(source.parent, group, []string{"original-owned-topic"}, handler, source.Fail)
	if err != nil {
		t.Fatal(err)
	}
	handler.run = run
	t.Cleanup(func() {
		source.cancel()
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := run.Stop(stop); err != nil && (group.closeErr == nil || !errors.Is(err, group.closeErr)) {
			t.Error("original claim did not close", err)
		}
	})
	if err := run.StartBounded(source.parent); err != nil {
		t.Fatal(err)
	}
	if err := run.Enable(); err != nil {
		t.Fatal(err)
	}
	return run, group
}
func legacyRecord(t *testing.T, message proto.Message) *sarama.ConsumerMessage {
	t.Helper()
	body, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	headers := []*sarama.RecordHeader{{Key: []byte(constant.OperationID), Value: []byte("synthetic-operation")}, {Key: []byte(constant.OpUserID), Value: []byte("synthetic-sender")}, {Key: []byte(constant.OpUserPlatform), Value: []byte("Android")}, {Key: []byte(constant.ConnID), Value: []byte("synthetic-connection")}}
	return &sarama.ConsumerMessage{Value: body, Key: []byte("synthetic-original-key"), Headers: headers, Offset: 7, Partition: 0}
}
func awaitLegacyMark(t *testing.T, g *legacyGroup, want *sarama.ConsumerMessage) {
	t.Helper()
	select {
	case actual := <-g.marks:
		if actual != want {
			t.Fatal("another original record was marked")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("actual original processing did not finish")
	}
}

type legacyDatabase struct {
	controller.CommonMsgDatabase
	cache   func(context.Context, string, []*sdkws.MsgData) (int64, bool, error)
	insert  func(context.Context, string, []*sdkws.MsgData, int64) error
	cleanup func() error
	mu      sync.Mutex
	order   []string
}

func (d *legacyDatabase) record(value string) {
	d.mu.Lock()
	d.order = append(d.order, value)
	d.mu.Unlock()
}
func (d *legacyDatabase) observed() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string{}, d.order...)
}
func (d *legacyDatabase) BatchInsertChat2Cache(ctx context.Context, cid string, msgs []*sdkws.MsgData) (int64, bool, error) {
	d.record("cache:" + cid)
	if d.cache != nil {
		return d.cache(ctx, cid, msgs)
	}
	for _, msg := range msgs {
		msg.Seq = 1
	}
	return 1, false, nil
}
func (d *legacyDatabase) MsgToMongoMQ(_ context.Context, _ string, cid string, _ []*sdkws.MsgData, last int64) error {
	d.record("mongo-queue:" + cid)
	if last != 1 {
		return errors.New("original allocated sequence lost")
	}
	return nil
}
func (d *legacyDatabase) MsgToPushMQ(_ context.Context, _ string, cid string, _ *sdkws.MsgData) (int32, int64, error) {
	d.record("push-queue:" + cid)
	return 0, 0, nil
}
func (d *legacyDatabase) BatchInsertChat2DB(ctx context.Context, cid string, msgs []*sdkws.MsgData, last int64) error {
	d.record("mongo-write:" + cid)
	if d.insert != nil {
		return d.insert(ctx, cid, msgs, last)
	}
	return nil
}
func (d *legacyDatabase) DeleteMessagesFromCache(_ context.Context, cid string, _ []int64) error {
	d.record("cache-retire:" + cid)
	return nil
}
func (d *legacyDatabase) DelUserDeleteMsgsList(_ context.Context, cid string, _ []int64) error {
	d.record("delete-list:" + cid)
	if d.cleanup != nil {
		return d.cleanup()
	}
	return nil
}
func legacyMessage(history, notice bool) *sdkws.MsgData {
	return &sdkws.MsgData{SendID: "synthetic-sender", RecvID: "synthetic-peer", ClientMsgID: "synthetic-client", SessionType: constant.SingleChatType, ContentType: constant.Text, Content: []byte("synthetic"), Options: msgprocessor.NewOptions(msgprocessor.WithNotNotification(!notice), msgprocessor.WithHistory(history))}
}
func TestOwnedLegacyRawClaimRunsOriginalStorageBranchesBeforeMark(t *testing.T) {
	for _, kind := range []string{"persisted-chat", "ephemeral-chat", "persisted-notice", "ephemeral-notice", "mongo-persistence"} {
		t.Run(kind, func(t *testing.T) {
			history := kind == "persisted-chat" || kind == "persisted-notice"
			notice := kind == "persisted-notice" || kind == "ephemeral-notice"
			source := legacySource(t)
			db := &legacyDatabase{}
			handler := &ownedLegacyClaim{source: source, lanes: &ownedLegacyLanes{}, redisHandler: &OnlineHistoryRedisConsumerHandler{msgDatabase: db, owned: source}}
			message := legacyMessage(history, notice)
			record := legacyRecord(t, message)
			if kind == "mongo-persistence" {
				message.Seq = 1
				record = legacyRecord(t, &pbmsg.MsgDataToMongoByMQ{ConversationID: "si_synthetic-persist", MsgData: []*sdkws.MsgData{message}, LastSeq: 1})
				handler.redisHandler = nil
				handler.mongoHandler = &OnlineHistoryMongoConsumerHandler{msgDatabase: db}
			}
			_, group := startLegacyClaim(t, source, handler)
			group.messages <- record
			awaitLegacyMark(t, group, record)
			cid := msgprocessor.GetChatConversationIDByMsg(message)
			if notice {
				cid = msgprocessor.GetNotificationConversationIDByMsg(message)
			}
			want := []string{"push-queue:" + cid}
			if history {
				want = []string{"cache:" + cid, "mongo-queue:" + cid, "push-queue:" + cid}
			}
			if kind == "mongo-persistence" {
				want = []string{"mongo-write:si_synthetic-persist", "cache-retire:si_synthetic-persist", "delete-list:si_synthetic-persist"}
			}
			if !reflect.DeepEqual(db.observed(), want) || source.Unconfirmed() {
				t.Fatal("original persisted/Seq0 branch, source or pre-mark obligation changed", db.observed())
			}
		})
	}
}
func TestOwnedLegacyUnconfirmedRecordNeverMarksOrAutomaticallyReplays(t *testing.T) {
	for _, kind := range []string{"redis-lost-ack-with-later-nil", "mongo-write-error", "rebalance-during-write"} {
		t.Run(kind, func(t *testing.T) {
			source := legacySource(t)
			db := &legacyDatabase{}
			handler := &ownedLegacyClaim{source: source, lanes: &ownedLegacyLanes{}}
			var record *sarama.ConsumerMessage
			var releaseOnce sync.Once
			entered, release := make(chan struct{}), make(chan struct{})
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			if kind == "redis-lost-ack-with-later-nil" {
				db.cache = func(context.Context, string, []*sdkws.MsgData) (int64, bool, error) {
					ownedsource.Attempt(source.parent, ownedsource.RedisMutation)(true)
					ownedsource.Attempt(source.parent, ownedsource.RedisMutation)(false)
					return 1, false, nil
				}
				handler.redisHandler = &OnlineHistoryRedisConsumerHandler{msgDatabase: db, owned: source}
				record = legacyRecord(t, legacyMessage(true, false))
			} else {
				db.insert = func(context.Context, string, []*sdkws.MsgData, int64) error {
					if kind == "rebalance-during-write" {
						close(entered)
						<-release
						return nil
					}
					return errors.New("synthetic unknown storage result")
				}
				handler.mongoHandler = &OnlineHistoryMongoConsumerHandler{msgDatabase: db}
				message := legacyMessage(true, false)
				message.Seq = 1
				record = legacyRecord(t, &pbmsg.MsgDataToMongoByMQ{ConversationID: "si_synthetic-original", MsgData: []*sdkws.MsgData{message}, LastSeq: 1})
			}
			run, group := startLegacyClaim(t, source, handler)
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			group.messages <- record
			if kind == "rebalance-during-write" {
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("original write did not begin")
				}
				group.rebalance()
				releaseOnce.Do(func() { close(release) })
			}
			select {
			case <-source.parent.Done():
			case <-time.After(3 * time.Second):
				t.Fatal("failed original record did not retire its owner")
			}
			stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := run.Stop(stop); err != nil {
				t.Fatal(err)
			}
			select {
			case <-group.marks:
				t.Fatal("unknown original record advanced its offset")
			default:
			}
			actual := db.observed()
			if len(actual) == 0 || !source.failed.Load() || run.Failure() == nil {
				t.Fatal("unknown work lost its original custody")
			}
			if kind == "redis-lost-ack-with-later-nil" && (len(actual) != 1 || !source.observer.Snapshot().External.RedisMutation.Unknown) {
				t.Fatal("swallowed Redis failure continued to another queue", actual)
			}
			if kind == "mongo-write-error" && !reflect.DeepEqual(actual, []string{"mongo-write:si_synthetic-original"}) {
				t.Fatal("unknown original Mongo write was replayed or cache was retired", actual)
			}
		})
	}
}

func TestOwnedLegacyPerUserCleanupFailureRetainsOriginalOffsetAfterRealProcessor(t *testing.T) {
	source := legacySource(t)
	failure := errors.New("synthetic pre-I/O deletion-list refusal")
	db := &legacyDatabase{cleanup: func() error { return failure }}
	handler := &ownedLegacyClaim{source: source, mongoHandler: &OnlineHistoryMongoConsumerHandler{msgDatabase: db}}
	run, group := startLegacyClaim(t, source, handler)
	message := legacyMessage(true, false)
	message.Seq = 1
	record := legacyRecord(t, &pbmsg.MsgDataToMongoByMQ{ConversationID: "si_synthetic-cleanup", MsgData: []*sdkws.MsgData{message}, LastSeq: 1})
	group.messages <- record
	select {
	case <-source.parent.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("actual cleanup failure did not retire original owner")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := run.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-group.marks:
		t.Fatal("failed final cleanup advanced original offset")
	default:
	}
	if !source.failed.Load() || run.Failure() == nil || !reflect.DeepEqual(db.observed(), []string{"mongo-write:si_synthetic-cleanup", "cache-retire:si_synthetic-cleanup", "delete-list:si_synthetic-cleanup"}) {
		t.Fatal("original processing retried/forgot final failed obligation", db.observed())
	}
	if source.observer.Snapshot().External.RedisMutation.Unknown {
		t.Fatal("known pre-I/O failure was falsely relabeled lost Redis ACK")
	}
}
