package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/mcontext"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const UnsupportedCustodyCollection = "message_delivery_unsupported_custody_v1"
const unsupportedCustodyKind = "unsupported-modifier-custody-v1"
const frozenModifierCustodyActor = "owned-frozen-unsupported-custody"

var ErrUnsupportedCustodyScope = errors.New("unsupported modifier source has no approved finite custody scope")
var ErrUnsupportedCustodyExpired = errors.New("unsupported modifier custody deadline expired; original intent unresolved")

type KafkaOriginalHeader struct {
	Key   []byte `bson:"key"`
	Value []byte `bson:"value"`
}
type KafkaFrozenCustodyWitness struct{ ClusterID, SourceID, RecordSHA256, RawSHA256, RangeProofSHA256 string }
type KafkaOriginalRecord struct {
	Frozen            *KafkaFrozenCustodyWitness `bson:"frozen,omitempty" json:"frozen,omitempty"`
	Encoding          string                     `bson:"encoding"`
	Record            KafkaRecord                `bson:"record"`
	Headers           []KafkaOriginalHeader      `bson:"headers"`
	TimestampUTC      string                     `bson:"timestamp_utc"`
	BlockTimestampUTC string                     `bson:"block_timestamp_utc"`
}
type UnsupportedCustodyReceipt struct {
	ID                  string    `bson:"_id"`
	Kind                string    `bson:"kind"`
	Epoch               string    `bson:"epoch"`
	Activation          string    `bson:"activation_sha256"`
	SourceID            string    `bson:"source_id"`
	RecordSHA           string    `bson:"record_sha256"`
	RawSHA              string    `bson:"raw_sha256"`
	PolicySHA           string    `bson:"custody_policy_sha256"`
	RangeSHA            string    `bson:"custody_range_sha256"`
	AdmissionGeneration int64     `bson:"admission_generation"`
	Topic               string    `bson:"topic"`
	Partition           int32     `bson:"partition"`
	Offset              int64     `bson:"offset"`
	ContentType         int32     `bson:"content_type"`
	Encoding            string    `bson:"encoding"`
	MessageCount        int64     `bson:"message_count"`
	ContentTypes        []int32   `bson:"content_types"`
	SourceCIDs          []string  `bson:"source_cids"`
	NormalCID           string    `bson:"normal_cid"`
	BranchCID           string    `bson:"branch_cid"`
	NotificationCID     string    `bson:"notification_cid"`
	OwnerHashes         []string  `bson:"owner_hashes"`
	CreatedAt           time.Time `bson:"created_at"`
	Deadline            time.Time `bson:"deadline"`
	State               string    `bson:"state"`
	Revision            int64     `bson:"revision"`
}
type unsupportedCustodyRaw struct {
	ID         string              `bson:"_id"`
	Kind       string              `bson:"kind"`
	Epoch      string              `bson:"epoch"`
	ReceiptSHA string              `bson:"receipt_sha256"`
	RawSHA     string              `bson:"raw_sha256"`
	Original   KafkaOriginalRecord `bson:"original"`
	Deadline   time.Time           `bson:"deadline"`
}

