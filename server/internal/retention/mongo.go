package retention

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"

	ut "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

const OutboxCollection = "conversation_retention_outbox"
const StateCollection = "conversation_retention_scan_state"

type MongoStore struct {
	DB  *mongo.Database
	Now func() time.Time
	// Existing Redis floors must have been explicitly attributed/migrated.
	// This reader never copies a possibly ambiguous legacy key into Mongo.
	LegacyFloors func(context.Context, string, string) (int64, int64, error)
}

func NewMongoStore(db *mongo.Database) *MongoStore {
	return &MongoStore{DB: db, Now: time.Now}
}

func (s *MongoStore) collection(name string) *mongo.Collection {
	return s.DB.Collection(name, options.Collection().SetReadPreference(readpref.Primary()).
		SetReadConcern(readconcern.Majority()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
}

// EnsureIndexes is an explicit deployment/preparation step, not a read-side
// effect. Callers must not advertise activation before this succeeds.
func (s *MongoStore) EnsureIndexes(ctx context.Context) error {
	_, err := s.collection("conversation").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "is_msg_destruct", Value: 1}, {Key: "retention_policy_prepared", Value: 1}, {Key: "owner_user_id", Value: 1}, {Key: "conversation_id", Value: 1}},
		Options: options.Index().SetName("retention_ready_owner_cid_v1"),
	})
	if err != nil {
		return err
	}
	_, err = s.collection(OutboxCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "done", Value: 1}, {Key: "lease_until", Value: 1}, {Key: "created_at", Value: 1}}, Options: options.Index().SetName("retention_pending_lease_v1")},
		{Keys: bson.D{{Key: "completed_at", Value: 1}}, Options: options.Index().SetName("retention_completed_ttl_v1").SetExpireAfterSeconds(30 * 24 * 60 * 60)},
	})
	return err
}

func (s *MongoStore) ReadPolicy(ctx context.Context, owner, conversation string) (Policy, error) {
	if owner == "" || conversation == "" {
		return Policy{}, ErrInvalidPolicy
	}
	var p Policy
	err := s.collection("conversation").FindOne(ctx, bson.M{"owner_user_id": owner, "conversation_id": conversation}).Decode(&p)
	if err != nil {
		return Policy{}, err
	}
	if p.Owner != owner || p.Conversation != conversation {
		return Policy{}, ErrSourceChanged
	}
	return p, nil
}

