package msgtransfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// DurableKafkaIntake is the concrete owned source boundary. Constructor
// arguments are startup/operator inputs, never account request metadata.
// This WIP does not yet provide the policy-aware projection/offline/Seq0
// workers needed to replace the existing StartTransfer activation fence.
// The private interface exposes only the two owned intake operations. Runtime
// construction always creates the concrete membership-bound Mongo Store;
// tests may substitute this boundary without starting a broker or a database.
type ordinaryKafkaStore interface {
	CheckReady(context.Context) error
	AcceptKafkaRecord(context.Context, messagedelivery.KafkaRecord, string, ...messagedelivery.KafkaDeliveryPolicy) (messagedelivery.Receipt, error)
}

type unsupportedKafkaCustody interface {
	Hold(context.Context, messagedelivery.KafkaOriginalRecord) (messagedelivery.UnsupportedCustodyReceipt, error)
	CheckReady(context.Context) error
	Expire(context.Context, int64) error
}

type DurableKafkaIntake struct {
	custody      unsupportedKafkaCustody
	store        ordinaryKafkaStore
	topic        string
	policySHA    string
	policy       messagedelivery.KafkaDeliveryPolicy
	failureMu    sync.Mutex
	firstFailure error
	failed       chan struct{}
}

func NewDurableKafkaIntake(db *mongo.Database, epoch, activation, topic, policySHA string) (*DurableKafkaIntake, error) {
	if topic == "" || strings.TrimSpace(topic) != topic || len(topic) > 249 || strings.ContainsAny(topic, "\x00\r\n") {
		return nil, messagedelivery.ErrInvalid
	}
	h, err := hex.DecodeString(policySHA)
	if err != nil || len(h) != 32 || hex.EncodeToString(h) != policySHA {
		return nil, messagedelivery.ErrInvalid
	}
	s, err := messagedelivery.NewMembershipBoundStore(db, epoch, activation)
	if err != nil {
		return nil, err
	}
	policy, err := FreezeOwnedKafkaPolicy(db.Name(), epoch, activation)
	if err != nil {
		return nil, err
	}
	actual, err := policy.Fingerprint()
	if err != nil || actual != policySHA {
		return nil, messagedelivery.ErrUnprepared
	}
	return &DurableKafkaIntake{store: s, topic: topic, policySHA: policySHA, policy: policy, failed: make(chan struct{})}, nil
}

// NewDurableKafkaIntakeWithOfflineRuntime consumes only the policy belonging
// to a successfully constructed owned runtime. Its current registry may bind
// actual credential material, so its fingerprint need not equal the raw
// configuration seed used by the historical five-argument constructor.
func NewDurableKafkaIntakeWithOfflineRuntime(db *mongo.Database, epoch, activation, topic string, runtime *OwnedOrdinaryOfflineRuntime) (*DurableKafkaIntake, error) {
	if db == nil || runtime == nil || runtime.worker == nil || runtime.databaseHandle == nil || runtime.databaseHandle.Client() != db.Client() || runtime.databaseHandle.Name() != db.Name() || runtime.lifetime == nil || runtime.lifetime.Err() != nil || topic == "" || strings.TrimSpace(topic) != topic || len(topic) > 249 || strings.ContainsAny(topic, "\x00\r\n") {
		return nil, messagedelivery.ErrInvalid
	}
	seed, err := FreezeOwnedKafkaPolicy(db.Name(), epoch, activation)
	if err != nil {
		return nil, err
	}
	policy, err := intakeOwnedRuntimePolicy(db.Name(), epoch, activation, seed, runtime)
	if err != nil {
		return nil, err
	}
	policySHA, err := policy.Fingerprint()
	if err != nil {
		return nil, err
	}
	store, err := messagedelivery.NewRuntimeMembershipStore(runtime.lifetime, db, epoch, activation, "ordinary-intake")
	if err != nil {
		return nil, err
	}
	intake := &DurableKafkaIntake{store: store, topic: topic, policySHA: policySHA, policy: policy, failed: make(chan struct{})}
	if err := intake.loadOwnedUnsupportedCustody(runtime.lifetime, db, epoch, activation); err != nil {
		return nil, err
	}
	return intake, nil
}

