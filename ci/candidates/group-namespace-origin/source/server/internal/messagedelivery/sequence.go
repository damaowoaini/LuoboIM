// Package messagedelivery is the shared durable sequence/receipt boundary for
// ordinary messages and group-effect projections. This isolated WIP is not
// wired to MsgTransfer and does not yet implement its managed push recovery.
package messagedelivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/convert"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
	"google.golang.org/protobuf/proto"
)

const (
	Sequences = "message_delivery_sequences_v1"
	Receipts  = "message_delivery_receipts_v1"
	Effects   = "message_delivery_effects_v1"
	SchemaID  = "schema:message-delivery-v1"
)

var (
	ErrInvalid           = errors.New("invalid durable message delivery")
	ErrUnprepared        = errors.New("message delivery authority unprepared")
	ErrConflict          = errors.New("message delivery identity changed")
	ErrCommitUnconfirmed = errors.New("message delivery commit unconfirmed")
	ErrRetryLimit        = errors.New("message delivery transaction retry limit")
)

type Store struct {
	db                   *mongo.Database
	epoch, activationSHA string
	instanceID           string
	membershipRequired   bool
}

// NewStore does no DB/DDL. The activation fingerprint identifies an explicit
// operator-verified drain/seed plan, never an implicit reinterpretation of a
// legacy Redis counter. The stored schema/epoch must match inside every TX.
func NewStore(db *mongo.Database, epoch, activationSHA string) (*Store, error) {
	if db == nil || db.Client() == nil || !validUUID(epoch) || !validSHA(activationSHA) {
		return nil, ErrInvalid
	}
	primary := db.Client().Database(db.Name(), options.Database().SetReadPreference(readpref.Primary()).SetReadConcern(readconcern.Majority()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
	instance, err := uuid.NewRandom()
	if err != nil {
		return nil, ErrUnprepared
	}
	return &Store{db: primary, epoch: epoch, activationSHA: activationSHA, instanceID: instance.String()}, nil
}
func validUUID(v string) bool {
	u, e := uuid.Parse(v)
	return e == nil && u != uuid.Nil && u.String() == v
}
func validSHA(v string) bool {
	b, e := hex.DecodeString(v)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == v
}
func identity(v string) bool {
	return v != "" && len(v) <= 512 && strings.TrimSpace(v) == v && !strings.ContainsAny(v, "\x00\r\n")
}
func hash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func key(epoch, kind, id string) string {
	b, _ := json.Marshal([]string{epoch, kind, id})
	return hash(b)
}

// requireTX deliberately does not accept standalone/bare/secondary/weak outer
// sessions. The enclosing receiver owns a bounded strong transaction runner;
// this method never starts a second session or performs postcommit effects.
func (s *Store) requireTX(ctx context.Context) error {
	if s == nil || s.db == nil || ctx == nil {
		return ErrUnprepared
	}
	session := mongo.SessionFromContext(ctx)
	active, ok := session.(mongo.XSession)
	if !ok || session.Client() != s.db.Client() {
		return ErrUnprepared
	}
	x := active.ClientSession()
	if x == nil || !x.TransactionRunning() || x.CurrentRc == nil || x.CurrentRc.Level != "snapshot" || x.CurrentRp == nil || x.CurrentRp.Mode() != readpref.PrimaryMode || x.CurrentWc == nil || x.CurrentWc.Journal == nil || !*x.CurrentWc.Journal {
		return ErrUnprepared
	}
	w, ok := x.CurrentWc.W.(string)
	if !ok || w != "majority" {
		return ErrUnprepared
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return ErrUnprepared
	}
	return s.checkEpoch(ctx)
}

// Seed is produced by the explicit migration/new-CID initializer after the
// legacy writer set is retired. Both maxima must be authoritative known
// observations (including known absence=0), never owner-visible history reads.
// This API does not certify that an arbitrary supplied number is authoritative.
type Seed struct {
	MongoMax, RedisMax     int64
	MongoKnown, RedisKnown bool
	EvidenceSHA256         string
}

func (s Seed) maximum() (int64, error) {
	if !s.MongoKnown || !s.RedisKnown || s.MongoMax < 0 || s.RedisMax < 0 || s.MongoMax == math.MaxInt64 || s.RedisMax == math.MaxInt64 || !validSHA(s.EvidenceSHA256) {
		return 0, ErrUnprepared
	}
	return max(s.MongoMax, s.RedisMax), nil
}

// InitializeSequence never resets/adopts an unknown counter or changes an
// existing seed. It must run in the same strong TX policy as Accept; unknown
// ACK is inspected, not replaced by a new epoch. There is no automatic fallback
// to Redis allocation when a counter is absent or incompatible.
func (s *Store) InitializeSequence(ctx context.Context, cid string, seed Seed) error {
	if !identity(cid) {
		return ErrInvalid
	}
	maximum, err := seed.maximum()
	if err != nil {
		return err
	}
	if ctx == nil {
		return ErrUnprepared
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.requireTX(ctx); err != nil {
		return err
	}
	if err := s.requireNamespaceSeed(ctx, cid, seed.EvidenceSHA256); err != nil {
		return err
	}
	row := counter{ID: key(s.epoch, "sequence", cid), Epoch: s.epoch, CID: cid, Maximum: maximum, SeedMaximum: maximum, SeedEvidenceSHA: seed.EvidenceSHA256, Revision: 1}
	var existing counter
	err = s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": row.ID}).Decode(&existing)
	if err == nil {
		if existing.Epoch != s.epoch || existing.CID != cid || existing.SeedMaximum != maximum || existing.SeedEvidenceSHA != seed.EvidenceSHA256 || existing.Maximum < maximum || existing.Revision < 1 {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return err
	}
	_, err = s.db.Collection(Sequences).InsertOne(ctx, row)
	return err
}

type counter struct {
	ID              string `bson:"_id"`
	Epoch           string `bson:"epoch"`
	CID             string `bson:"conversation_id"`
	Maximum         int64  `bson:"maximum"`
	Revision        int64  `bson:"revision"`
	SeedMaximum     int64  `bson:"seed_maximum"`
	SeedEvidenceSHA string `bson:"seed_evidence_sha256"`
}

// Branch is frozen before transaction retries. Ordinary queue records derive
// identity from trusted topic/partition/offset; group effects derive it from
// the exact durable effect/branch. These are NOT client-provided namespaces.
type Branch struct {
	ID             string   `bson:"id"`
	ConversationID string   `bson:"conversation_id"`
	Payload        []byte   `bson:"payload"`
	Recipients     []string `bson:"recipients"`
	Persist        bool     `bson:"persist"`
}
type Envelope struct {
	ID       string
	Branches []Branch
}
type AcceptedBranch struct {
	ID             string `bson:"id"`
	ConversationID string `bson:"conversation_id"`
	Seq            int64  `bson:"seq"`
	Persist        bool   `bson:"persist"`
	Hidden         bool   `bson:"hidden"`
}
type Receipt struct {
	ID          string           `bson:"_id"`
	Epoch       string           `bson:"epoch"`
	SourceID    string           `bson:"source_id"`
	InputSHA256 string           `bson:"input_sha256"`
	Branches    []AcceptedBranch `bson:"branches"`
	AcceptedAt  time.Time        `bson:"accepted_at"`
}

func freezeEnvelope(e Envelope) (Envelope, string, error) { return freezeEnvelopeMode(e, false) }
func freezeEnvelopeMode(e Envelope, ownedGroup bool) (Envelope, string, error) {
	if !validUUID(e.ID) || len(e.Branches) == 0 || len(e.Branches) > 100 {
		return Envelope{}, "", ErrInvalid
	}
	copyE := Envelope{ID: e.ID}
	seen := map[string]bool{}
	total := 0
	for _, b := range e.Branches {
		if !validUUID(b.ID) || seen[b.ID] || !identity(b.ConversationID) || len(b.Payload) == 0 || !ownedGroup && len(b.Recipients) > 50000 {
			return Envelope{}, "", ErrInvalid
		}
		seen[b.ID] = true
		total += len(b.Payload)
		if total > 4<<20 {
			return Envelope{}, "", ErrInvalid
		}
		var msg sdkws.MsgData
		if proto.Unmarshal(b.Payload, &msg) != nil || msg.Seq != 0 || !identity(msg.SendID) || !identity(msg.RecvID) || !identity(msg.ServerMsgID) || msg.SendTime <= 0 || msg.CreateTime <= 0 {
			return Envelope{}, "", ErrInvalid
		}
		if msgprocessor.GetConversationIDByMsg(&msg) != b.ConversationID || msgprocessor.Options(msg.Options).IsHistory() != b.Persist {
			return Envelope{}, "", ErrInvalid
		}
		canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(&msg)
		if err != nil {
			return Envelope{}, "", ErrInvalid
		}
		if string(canonical) != string(b.Payload) {
			return Envelope{}, "", ErrInvalid
		}
		b.Payload = append([]byte(nil), b.Payload...)
		b.Recipients = append([]string(nil), b.Recipients...)
		sort.Strings(b.Recipients)
		for i, id := range b.Recipients {
			if !identity(id) || i > 0 && id == b.Recipients[i-1] {
				return Envelope{}, "", ErrInvalid
			}
		}
		copyE.Branches = append(copyE.Branches, b)
	}
	// Branch order is intentional stable input, not sorted after dispatch.
	body, err := json.Marshal(copyE)
	if err != nil || len(body) > 8<<20 {
		return Envelope{}, "", ErrInvalid
	}
	return copyE, hash(body), nil
}

// Accept persists the receipt, every sequence allocation, existing OpenIM
// message documents, and immutable cache/push duties in ONE strong Mongo TX.
// Replays return the original minimal receipt before touching history/cache;
// deleted/expired content is therefore never revived by a receipt retry.
func (s *Store) Accept(ctx context.Context, input Envelope) (Receipt, error) {
	return s.accept(ctx, input, false)
}
func (s *Store) accept(ctx context.Context, input Envelope, ownedGroup bool) (Receipt, error) {
	return s.acceptWithSuppression(ctx, input, ownedGroup, nil)
}
func (s *Store) acceptWithSuppression(ctx context.Context, input Envelope, ownedGroup bool, denied map[string][]string) (Receipt, error) {
	e, digest, err := freezeEnvelopeMode(input, ownedGroup)
	if err != nil {
		return Receipt{}, err
	}
	if ctx == nil {
		return Receipt{}, ErrUnprepared
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.requireTX(ctx); err != nil {
		return Receipt{}, err
	}
	receiptID := key(s.epoch, "source", e.ID)
	var old Receipt
	err = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": receiptID}).Decode(&old)
	if err == nil {
		if err := s.validateReceipt(old, e, digest); err != nil {
			return Receipt{}, err
		}
		return old, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return Receipt{}, err
	}
	if err := s.requireNamespaceInput(ctx, digest); err != nil {
		return Receipt{}, err
	}
	r := Receipt{ID: receiptID, Epoch: s.epoch, SourceID: e.ID, InputSHA256: digest, AcceptedAt: time.Now().UTC().Truncate(time.Millisecond)}
	// Stable CID order avoids lock-order inversion for a two-branch notification.
	indices := make([]int, len(e.Branches))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool {
		return e.Branches[indices[i]].ConversationID < e.Branches[indices[j]].ConversationID
	})
	accepted := make([]AcceptedBranch, len(indices))
	obligations := make([]any, 0, len(indices))
	for _, i := range indices {
		b := e.Branches[i]
		var message sdkws.MsgData
		if err := proto.Unmarshal(b.Payload, &message); err != nil {
			return Receipt{}, ErrInvalid
		}
		origin, err := messageNamespace(&message)
		if err != nil {
			return Receipt{}, err
		}
		// Silent Seq0 also creates a delivery obligation and must own its CID.
		if err := s.requireNamespace(ctx, origin); err != nil {
			return Receipt{}, err
		}
		seq := int64(0)
		hidden := false
		if ownedGroup && len(b.Recipients) > 0 {
			suppressed := map[string]bool{}
			for _, uid := range denied[b.ID] {
				suppressed[uid] = true
			}
			hidden = true
			for _, uid := range b.Recipients {
				if !suppressed[uid] {
					hidden = false
				}
			}
		}
		if b.Persist {
			var current counter
			err := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "sequence", b.ConversationID)}).Decode(&current)
			if err != nil {
				return Receipt{}, errors.Join(ErrUnprepared, err)
			}
			if current.Epoch != s.epoch || current.CID != b.ConversationID || current.Maximum < current.SeedMaximum || current.Maximum < 0 || current.Maximum >= math.MaxInt64 || current.Revision < 1 || current.Revision >= math.MaxInt64 || !validSHA(current.SeedEvidenceSHA) {
				return Receipt{}, ErrUnprepared
			}
			seq = current.Maximum + 1
			updated, err := s.db.Collection(Sequences).UpdateOne(ctx, bson.M{"_id": current.ID, "epoch": s.epoch, "conversation_id": b.ConversationID, "maximum": current.Maximum, "revision": current.Revision}, bson.M{"$set": bson.M{"maximum": seq}, "$inc": bson.M{"revision": int64(1)}})
			if err != nil {
				return Receipt{}, err
			}
			if updated.MatchedCount != 1 || updated.ModifiedCount != 1 {
				return Receipt{}, ErrConflict
			}
			message.Seq = seq
			if !hidden {
				hidden, err = s.persist(ctx, b.ConversationID, &message)
				if err != nil {
					return Receipt{}, err
				}
				if !hidden && ownedGroup && len(denied[b.ID]) > 0 {
					model := table.MsgDocModel{}
					field := fmt.Sprintf("msgs.%d.del_list", model.GetMsgIndex(message.Seq))
					changed, err := s.db.Collection(model.TableName()).UpdateOne(ctx, bson.M{"doc_id": model.GetDocID(b.ConversationID, message.Seq)}, bson.M{"$addToSet": bson.M{field: bson.M{"$each": denied[b.ID]}}})
					if err != nil {
						return Receipt{}, err
					}
					if changed.MatchedCount != 1 {
						return Receipt{}, ErrConflict
					}
				}
			}
		}
		body, err := proto.MarshalOptions{Deterministic: true}.Marshal(&message)
		if err != nil {
			return Receipt{}, ErrInvalid
		}
		accepted[i] = AcceptedBranch{ID: b.ID, ConversationID: b.ConversationID, Seq: seq, Persist: b.Persist, Hidden: hidden}
		duty := bson.M{"_id": key(s.epoch, "branch", b.ID), "epoch": s.epoch, "source_id": e.ID, "branch_id": b.ID, "conversation_id": b.ConversationID, "input_sha256": digest, "payload_sha256": hash(body), "seq": seq, "sender_id": message.SendID, "namespace_origin_kind": origin.Kind, "namespace_origin_sha256": origin.SHA256, "hidden": hidden, "persist": b.Persist, "max_pending": b.Persist, "sender_read_pending": b.Persist && !hidden, "cache_pending": false, "push_pending": len(b.Recipients) > 0 && !hidden, "online_pending": len(b.Recipients) > 0 && !hidden, "offline_pending": len(b.Recipients) > 0 && !hidden && msgprocessor.Options(message.Options).IsOfflinePush(), "recipient_count": int64(len(b.Recipients)), "state": "ready", "revision": int64(1)}
		if !hidden && (b.Persist || len(b.Recipients) > 0) {
			duty["payload"] = body
			duty["recipients"] = b.Recipients
		} else {
			if !b.Persist {
				duty["state"] = "done"
			}
		}
		obligations = append(obligations, duty)
	}
	r.Branches = accepted
	if _, err := s.db.Collection(Effects).InsertMany(ctx, obligations); err != nil {
		return Receipt{}, err
	}
	if _, err := s.db.Collection(Receipts).InsertOne(ctx, r); err != nil {
		return Receipt{}, err
	}
	return r, nil
}

func (s *Store) persist(ctx context.Context, cid string, message *sdkws.MsgData) (bool, error) {
	model := table.MsgDocModel{}
	docID, index := model.GetDocID(cid, message.Seq), model.GetMsgIndex(message.Seq)
	driver := unrelation.NewMsgMongoDriver(s.db)
	floors, err := driver.ReadMessageVisibility(ctx, []table.MessageVisibilityFloor{{ConversationID: cid}})
	if err != nil {
		return false, err
	}
	for _, floor := range floors {
		if message.Seq < floor.MinSeq {
			return true, nil
		}
	}
	if err := driver.EnsureMessageDoc(ctx, docID); err != nil {
		return false, err
	}
	collection := s.db.Collection(model.TableName())
	field := fmt.Sprintf("msgs.%d", index)
	// Preserve pre-existing user tombstones/revokes. A global tombstone consumes
	// the assigned slot but receives no late plaintext. A nonempty legacy slot
	// is a bad seed/collision and aborts the whole receipt rather than overwrite.
	// A dotted numeric path compared with null can also match a missing field
	// while traversing the array, even when this exact slot is occupied. Read
	// the assigned array element explicitly in the atomic write predicate.
	filter := bson.M{
		"doc_id":                  docID,
		field + ".global_deleted": bson.M{"$ne": true},
		"$expr": bson.M{"$let": bson.M{
			"vars": bson.M{"slot": bson.M{"$arrayElemAt": bson.A{"$msgs", index}}},
			"in":   bson.M{"$eq": bson.A{"$$slot.msg", nil}},
		}},
	}
	res, err := collection.UpdateOne(ctx, filter, bson.M{"$set": bson.M{field + ".msg": convert.MsgPb2DB(message)}})
	if err != nil {
		return false, err
	}
	if res.MatchedCount == 1 {
		return false, nil
	}
	if err := collection.FindOne(ctx, bson.M{"doc_id": docID, field + ".global_deleted": true}, options.FindOne().SetProjection(bson.M{"_id": 1})).Err(); err == nil {
		return true, nil
	} else if !errors.Is(err, mongo.ErrNoDocuments) {
		return false, err
	}
	return false, ErrConflict
}