func (r UnsupportedCustodyReceipt) KnownFor(original KafkaOriginalRecord) bool {
	return r.Kind == unsupportedCustodyKind && validUUID(r.SourceID) && validSHA(r.RawSHA) && validSHA(r.RecordSHA) && validSHA(r.PolicySHA) && validSHA(r.RangeSHA) && r.AdmissionGeneration > 0 && r.Encoding == original.Encoding && r.RawSHA == custodyDigest(original) && r.Topic == original.Record.Topic && r.Partition == original.Record.Partition && r.Offset == original.Record.Offset && (r.State == "held-no-projection" || r.State == "redacted-unapplied") && time.Now().UTC().Before(r.Deadline)
}
func custodyDigest(v any) string {
	body, e := json.Marshal(v)
	if e != nil {
		return ""
	}
	return hash(body)
}
func originalHeadersValid(raw KafkaOriginalRecord) bool {
	if raw.Encoding == LegacyModifierBatchEncoding {
		if raw.Frozen == nil || raw.Record.Actor != frozenModifierCustodyActor || raw.Record.OperationID != raw.Frozen.SourceID || raw.Record.ContextSHA256 != custodyDigest(raw.Headers) || len(raw.Headers) > 1024 {
			return false
		}
		total := len(raw.Record.Key) + len(raw.Record.Body)
		for _, h := range raw.Headers {
			total += len(h.Key) + len(h.Value)
			if total > 8<<20 {
				return false
			}
		}
		return true
	}
	keys := []string{constant.OperationID, constant.OpUserID, constant.OpUserPlatform, constant.ConnID}
	if len(raw.Headers) != len(keys) {
		return false
	}
	values := map[string]string{}
	for _, h := range raw.Headers {
		key := string(h.Key)
		allowed := false
		for _, k := range keys {
			allowed = allowed || k == key
		}
		if !allowed || len(h.Value) > 2048 || strings.ContainsAny(string(h.Value), "\x00\r\n") {
			return false
		}
		if _, ok := values[key]; ok {
			return false
		}
		values[key] = string(h.Value)
	}
	ordered := []string{}
	for _, k := range keys {
		value, ok := values[k]
		if !ok {
			return false
		}
		ordered = append(ordered, value)
	}
	return custodyDigest(ordered) == raw.Record.ContextSHA256 && values[constant.OpUserID] == raw.Record.Actor && values[constant.OperationID] == raw.Record.OperationID
}
func (c *OwnedUnsupportedKafkaCustody) Hold(ctx context.Context, raw KafkaOriginalRecord) (UnsupportedCustodyReceipt, error) {
	if raw.Encoding != OrdinaryModifierEncoding || raw.Frozen != nil {
		return UnsupportedCustodyReceipt{}, ErrUnsupportedCustodyScope
	}
	return c.holdOriginal(ctx, raw)
}

