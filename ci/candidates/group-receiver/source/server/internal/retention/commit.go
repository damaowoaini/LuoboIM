package retention

import (
	"context"
	"errors"
	"math"
	"time"

	ut "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"go.mongodb.org/mongo-driver/bson"
)

type Event struct {
	ID           string    `bson:"_id"`
	Owner        string    `bson:"owner"`
	Conversation string    `bson:"conversation"`
	Floor        int64     `bson:"floor"`
	Sequences    []int64   `bson:"sequences"`
	Created      time.Time `bson:"created_at"`
	Done         bool      `bson:"done"`
	Lease        string    `bson:"lease"`
	LeaseUntil   time.Time `bson:"lease_until"`
}

func (s *MongoStore) Commit(ctx context.Context, plan Plan) error {
	if !plan.Policy.Eligible() || s.Now().UnixMilli() < plan.At.UnixMilli() {
		return ErrPolicyChanged
	}
	return transaction(ctx, s.DB.Client(), false, func(current context.Context) error {
		p, err := s.ReadPolicy(current, plan.Policy.Owner, plan.Policy.Conversation)
		if err != nil {
			return err
		}
		if !p.Eligible() || p.Revision != plan.Policy.Revision || p.Generation != plan.Policy.Generation || p.Duration != plan.Policy.Duration {
			return ErrPolicyChanged
		}
		floor, err := s.Floor(current, p)
		if err != nil {
			return err
		}
		fresh, err := BuildPlan(current, s, p, floor, plan.At, plan.Limit)
		if err != nil {
			return err
		}
		if !samePlan(plan, fresh) {
			return ErrSourceChanged
		}
		filter := revisionFilter("retention_scan_generation", p.Generation)
		filter["owner_user_id"] = p.Owner
		filter["conversation_id"] = p.Conversation
		filter["retention_policy_revision"] = p.Revision
		filter["is_msg_destruct"] = true
		filter["msg_destruct_time"] = p.Duration
		filter["retention_policy_prepared"] = true
		if p.Generation == math.MaxInt64 {
			return ErrInvalidPolicy
		}
		// This must be a real write to the SAME policy document. A snapshot
		// read followed only by a floor write would permit write-skew.
		result, err := s.collection("conversation").UpdateOne(current, filter, bson.M{"$set": bson.M{"retention_scan_generation": p.Generation + 1, "latest_msg_destruct_time": plan.At.UTC()}})
		if err != nil {
			return err
		}
		if result.MatchedCount != 1 {
			return ErrPolicyChanged
		}
		if fresh.ToFloor == fresh.FromFloor {
			return nil
		}
		commitID := tupleID(p.Owner, p.Conversation, p.Revision, p.Generation, fresh.ToFloor)
		// Message reads also participate in the commit conflict set. A late
		// persistence/update of a scanned document cannot race the floor.
		for _, doc := range fresh.Documents {
			result, err = s.collection("msg").UpdateOne(current, bson.M{"doc_id": doc}, bson.M{"$set": bson.M{"retention_scan_fence": commitID}})
			if err != nil {
				return err
			}
			if result.MatchedCount != 1 {
				return ErrSourceChanged
			}
		}
		id := floorID(p.Conversation, p.Owner)
		_, err = s.collection("message_visibility_floors").UpdateOne(current, bson.M{"_id": id, "conversation_id": p.Conversation, "user_id": p.Owner},
			bson.M{"$setOnInsert": bson.M{"conversation_id": p.Conversation, "user_id": p.Owner}, "$max": bson.M{"min_seq": fresh.ToFloor, "updated_at": plan.At.UTC()}}, upsert())
		if err != nil {
			return err
		}
		if s.PendingDelivery != nil {
			if err := s.PendingDelivery.MutateMessageVisibility(current, s.DB, []ut.PendingVisibilityChange{{ConversationID: p.Conversation, Owners: []string{p.Owner}, Below: fresh.ToFloor}}, func(context.Context) error { return nil }); err != nil {
				return err
			}
		}
		_, err = s.collection(OutboxCollection).InsertOne(current, Event{ID: commitID, Owner: p.Owner, Conversation: p.Conversation, Floor: fresh.ToFloor, Sequences: fresh.Sequences, Created: plan.At.UTC(), LeaseUntil: time.Unix(0, 0).UTC()})
		return err
	})
}

// PrepareLegacyPage is metadata-only and bounded. It does not enable a policy,
// change a duration, advance a floor or emit a deletion. Operator orchestration
// must first stop the old cron and drain ALL old preference writers.
func (s *MongoStore) PrepareLegacyPage(ctx context.Context, after Cursor, limit int) (Cursor, int64, error) {
	if limit < 1 || limit > 100 {
		return after, 0, ErrInvalidPolicy
	}
	filter := revisionFilter("retention_policy_revision", 0)
	if after.Owner != "" || after.Conversation != "" {
		filter["$and"] = []bson.M{{"$or": []bson.M{{"owner_user_id": bson.M{"$gt": after.Owner}}, {"owner_user_id": after.Owner, "conversation_id": bson.M{"$gt": after.Conversation}}}}}
	}
	cur, err := s.collection("conversation").Find(ctx, filter, pageOptions(limit))
	if err != nil {
		return after, 0, err
	}
	defer cur.Close(ctx)
	var rows []Policy
	if err = cur.All(ctx, &rows); err != nil {
		return after, 0, err
	}
	next := after
	var changed int64
	for _, p := range rows {
		next.Owner, next.Conversation = p.Owner, p.Conversation
		if p.Owner == "" || p.Conversation == "" || p.Generation < 0 || p.Generation >= math.MaxInt64 || ValidateDuration(p.Enabled, p.Duration) != nil {
			continue
		}
		if _, err := s.Floor(ctx, p); err != nil {
			return after, changed, err
		}
		match := revisionFilter("retention_policy_revision", 0)
		match["owner_user_id"] = p.Owner
		match["conversation_id"] = p.Conversation
		// An absent generation is interpreted as0. Existing generation and
		// last-scan metadata are retained rather than overwritten by preparation.
		result, err := s.collection("conversation").UpdateOne(ctx, match, bson.M{"$set": bson.M{"retention_policy_revision": int64(1), "retention_policy_prepared": true}})
		if err != nil {
			return after, changed, err
		}
		changed += result.ModifiedCount
	}
	return next, changed, nil
}

func isStale(err error) bool {
	return errors.Is(err, ErrPolicyChanged) || errors.Is(err, ErrSourceChanged)
}
