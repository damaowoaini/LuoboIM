package retention

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Effects interface {
	SyncFloor(context.Context, string, string, int64) error
	Notify(context.Context, string, string, []int64) error
}

func (s *MongoStore) Claim(ctx context.Context) (*Event, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	now := s.Now().UTC()
	var event Event
	err := s.collection(OutboxCollection).FindOneAndUpdate(ctx, bson.M{"done": false, "lease_until": bson.M{"$lte": now}},
		bson.M{"$set": bson.M{"lease": hex.EncodeToString(nonce[:]), "lease_until": now.Add(30 * time.Second)}},
		options.FindOneAndUpdate().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}).SetReturnDocument(options.After)).Decode(&event)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if event.Owner == "" || event.Conversation == "" || event.Floor < 1 || len(event.Sequences) > MaximumChunk {
		return nil, ErrSourceChanged
	}
	return &event, nil
}

func (s *MongoStore) Complete(ctx context.Context, event Event) error {
	result, err := s.collection(OutboxCollection).UpdateOne(ctx, bson.M{"_id": event.ID, "lease": event.Lease, "done": false}, bson.M{"$set": bson.M{"done": true, "completed_at": s.Now().UTC()}})
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return ErrPolicyChanged
	}
	return nil
}

// Failure never erases the pending event. The lease expires for bounded retry;
// duplicate notifications are legal idempotent deletions, not policy changes.
func (s *MongoStore) Drain(ctx context.Context, effects Effects, limit int) (int, error) {
	if effects == nil || limit < 1 || limit > 100 {
		return 0, ErrUnavailable
	}
	completed := 0
	for i := 0; i < limit; i++ {
		event, err := s.Claim(ctx)
		if err != nil {
			return completed, err
		}
		if event == nil {
			return completed, nil
		}
		if err = effects.SyncFloor(ctx, event.Owner, event.Conversation, event.Floor); err != nil {
			return completed, err
		}
		if len(event.Sequences) > 0 {
			if err = effects.Notify(ctx, event.Owner, event.Conversation, event.Sequences); err != nil {
				return completed, err
			}
		}
		if err = s.Complete(ctx, *event); err != nil {
			return completed, err
		}
		completed++
	}
	return completed, nil
}