// Only the owned frozen-range adapter calls this for the separate legacy
// codec, after its actual AdmissionHold and fixed broker proof checks.
func (c *OwnedUnsupportedKafkaCustody) holdOriginal(ctx context.Context, raw KafkaOriginalRecord) (UnsupportedCustodyReceipt, error) {
	if c == nil || c.store == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil || mcontext.GetOpUserID(ctx) != raw.Record.Actor || !originalHeadersValid(raw) {
		return UnsupportedCustodyReceipt{}, ErrInvalid
	}
	scope, e := c.sourceRange(raw.Record, time.Now().UTC())
	if e != nil {
		return UnsupportedCustodyReceipt{}, e
	}
	s := c.store
	source, recordSHA, e := s.kafkaIdentity(raw.Record)
	if e != nil {
		return UnsupportedCustodyReceipt{}, e
	}
	if raw.Encoding != scope.Encoding {
		return UnsupportedCustodyReceipt{}, ErrUnsupportedCustodyScope
	}
	if raw.Encoding == LegacyModifierBatchEncoding && (raw.Frozen == nil || !identity(raw.Frozen.ClusterID) || !validSHA(raw.Frozen.SourceID) || !validSHA(raw.Frozen.RecordSHA256) || raw.Frozen.RawSHA256 != hash(raw.Record.Body) || raw.Frozen.RangeProofSHA256 != scope.SourceProofSHA256) {
		return UnsupportedCustodyReceipt{}, ErrUnsupportedCustodyScope
	}
	meta, e := modifierCustodyMessages(raw)
	if e != nil {
		return UnsupportedCustodyReceipt{}, e
	}
	deadline, _ := custodyDeadline(scope.DeadlineUTC)
	now := time.Now().UTC().Truncate(time.Millisecond)
	// Exact UTC milliseconds are an explicit contract boundary, matching BSON
	// dates. Never silently round an approved deadline to another instant.
	if !deadline.Equal(deadline.Truncate(time.Millisecond)) {
		return UnsupportedCustodyReceipt{}, ErrInvalid
	}
	expected := UnsupportedCustodyReceipt{ID: key(s.epoch, "unsupported-kafka-hold", source), Kind: unsupportedCustodyKind, Epoch: s.epoch, Activation: s.activationSHA, SourceID: source, RecordSHA: recordSHA, RawSHA: custodyDigest(raw), PolicySHA: c.inputSHA, RangeSHA: custodyDigest(scope), AdmissionGeneration: c.policy.AdmissionGeneration, Topic: raw.Record.Topic, Partition: raw.Record.Partition, Offset: raw.Record.Offset, ContentType: meta.Types[0], Encoding: raw.Encoding, MessageCount: meta.Count, ContentTypes: meta.Types, SourceCIDs: meta.CIDs, NormalCID: meta.Normal, BranchCID: meta.Branch, NotificationCID: meta.Notification, OwnerHashes: meta.Owners, CreatedAt: now, Deadline: deadline, State: "held-no-projection", Revision: 1}
	if e = c.CheckReady(ctx); e != nil {
		return UnsupportedCustodyReceipt{}, e
	}
	var result UnsupportedCustodyReceipt
	e = s.transaction(ctx, func(tx context.Context) error {
		if _, e := c.sourceRange(raw.Record, time.Now().UTC()); e != nil {
			return e
		}
		old, e := c.readReceipt(tx, source)
		if e == nil {
			copy := old
			copy.CreatedAt = expected.CreatedAt
			copy.State = expected.State
			copy.Revision = expected.Revision
			if custodyDigest(copy) != custodyDigest(expected) {
				return ErrConflict
			}
			if old.State == "expired-unresolved" {
				return ErrUnsupportedCustodyExpired
			}
			if old.State != "held-no-projection" && old.State != "redacted-unapplied" {
				return ErrConflict
			}
			if old.State == "held-no-projection" {
				if _, e = c.readRaw(tx, old); e != nil {
					return e
				}
			}
			result = old
			return nil
		}
		if !errors.Is(e, mongo.ErrNoDocuments) {
			return e
		}
		// A projected/accepted old source is not silently reclassified as custody.
		for _, kind := range []string{"source", "kafka-source"} {
			e = s.db.Collection(Receipts).FindOne(tx, bson.M{"_id": key(s.epoch, kind, source)}).Err()
			if e == nil {
				return ErrConflict
			}
			if !errors.Is(e, mongo.ErrNoDocuments) {
				return e
			}
		}
		if e = c.claimUnsupportedCustodyGeneration(tx); e != nil {
			return e
		}
		body := unsupportedCustodyRaw{ID: expected.ID, Kind: unsupportedCustodyKind, Epoch: s.epoch, ReceiptSHA: custodyDigest(expected), RawSHA: expected.RawSHA, Original: raw, Deadline: deadline}
		if _, e = s.db.Collection(UnsupportedCustodyCollection).InsertOne(tx, body); e != nil {
			return e
		}
		if _, e = s.db.Collection(Receipts).InsertOne(tx, expected); e != nil {
			return e
		}
		result = expected
		return nil
	})
	if e != nil {
		return UnsupportedCustodyReceipt{}, e
	}
	if _, e = c.sourceRange(raw.Record, time.Now().UTC()); e != nil {
		return UnsupportedCustodyReceipt{}, e
	}
	return result, nil
}
func (c *OwnedUnsupportedKafkaCustody) readReceipt(ctx context.Context, source string) (UnsupportedCustodyReceipt, error) {
	var r UnsupportedCustodyReceipt
	var raw bson.Raw
	if e := c.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(c.store.epoch, "unsupported-kafka-hold", source)}).Decode(&raw); e != nil {
		return r, e
	}
	fields, e := raw.Elements()
	if e != nil || len(fields) != 26 {
		return r, ErrConflict
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f.Key()] {
			return r, ErrConflict
		}
		seen[f.Key()] = true
	}
	for _, name := range []string{"_id", "kind", "epoch", "activation_sha256", "source_id", "record_sha256", "raw_sha256", "custody_policy_sha256", "custody_range_sha256", "topic", "encoding", "normal_cid", "branch_cid", "notification_cid", "state"} {
		if _, ok := raw.Lookup(name).StringValueOK(); !ok {
			return r, ErrConflict
		}
	}
	for _, name := range []string{"admission_generation", "offset", "message_count", "revision"} {
		if _, ok := raw.Lookup(name).Int64OK(); !ok {
			return r, ErrConflict
		}
	}
	for _, name := range []string{"partition", "content_type"} {
		if _, ok := raw.Lookup(name).Int32OK(); !ok {
			return r, ErrConflict
		}
	}
	for _, name := range []string{"source_cids", "owner_hashes", "content_types"} {
		if _, ok := raw.Lookup(name).ArrayOK(); !ok {
			return r, ErrConflict
		}
	}
	for _, name := range []string{"created_at", "deadline"} {
		if _, ok := raw.Lookup(name).DateTimeOK(); !ok {
			return r, ErrConflict
		}
	}
	if bson.Unmarshal(raw, &r) != nil {
		return r, ErrConflict
	}
	if r.Kind != unsupportedCustodyKind || !validUUID(source) || r.SourceID != source || r.Epoch != c.store.epoch || r.Activation != c.store.activationSHA || r.ID != key(r.Epoch, "unsupported-kafka-hold", source) || !validSHA(r.RecordSHA) || !validSHA(r.RawSHA) || !validSHA(r.PolicySHA) || !validSHA(r.RangeSHA) || r.AdmissionGeneration < 1 || !identity(r.Topic) || r.Partition < 0 || r.Offset < 0 || CheckSupportedMessageContentType(r.ContentType) != ErrUnsupportedModifier || !identity(r.NormalCID) || !identity(r.BranchCID) || !identity(r.NotificationCID) || len(r.OwnerHashes) < 1 || r.MessageCount < 1 || len(r.SourceCIDs) < 1 || len(r.ContentTypes) < 1 || len(r.ContentTypes) > 2 || r.Encoding != OrdinaryModifierEncoding && r.Encoding != LegacyModifierBatchEncoding || r.Revision < 1 || r.CreatedAt.IsZero() || r.Deadline.IsZero() || !r.CreatedAt.Before(r.Deadline) || r.State != "held-no-projection" && r.State != "redacted-unapplied" && r.State != "expired-unresolved" {
		return r, ErrConflict
	}
	seen = map[string]bool{}
	for _, owner := range r.OwnerHashes {
		if !validSHA(owner) || seen[owner] {
			return r, ErrConflict
		}
		seen[owner] = true
	}
	for i, cid := range r.SourceCIDs {
		if !identity(cid) || i > 0 && cid <= r.SourceCIDs[i-1] {
			return r, ErrConflict
		}
	}
	for i, kind := range r.ContentTypes {
		if CheckSupportedMessageContentType(kind) != ErrUnsupportedModifier || i > 0 && kind <= r.ContentTypes[i-1] {
			return r, ErrConflict
		}
	}
	if r.ContentTypes[0] != r.ContentType || r.Encoding == OrdinaryModifierEncoding && r.MessageCount != 1 {
		return r, ErrConflict
	}
	for _, expected := range []string{r.NormalCID, r.BranchCID, r.NotificationCID} {
		found := false
		for _, cid := range r.SourceCIDs {
			found = found || cid == expected
		}
		if !found {
			return r, ErrConflict
		}
	}
	return r, nil
}
func (c *OwnedUnsupportedKafkaCustody) readRaw(ctx context.Context, receipt UnsupportedCustodyReceipt) (KafkaOriginalRecord, error) {
	if !time.Now().UTC().Before(receipt.Deadline) {
		return KafkaOriginalRecord{}, ErrUnsupportedCustodyExpired
	}
	var row unsupportedCustodyRaw
	if e := c.store.db.Collection(UnsupportedCustodyCollection).FindOne(ctx, bson.M{"_id": receipt.ID}).Decode(&row); e != nil {
		return KafkaOriginalRecord{}, e
	}
	if row.Kind != unsupportedCustodyKind || row.Epoch != receipt.Epoch || row.ReceiptSHA != custodyDigest(receipt) || row.RawSHA != receipt.RawSHA || !row.Deadline.Equal(receipt.Deadline) || custodyDigest(row.Original) != row.RawSHA || !originalHeadersValid(row.Original) {
		return KafkaOriginalRecord{}, ErrConflict
	}
	source, proof, e := c.store.kafkaIdentity(row.Original.Record)
	if e != nil || source != receipt.SourceID || proof != receipt.RecordSHA {
		return KafkaOriginalRecord{}, ErrConflict
	}
	meta, e := modifierCustodyMessages(row.Original)
	if e != nil || row.Original.Encoding != receipt.Encoding || meta.Count != receipt.MessageCount || custodyDigest(meta.CIDs) != custodyDigest(receipt.SourceCIDs) || custodyDigest(meta.Owners) != custodyDigest(receipt.OwnerHashes) || custodyDigest(meta.Types) != custodyDigest(receipt.ContentTypes) || meta.Normal != receipt.NormalCID || meta.Branch != receipt.BranchCID || meta.Notification != receipt.NotificationCID {
		return KafkaOriginalRecord{}, ErrConflict
	}
	return row.Original, nil
}

