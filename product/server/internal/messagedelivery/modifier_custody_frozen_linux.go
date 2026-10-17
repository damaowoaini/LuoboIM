//go:build linux

package messagedelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
)

// No new group consumer or offset-reset operation is created. The Agent's
// finite reader verifies real before/after broker/retention/old-group state.
// Its original indivisible batch maps to ONE epoch-bound custody receipt.
func (c *OwnedUnsupportedKafkaCustody) CaptureFrozenModifierPartition(ctx context.Context, probe *ownedcontrol.KafkaProbe, hold *ownedcontrol.AdmissionHold, r ownedcontrol.FrozenPartitionRange) (ownedcontrol.FrozenRangeReceipt, error) {
	if c == nil || c.store == nil || probe == nil || ctx == nil || hold == nil || hold.ValidateExclusion() != nil || hold.HeaderSHA256() != c.policy.HeaderSHA256 || r.ClusterID != c.policy.Header.KafkaClusterID || r.Group != c.policy.ConsumerGroup || !validSHA(r.ProofSHA256) {
		return ownedcontrol.FrozenRangeReceipt{}, ErrUnsupportedCustodyScope
	}
	scope, e := c.sourceRange(KafkaRecord{Topic: r.Topic, Partition: r.Partition, Offset: r.Start}, time.Now().UTC())
	if e != nil {
		return ownedcontrol.FrozenRangeReceipt{}, e
	}
	deadline, _ := custodyDeadline(scope.DeadlineUTC)
	if scope.Encoding != LegacyModifierBatchEncoding || scope.Start != r.Start || scope.End != r.End || scope.SourceProofSHA256 != r.ProofSHA256 || !deadline.Equal(r.Deadline) {
		return ownedcontrol.FrozenRangeReceipt{}, ErrUnsupportedCustodyScope
	}
	// ValidateExclusion intentionally expires after one minute. This entry does
	// not refresh it, extend a deadline or invent a fresh policy on interruption.
	return probe.ReadFrozenPartition(ctx, hold, r, &frozenModifierCustodySink{custody: c, hold: hold, scope: r})
}

type frozenModifierCustodySink struct {
	custody *OwnedUnsupportedKafkaCustody
	hold    *ownedcontrol.AdmissionHold
	scope   ownedcontrol.FrozenPartitionRange
}

func (s *frozenModifierCustodySink) Hold(ctx context.Context, frame ownedcontrol.FrozenKafkaRecord) (out ownedcontrol.KnownCustody, resultErr error) {
	defer func() {
		if resultErr != nil {
			resultErr = BlockUnsupportedCustody(KafkaRecord{Topic: frame.Topic, Partition: frame.Partition, Offset: frame.Offset, Body: frame.Value}, resultErr)
		}
	}()
	if s == nil || s.custody == nil || s.hold == nil || s.hold.ValidateExclusion() != nil || s.hold.HeaderSHA256() != s.custody.policy.HeaderSHA256 {
		return ownedcontrol.KnownCustody{}, ErrUnsupportedCustodyScope
	}
	r := s.scope
	if frame.ClusterID != r.ClusterID || frame.Topic != r.Topic || frame.Partition != r.Partition || frame.Offset < r.Start || frame.Offset >= r.End || frame.RangeProofSHA256 != r.ProofSHA256 || !frame.Deadline.Equal(r.Deadline) || !time.Now().UTC().Before(r.Deadline) {
		return ownedcontrol.KnownCustody{}, ErrUnsupportedCustodyScope
	}
	position, e := json.Marshal([]any{"owned-frozen-kafka-source-v1", frame.ClusterID, frame.Topic, frame.Partition, frame.Offset})
	if e != nil || hash(position) != frame.SourceID || hash(frame.Value) != frame.RawSHA256 {
		return ownedcontrol.KnownCustody{}, ErrConflict
	}
	canonical := frame
	canonical.RecordSHA256 = ""
	body, e := json.Marshal(canonical)
	if e != nil || hash(body) != frame.RecordSHA256 {
		return ownedcontrol.KnownCustody{}, ErrConflict
	}
	original := KafkaOriginalRecord{Encoding: LegacyModifierBatchEncoding, Record: KafkaRecord{Topic: frame.Topic, Partition: frame.Partition, Offset: frame.Offset, Key: bytes.Clone(frame.Key), Body: bytes.Clone(frame.Value), Actor: frozenModifierCustodyActor, OperationID: frame.SourceID}, TimestampUTC: frame.Timestamp.UTC().Format(time.RFC3339Nano), Frozen: &KafkaFrozenCustodyWitness{ClusterID: frame.ClusterID, SourceID: frame.SourceID, RecordSHA256: frame.RecordSHA256, RawSHA256: frame.RawSHA256, RangeProofSHA256: frame.RangeProofSHA256}}
	for _, h := range frame.Headers {
		original.Headers = append(original.Headers, KafkaOriginalHeader{Key: bytes.Clone(h.Key), Value: bytes.Clone(h.Value)})
	}
	original.Record.ContextSHA256 = custodyDigest(original.Headers)
	// The internal actor is SOURCE KEY material, never authenticated authority
	// for messages in this abandoned batch or any grant to a user/history row.
	ctx = context.WithValue(ctx, constant.OpUserID, frozenModifierCustodyActor)
	held, e := s.custody.holdOriginal(ctx, original)
	if e != nil {
		return ownedcontrol.KnownCustody{}, e
	}
	if s.hold.ValidateExclusion() != nil || !held.KnownFor(original) {
		return ownedcontrol.KnownCustody{}, ErrConflict
	}
	return ownedcontrol.KnownCustody{SourceID: frame.SourceID, RecordSHA256: frame.RecordSHA256, RawSHA256: frame.RawSHA256, ReceiptSHA256: custodyDigest(held)}, nil
}
