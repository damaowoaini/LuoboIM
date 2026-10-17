//go:build linux

package ownedcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/IBM/sarama"
)

// This independent range is NOT relabeled as either ordinary drain plane.
// It is custody only: no CommitOffsets/ResetOffset/JoinGroup/producer method is
// used. Complete frames, including legacy batch envelopes, remain indivisible.
type FrozenPartitionRange struct {
	ClusterID, Topic, Group string
	Partition               int32
	Start, End              int64
	Deadline                time.Time
	ProofSHA256             string
}
type FrozenRangeProof struct {
	Schema                                               int `json:"schema"`
	ClusterID, Topic, Group                              string
	Partition                                            int32
	Start, End, Earliest, ObservedEnd, CommittedNext     int64
	CommittedExists                                      bool
	LeaderEpoch                                          int32
	RetentionMilliseconds, RetentionBytes, CleanupPolicy string
	AdmissionGuardSHA256                                 string
	HeaderSHA256                                         string
	LeaseID                                              string
}

func (p FrozenRangeProof) SHA256() string { body, _ := json.Marshal(p); return sha(body) }

type KafkaRawHeader struct {
	Key   []byte `json:"key"`
	Value []byte `json:"value"`
}
type FrozenKafkaRecord struct {
	ClusterID        string           `json:"clusterID"`
	Topic            string           `json:"topic"`
	Partition        int32            `json:"partition"`
	Offset           int64            `json:"offset"`
	Key              []byte           `json:"key"`
	Value            []byte           `json:"value"`
	Headers          []KafkaRawHeader `json:"headers"`
	Timestamp        time.Time        `json:"timestamp"`
	SourceID         string           `json:"sourceID"`
	RawSHA256        string           `json:"rawSHA256"`
	RecordSHA256     string           `json:"recordSHA256"`
	RangeProofSHA256 string           `json:"rangeProofSHA256"`
	Deadline         time.Time        `json:"deadline"`
}
type KnownCustody struct{ SourceID, RecordSHA256, RawSHA256, ReceiptSHA256 string }
type FrozenRecordSink interface {
	Hold(context.Context, FrozenKafkaRecord) (KnownCustody, error)
}
type FrozenRangeReceipt struct {
	Before, After       FrozenRangeProof
	Records             int64
	ReceiptsChainSHA256 string
	OffsetsUnchanged    bool
}