func (s *MongoStore) Page(ctx context.Context, after Cursor, limit int) ([]Policy, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalidPolicy
	}
	filter := bson.M{"is_msg_destruct": true, "retention_policy_prepared": true, "msg_destruct_time": bson.M{"$type": bson.A{"int", "long"}, "$gt": 0, "$lte": MaxDurationSeconds}, "retention_policy_revision": bson.M{"$type": bson.A{"int", "long"}, "$gt": 0, "$lt": int64(math.MaxInt64)}, "owner_user_id": bson.M{"$type": "string", "$ne": ""}, "conversation_id": bson.M{"$type": "string", "$ne": ""}}
	if after.Owner != "" || after.Conversation != "" {
		filter["$or"] = []bson.M{{"owner_user_id": bson.M{"$gt": after.Owner}}, {"owner_user_id": after.Owner, "conversation_id": bson.M{"$gt": after.Conversation}}}
	}
	cur, err := s.collection("conversation").Find(ctx, filter, pageOptions(limit).SetHint("retention_ready_owner_cid_v1").SetProjection(bson.M{"owner_user_id": 1, "conversation_id": 1, "is_msg_destruct": 1, "msg_destruct_time": 1, "retention_policy_revision": 1, "retention_scan_generation": 1, "retention_policy_prepared": 1}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var rows []Policy
	if err = cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func pageOptions(limit int) *options.FindOptions {
	return options.Find().SetSort(bson.D{{Key: "owner_user_id", Value: 1}, {Key: "conversation_id", Value: 1}}).SetLimit(int64(limit))
}
func upsert() *options.UpdateOptions { return options.Update().SetUpsert(true) }

func (s *MongoStore) Cursor(ctx context.Context) (Cursor, error) {
	var c Cursor
	err := s.collection(StateCollection).FindOne(ctx, bson.M{"_id": "owner-cid-v1"}).Decode(&c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Cursor{}, nil
	}
	return c, err
}

func revisionFilter(name string, expected int64) bson.M {
	if expected == 0 {
		return bson.M{"$or": []bson.M{{name: 0}, {name: bson.M{"$exists": false}}}}
	}
	return bson.M{name: expected}
}

func (s *MongoStore) SaveCursor(ctx context.Context, before, next Cursor) error {
	if before.Version < 0 || before.Version == int64(^uint64(0)>>1) {
		return ErrInvalidPolicy
	}
	filter := revisionFilter("version", before.Version)
	filter["_id"] = "owner-cid-v1"
	result, err := s.collection(StateCollection).UpdateOne(ctx, filter, bson.M{"$set": bson.M{"owner": next.Owner, "conversation": next.Conversation, "version": before.Version + 1}}, options.Update().SetUpsert(before.Version == 0))
	if mongo.IsDuplicateKeyError(err) {
		return ErrPolicyChanged
	}
	if err != nil {
		return err
	}
	if result.MatchedCount+result.UpsertedCount != 1 {
		return ErrPolicyChanged
	}
	return nil
}

func tupleID(values ...any) string {
	b, _ := json.Marshal(values)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func floorID(conversation, user string) string { return tupleID(conversation, user) }

func (s *MongoStore) Floor(ctx context.Context, p Policy) (int64, error) {
	if p.Owner == "" || p.Conversation == "" {
		return 0, ErrInvalidPolicy
	}
	ownerID, globalID := floorID(p.Conversation, p.Owner), floorID(p.Conversation, "")
	cur, err := s.collection("message_visibility_floors").Find(ctx, bson.M{"_id": bson.M{"$in": []string{ownerID, globalID}}})
	if err != nil {
		return 0, err
	}
	defer cur.Close(ctx)
	var floors []ut.MessageVisibilityFloor
	if err = cur.All(ctx, &floors); err != nil {
		return 0, err
	}
	floor, ownFloor, globalFloor := int64(1), int64(1), int64(1)
	for _, f := range floors {
		if f.ConversationID != p.Conversation || f.MinSeq < 0 || (f.UserID != p.Owner && f.UserID != "") || f.ID != floorID(f.ConversationID, f.UserID) {
			return 0, ErrSourceChanged
		}
		if f.MinSeq > floor {
			floor = f.MinSeq
		}
		if f.UserID == p.Owner && f.MinSeq > ownFloor {
			ownFloor = f.MinSeq
		}
		if f.UserID == "" && f.MinSeq > globalFloor {
			globalFloor = f.MinSeq
		}
	}
	if s.LegacyFloors == nil {
		return 0, ErrUnavailable
	}
	legacyOwn, legacyGlobal, err := s.LegacyFloors(ctx, p.Owner, p.Conversation)
	if err != nil {
		return 0, err
	}
	if legacyOwn < 0 || legacyGlobal < 0 || legacyOwn > ownFloor || legacyGlobal > globalFloor {
		return 0, ErrUnavailable
	}
	return floor, nil
}

type slotMessage struct {
	Seq      int64 `bson:"seq"`
	SendTime int64 `bson:"send_time"`
}
type slotRecord struct {
	Message *slotMessage `bson:"msg"`
	Owners  []string     `bson:"del_list"`
	Global  bool         `bson:"global_deleted"`
}
type slotDocument struct {
	ID    string        `bson:"doc_id"`
	Slots []*slotRecord `bson:"msgs"`
}

func (s *MongoStore) Slots(ctx context.Context, p Policy, first int64, limit int) ([]Slot, error) {
	if first < 1 || limit < 1 || limit > MaximumChunk || first > math.MaxInt64-int64(limit) {
		return nil, ErrInvalidPolicy
	}
	var model ut.MsgDocModel
	loaded := map[string]*slotDocument{}
	result := make([]Slot, 0, limit)
	for i := 0; i < limit; i++ {
		seq := first + int64(i)
		docID := model.GetDocID(p.Conversation, seq)
		doc, ok := loaded[docID]
		if !ok {
			doc = &slotDocument{}
			err := s.collection("msg").FindOne(ctx, bson.M{"doc_id": docID}, options.FindOne().SetProjection(bson.M{"doc_id": 1, "msgs.msg.seq": 1, "msgs.msg.send_time": 1, "msgs.del_list": 1, "msgs.global_deleted": 1})).Decode(doc)
			if errors.Is(err, mongo.ErrNoDocuments) {
				doc = nil
			} else if err != nil {
				return nil, err
			} else if doc.ID != docID {
				return nil, ErrSourceChanged
			}
			loaded[docID] = doc
		}
		slot := Slot{Seq: seq, Document: docID}
		index := model.GetMsgIndex(seq)
		if doc != nil && index < int64(len(doc.Slots)) && doc.Slots[index] != nil {
			r := doc.Slots[index]
			slot.Hidden = r.Global
			for _, owner := range r.Owners {
				if owner == p.Owner {
					slot.Hidden = true
				}
			}
			if r.Message != nil {
				if r.Message.Seq != seq {
					return nil, ErrSourceChanged
				}
				slot.SendTime = r.Message.SendTime
				slot.Known = true
			} else {
				slot.Known = slot.Hidden
			}
		}
		result = append(result, slot)
		if !slot.Known {
			break
		}
	}
	return result, nil
}