func intakeOwnedRuntimePolicy(database, epoch, activation string, seed messagedelivery.KafkaDeliveryPolicy, runtime *OwnedOrdinaryOfflineRuntime) (messagedelivery.KafkaDeliveryPolicy, error) {
	if runtime == nil || runtime.database != database || runtime.epoch != epoch || runtime.activation != activation || runtime.bindingSeed != seed || seed.Version != "ordinary-kafka-v1" {
		return messagedelivery.KafkaDeliveryPolicy{}, messagedelivery.ErrUnprepared
	}
	expected := seed
	expected.VendorBinding = runtime.policy.VendorBinding
	var err error
	expected.ConfigurationSHA256, err = kafkaOwnedDigest([]any{database, epoch, activation, expected.Discovery, expected.EncryptionRequired, expected.BeforeGroupPushBinding, expected.OnlinePushBinding, expected.OfflinePushBinding, expected.Vendor, expected.VendorBinding})
	if err != nil || expected != runtime.policy {
		return messagedelivery.KafkaDeliveryPolicy{}, messagedelivery.ErrUnprepared
	}
	if _, err := expected.Fingerprint(); err != nil {
		return messagedelivery.KafkaDeliveryPolicy{}, err
	}
	return expected, nil
}

func (i *DurableKafkaIntake) CheckReady(ctx context.Context) error {
	if i == nil || i.store == nil || ctx == nil {
		return messagedelivery.ErrUnprepared
	}
	if err := i.store.CheckReady(ctx); err != nil {
		return err
	}
	if i.custody != nil {
		return i.custody.CheckReady(ctx)
	}
	return nil
}

// No broker offsets are marked while a body is merely buffered in memory.
// The real handler's ConsumeClaim delegates here before entering its legacy
// batching/timer goroutines. A known receipt is enough to ACK source custody;
// projection completion has its own durable obligations, not a repeated write.
func (i *DurableKafkaIntake) consumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	if i == nil || i.store == nil || session == nil || claim == nil || claim.Topic() != i.topic || session.Context() == nil {
		return messagedelivery.ErrInvalid
	}
	for {
		if err := i.failure(); err != nil {
			return err
		}
		select {
		case <-session.Context().Done():
			return session.Context().Err()
		case message, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			if message == nil || message.Topic != claim.Topic() || message.Partition != claim.Partition() || message.Offset < 0 {
				return messagedelivery.ErrInvalid
			}
			ctx, record, err := ownedKafkaRecord(session.Context(), message)
			if err != nil {
				return err
			}
			work, cancel := context.WithTimeout(ctx, 30*time.Second)
			var typed sdkws.MsgData
			if proto.Unmarshal(record.Body, &typed) == nil && messagedelivery.CheckSupportedMessageContentType(typed.ContentType) == messagedelivery.ErrUnsupportedModifier {
				if i.custody == nil {
					cancel()
					return messagedelivery.BlockUnsupportedCustody(record, messagedelivery.ErrUnsupportedCustodyScope)
				}
				original := messagedelivery.KafkaOriginalRecord{Encoding: messagedelivery.OrdinaryModifierEncoding, Record: record, TimestampUTC: message.Timestamp.UTC().Format(time.RFC3339Nano), BlockTimestampUTC: message.BlockTimestamp.UTC().Format(time.RFC3339Nano)}
				for _, h := range message.Headers {
					original.Headers = append(original.Headers, messagedelivery.KafkaOriginalHeader{Key: bytes.Clone(h.Key), Value: bytes.Clone(h.Value)})
				}
				var held messagedelivery.UnsupportedCustodyReceipt
				held, err = i.custody.Hold(work, original)
				cancel()
				if err != nil {
					return messagedelivery.BlockUnsupportedCustody(record, err)
				}
				if !time.Now().UTC().Before(held.Deadline) {
					return messagedelivery.BlockUnsupportedCustody(record, messagedelivery.ErrUnsupportedCustodyExpired)
				}
				if !held.KnownFor(original) {
					return messagedelivery.BlockUnsupportedCustody(record, messagedelivery.ErrConflict)
				}
				session.MarkMessage(message, "known-durable-unsupported-custody-v1")
				continue
			}
			_, err = i.store.AcceptKafkaRecord(work, record, i.policySHA, i.policy)
			cancel()
			if err != nil {
				return err
			} // including unknown commit: no source ACK/new ID.
			session.MarkMessage(message, "known-durable-source-receipt-v1")
		}
	}
}