func validFrozenRange(r FrozenPartitionRange, cluster string) bool {
	return r.ClusterID == cluster && r.Topic != "" && r.Group != "" && !strings.ContainsAny(r.Topic+r.Group, "\x00\r\n") && r.Partition >= 0 && r.Start >= 0 && r.End >= r.Start && !r.Deadline.IsZero() && r.Deadline.Location() == time.UTC && time.Now().Before(r.Deadline)
}
func (p *KafkaProbe) frozenProof(ctx context.Context, hold *AdmissionHold, client sarama.Client, r FrozenPartitionRange) (FrozenRangeProof, error) {
	if ctx.Err() != nil || hold.ValidateExclusion() != nil {
		return FrozenRangeProof{}, ErrOwnedEvidence
	}
	controller, e := client.Controller()
	if e != nil {
		return FrozenRangeProof{}, e
	}
	metadata, e := controller.GetMetadata(&sarama.MetadataRequest{Version: 7, Topics: []string{r.Topic}, AllowAutoTopicCreation: false})
	if e != nil || metadata.ClusterID == nil || *metadata.ClusterID != r.ClusterID || len(metadata.Topics) != 1 || metadata.Topics[0].Name != r.Topic || metadata.Topics[0].Err != sarama.ErrNoError {
		return FrozenRangeProof{}, ErrOwnedEvidence
	}
	for _, broker := range metadata.Brokers {
		if !loopbackBroker(broker.Addr()) {
			return FrozenRangeProof{}, ErrOwnedEvidence
		}
	}
	epoch := int32(-1)
	for _, part := range metadata.Topics[0].Partitions {
		if part.ID == r.Partition {
			if part.Err != sarama.ErrNoError || epoch != -1 {
				return FrozenRangeProof{}, ErrOwnedEvidence
			}
			epoch = part.LeaderEpoch
		}
	}
	if epoch < 0 {
		return FrozenRangeProof{}, ErrOwnedEvidence
	}
	earliest, e := client.GetOffset(r.Topic, r.Partition, sarama.OffsetOldest)
	if e != nil {
		return FrozenRangeProof{}, e
	}
	end, e := client.GetOffset(r.Topic, r.Partition, sarama.OffsetNewest)
	if e != nil || earliest < 0 || earliest > r.Start || r.End > end {
		return FrozenRangeProof{}, ErrOwnedEvidence
	}
	coordinator, e := client.Coordinator(r.Group)
	if e != nil {
		return FrozenRangeProof{}, e
	}
	offsetRequest := &sarama.OffsetFetchRequest{Version: 7, ConsumerGroup: r.Group, RequireStable: true}
	offsetRequest.AddPartition(r.Topic, r.Partition)
	offsets, e := coordinator.FetchOffset(offsetRequest)
	if e != nil || offsets.Err != sarama.ErrNoError {
		return FrozenRangeProof{}, ErrOwnedEvidence
	}
	block := offsets.GetBlock(r.Topic, r.Partition)
	if block == nil || block.Err != sarama.ErrNoError || block.Offset < -1 || block.Offset > end {
		return FrozenRangeProof{}, ErrOwnedEvidence
	}
	// Use the actual controller protocol. Closing an admin built from this client
	// would also close the client; this bounded one-request path owns no admin.
	configs, e := controller.DescribeConfigs(&sarama.DescribeConfigsRequest{Version: 2, Resources: []*sarama.ConfigResource{{Type: sarama.TopicResource, Name: r.Topic, ConfigNames: []string{"retention.ms", "retention.bytes", "cleanup.policy"}}}})
	if e != nil || len(configs.Resources) != 1 || configs.Resources[0].Name != r.Topic || configs.Resources[0].Type != sarama.TopicResource || configs.Resources[0].ErrorCode != 0 {
		return FrozenRangeProof{}, ErrOwnedEvidence
	}
	values := map[string]string{}
	for _, value := range configs.Resources[0].Configs {
		if value == nil || value.Sensitive || values[value.Name] != "" {
			return FrozenRangeProof{}, ErrOwnedEvidence
		}
		values[value.Name] = value.Value
	}
	ms, me := strconv.ParseInt(values["retention.ms"], 10, 64)
	bytes, be := strconv.ParseInt(values["retention.bytes"], 10, 64)
	if me != nil || be != nil || ms < -1 || bytes < -1 || values["cleanup.policy"] != "delete" {
		return FrozenRangeProof{}, ErrOwnedEvidence
	}
	return FrozenRangeProof{Schema: 1, ClusterID: r.ClusterID, Topic: r.Topic, Group: r.Group, Partition: r.Partition, Start: r.Start, End: r.End, Earliest: earliest, ObservedEnd: end, CommittedNext: block.Offset, CommittedExists: block.Offset >= 0, LeaderEpoch: epoch, RetentionMilliseconds: values["retention.ms"], RetentionBytes: values["retention.bytes"], CleanupPolicy: values["cleanup.policy"], AdmissionGuardSHA256: hold.RecordSHA256(), HeaderSHA256: hold.HeaderSHA256(), LeaseID: hold.LeaseID()}, nil
}