// Metadata only. No mounted HTTP endpoint and no raw body in logs/status.
func (c *OwnedUnsupportedKafkaCustody) Inspect(ctx context.Context, source string) (UnsupportedCustodyReceipt, error) {
	if c == nil || c.store == nil || ctx == nil || !validUUID(source) {
		return UnsupportedCustodyReceipt{}, ErrInvalid
	}
	r, e := c.readReceipt(ctx, source)
	if e != nil {
		return r, e
	}
	if !time.Now().UTC().Before(r.Deadline) && r.State == "held-no-projection" {
		r.State = "expired-unresolved"
	}
	return r, nil
}

// Explicit source-bound private operator access. The known expected hash and
// current policy/deadline are mandatory; nothing invokes a modifier producer.
func (c *OwnedUnsupportedKafkaCustody) InspectOriginal(ctx context.Context, expected UnsupportedCustodyReceipt, inspect func(KafkaOriginalRecord) error) error {
	if c == nil || inspect == nil {
		return ErrInvalid
	}
	if e := c.policyCurrent(); e != nil {
		return e
	}
	r, e := c.readReceipt(ctx, expected.SourceID)
	if e != nil {
		return e
	}
	if custodyDigest(r) != custodyDigest(expected) || r.PolicySHA != c.inputSHA || r.State != "held-no-projection" {
		return ErrConflict
	}
	raw, e := c.readRaw(ctx, r)
	if e != nil {
		return e
	}
	if _, e = c.sourceRange(raw.Record, time.Now().UTC()); e != nil {
		return e
	}
	return inspect(raw)
}

// Bounded metadata listing is visible custody, not "message applied". Large
// inventories rotate this exact cursor; no fixed total roster/backlog cap.
func (c *OwnedUnsupportedKafkaCustody) List(ctx context.Context, after string, limit int64) ([]UnsupportedCustodyReceipt, error) {
	if c == nil || ctx == nil || limit < 1 || limit > 32 || after != "" && !validSHA(after) {
		return nil, ErrInvalid
	}
	filter := bson.M{"kind": unsupportedCustodyKind, "epoch": c.store.epoch}
	if after != "" {
		filter["_id"] = bson.M{"$gt": after}
	}
	cursor, e := c.store.db.Collection(Receipts).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(limit))
	if e != nil {
		return nil, e
	}
	var rows []UnsupportedCustodyReceipt
	e = cursor.All(ctx, &rows)
	closed := cursor.Close(ctx)
	if e != nil || closed != nil {
		return nil, errors.Join(e, closed)
	}
	for i, row := range rows {
		r, e := c.Inspect(ctx, row.SourceID)
		if e != nil {
			return nil, e
		}
		rows[i] = r
	}
	return rows, nil
}