func ownedKafkaRecord(parent context.Context, message *sarama.ConsumerMessage) (context.Context, messagedelivery.KafkaRecord, error) {
	if parent == nil || message == nil || len(message.Headers) != 4 {
		return nil, messagedelivery.KafkaRecord{}, messagedelivery.ErrInvalid
	}
	keys := []string{constant.OperationID, constant.OpUserID, constant.OpUserPlatform, constant.ConnID}
	allowed := make(map[string]bool, 4)
	for _, key := range keys {
		allowed[key] = true
	}
	values := make(map[string]string, 4)
	for _, header := range message.Headers {
		if header == nil || !allowed[string(header.Key)] {
			return nil, messagedelivery.KafkaRecord{}, messagedelivery.ErrInvalid
		}
		key, value := string(header.Key), string(header.Value)
		if _, duplicate := values[key]; duplicate || len(value) > 2048 || strings.ContainsAny(value, "\x00\r\n") {
			return nil, messagedelivery.KafkaRecord{}, messagedelivery.ErrInvalid
		}
		values[key] = value
	}
	if values[constant.OpUserID] == "" {
		return nil, messagedelivery.KafkaRecord{}, messagedelivery.ErrInvalid
	}
	ctx := parent
	ordered := make([]string, 0, 4)
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			return nil, messagedelivery.KafkaRecord{}, messagedelivery.ErrInvalid
		}
		ctx = context.WithValue(ctx, key, value)
		ordered = append(ordered, value)
	}
	canonical, err := json.Marshal(ordered)
	if err != nil {
		return nil, messagedelivery.KafkaRecord{}, errors.New("invalid owned Kafka context")
	}
	digest := sha256.Sum256(canonical)
	return ctx, messagedelivery.KafkaRecord{Topic: message.Topic, Partition: message.Partition, Offset: message.Offset, Key: append([]byte(nil), message.Key...), Body: append([]byte(nil), message.Value...), Actor: values[constant.OpUserID], OperationID: values[constant.OperationID], ContextSHA256: hex.EncodeToString(digest[:])}, nil
}

// NewDurableOnlineHistoryHandler does not create the legacy100 Run goroutines
// or perform any I/O. A managed consumer lifecycle must own the supplied
// handler and close/join it; old RegisterHandleAndConsumer is NOT used for it.
func NewDurableOnlineHistoryHandler(intake *DurableKafkaIntake) (*OnlineHistoryRedisConsumerHandler, error) {
	if intake == nil || intake.store == nil {
		return nil, messagedelivery.ErrInvalid
	}
	return &OnlineHistoryRedisConsumerHandler{durableIntake: intake}, nil
}

// A poison/unknown record stops this owned intake instance. Only a fresh,
// explicitly restarted instance can resolve the same broker source again.
func (i *DurableKafkaIntake) recordFailure(err error) {
	if i == nil || err == nil {
		return
	}
	i.failureMu.Lock()
	defer i.failureMu.Unlock()
	if i.firstFailure == nil {
		i.firstFailure = err
		if i.failed != nil {
			close(i.failed)
		}
	}
}
func (i *DurableKafkaIntake) failure() error {
	if i == nil {
		return messagedelivery.ErrUnprepared
	}
	i.failureMu.Lock()
	defer i.failureMu.Unlock()
	return i.firstFailure
}