// InspectFrozenPartition creates an actual broker/held-source observation for
// a private, explicitly reviewed finite policy. The policy must pin this hash;
// ReadFrozenPartition cannot silently renew it to newly observed coordinates.
func (p *KafkaProbe) InspectFrozenPartition(ctx context.Context, hold *AdmissionHold, r FrozenPartitionRange) (out FrozenRangeProof, resultErr error) {
	if p == nil || ctx == nil || hold == nil || !validFrozenRange(r, p.cluster) {
		return out, ErrOwnedEvidence
	}
	deadline := r.Deadline
	if own, ok := ctx.Deadline(); ok && own.Before(deadline) {
		deadline = own
	}
	current, stop := context.WithDeadline(ctx, deadline)
	defer stop()
	client, e := sarama.NewClient(p.brokers, p.config())
	if e != nil {
		return out, e
	}
	defer func() {
		if client.Close() != nil {
			out = FrozenRangeProof{}
			resultErr = ErrOwnedEvidence
		}
	}()
	return p.frozenProof(current, hold, client, r)
}
func frozenRecord(r FrozenPartitionRange, m *sarama.ConsumerMessage) (FrozenKafkaRecord, error) {
	if m == nil || m.Topic != r.Topic || m.Partition != r.Partition || m.Offset < r.Start || m.Offset >= r.End || len(m.Key)+len(m.Value) > 16*1024*1024 {
		return FrozenKafkaRecord{}, ErrOwnedEvidence
	}
	frame := FrozenKafkaRecord{ClusterID: r.ClusterID, Topic: m.Topic, Partition: m.Partition, Offset: m.Offset, Key: append([]byte(nil), m.Key...), Value: append([]byte(nil), m.Value...), Headers: []KafkaRawHeader{}, Timestamp: m.Timestamp.UTC(), RangeProofSHA256: r.ProofSHA256, Deadline: r.Deadline}
	bytes := len(frame.Key) + len(frame.Value)
	for _, h := range m.Headers {
		if h == nil {
			return frame, ErrOwnedEvidence
		}
		bytes += len(h.Key) + len(h.Value)
		if bytes > 16*1024*1024 || len(frame.Headers) >= 1024 {
			return frame, ErrOwnedEvidence
		}
		frame.Headers = append(frame.Headers, KafkaRawHeader{append([]byte(nil), h.Key...), append([]byte(nil), h.Value...)})
	}
	source, _ := json.Marshal([]any{"owned-frozen-kafka-source-v1", r.ClusterID, r.Topic, r.Partition, m.Offset})
	frame.SourceID = sha(source)
	frame.RawSHA256 = sha(frame.Value)
	body, e := json.Marshal(frame)
	if e != nil {
		return frame, e
	}
	frame.RecordSHA256 = sha(body)
	return frame, nil
}
func (p *KafkaProbe) ReadFrozenPartition(ctx context.Context, hold *AdmissionHold, r FrozenPartitionRange, sink FrozenRecordSink) (out FrozenRangeReceipt, resultErr error) {
	if p == nil || ctx == nil || hold == nil || sink == nil || !isSHA(r.ProofSHA256) || !validFrozenRange(r, p.cluster) {
		return out, ErrOwnedEvidence
	}
	deadline := r.Deadline
	if own, ok := ctx.Deadline(); ok && own.Before(deadline) {
		deadline = own
	}
	current, stop := context.WithDeadline(ctx, deadline)
	defer stop()
	cfg := p.config()
	cfg.Consumer.Return.Errors = true
	cfg.Consumer.Fetch.Max = 16 * 1024 * 1024
	cfg.Consumer.MaxWaitTime = 250 * time.Millisecond
	client, e := sarama.NewClient(p.brokers, cfg)
	if e != nil {
		return out, e
	}
	defer func() {
		if client.Close() != nil {
			out = FrozenRangeReceipt{}
			resultErr = ErrOwnedEvidence
		}
	}()
	before, e := p.frozenProof(current, hold, client, r)
	if e != nil || before.SHA256() != r.ProofSHA256 {
		return out, ErrOwnedEvidence
	}
	out.Before = before
	cursor := r.Start
	chain := sha([]byte("owned-frozen-kafka-custody-chain-v1"))
	if cursor < r.End {
		consumer, e := sarama.NewConsumerFromClient(client)
		if e != nil {
			return out, e
		}
		defer func() {
			if consumer.Close() != nil {
				out = FrozenRangeReceipt{}
				resultErr = ErrOwnedEvidence
			}
		}()
		partition, e := consumer.ConsumePartition(r.Topic, r.Partition, cursor)
		if e != nil {
			return out, e
		}
		defer func() {
			if partition.Close() != nil {
				out = FrozenRangeReceipt{}
				resultErr = ErrOwnedEvidence
			}
		}()
		for cursor < r.End {
			select {
			case <-current.Done():
				return FrozenRangeReceipt{}, current.Err()
			case failure, ok := <-partition.Errors():
				if !ok || failure != nil {
					return FrozenRangeReceipt{}, ErrOwnedEvidence
				}
			case message, ok := <-partition.Messages():
				if !ok || message == nil || message.Offset != cursor || hold.ValidateExclusion() != nil || !time.Now().Before(r.Deadline) {
					return FrozenRangeReceipt{}, ErrOwnedEvidence
				}
				frame, e := frozenRecord(r, message)
				if e != nil {
					return FrozenRangeReceipt{}, e
				}
				receipt, e := sink.Hold(current, frame)
				if e != nil {
					return FrozenRangeReceipt{}, e
				}
				if receipt.SourceID != frame.SourceID || receipt.RecordSHA256 != frame.RecordSHA256 || receipt.RawSHA256 != frame.RawSHA256 || !isSHA(receipt.ReceiptSHA256) {
					return FrozenRangeReceipt{}, ErrOwnedEvidence
				}
				body, _ := json.Marshal([]string{chain, frame.SourceID, frame.RecordSHA256, receipt.ReceiptSHA256})
				chain = sha(body)
				cursor++
				out.Records++
			}
		}
	}
	after, e := p.frozenProof(current, hold, client, r)
	if e != nil || after != before || hold.ValidateExclusion() != nil {
		return FrozenRangeReceipt{}, fmt.Errorf("%w: frozen range drift", ErrOwnedEvidence)
	}
	out.After = after
	out.ReceiptsChainSHA256 = chain
	out.OffsetsUnchanged = true
	return out, nil
}
