package messagedelivery

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (s *Store) actionRecovery(ctx context.Context, branch, policy string) (context.Context, deliveryDuty, error) {
	d, err := s.readDutyRecord(ctx, branch)
	if err != nil {
		return ctx, deliveryDuty{}, err
	}
	if d.ProducerVersion != ownedGroupProducer || d.PolicySHA != policy || d.ActionProfile == nil || d.ActionCut == nil {
		return ctx, deliveryDuty{}, ErrUnprepared
	}
	return context.WithValue(ctx, kafkaRecoveryKey{}, kafkaRecoveryScope{store: s, branch: branch, policy: policy, producer: ownedGroupProducer}), d, nil
}
func (s *Store) RecoverOwnedActionOnline(ctx context.Context, branch string, reader VisibilityReader, projection Projection, transport KafkaOnlineTransport) error {
	if s == nil || ctx == nil || reader == nil || projection == nil || transport == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.CheckKafkaReady(current); err != nil {
		return err
	}
	current, d, err := s.actionRecovery(current, branch, transport.PolicyFingerprint())
	if err != nil {
		return err
	}
	if !d.Persist {
		return s.recoverOwnedSilentOnline(current, branch, transport)
	}
	if err = s.Project(current, branch, projection); err != nil {
		return err
	}
	return s.RecoverOnline(current, branch, reader, transport)
}

// Actions use the SAME real vendor implementations and durable per-device /
// alias receipts. Source policy and private context stay explicitly action-bound.
func (s *Store) RecoverOwnedActionOffline(ctx context.Context, branch string, reader VisibilityReader, transport KafkaOfflineTransport) error {
	if s == nil || ctx == nil || reader == nil || transport == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.CheckKafkaReady(current); err != nil {
		return err
	}
	current, _, err := s.actionRecovery(current, branch, transport.PolicyFingerprint())
	if err != nil {
		return err
	}
	return s.recoverOffline(current, branch, reader, transport, true)
}
func (s *Store) ListOwnedActionPending(ctx context.Context, after string, limit int64) ([]KafkaOnlinePending, error) {
	if s == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil || limit < 1 || limit > 32 || after != "" && !validSHA(after) {
		return nil, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.CheckKafkaReady(current); err != nil {
		return nil, err
	}
	filter := bson.M{"epoch": s.epoch, "producer_version": ownedGroupProducer, "state": "ready", "$or": bson.A{bson.M{"online_pending": true}, bson.M{"offline_pending": true}, bson.M{"max_pending": true}, bson.M{"sender_read_pending": true}}}
	if after != "" {
		filter["_id"] = bson.M{"$gt": after}
	}
	cursor, err := s.db.Collection(Effects).Find(current, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(limit).SetProjection(bson.M{"_id": 1, "branch_id": 1, "policy_sha256": 1}))
	if err != nil {
		return nil, err
	}
	var out []KafkaOnlinePending
	for cursor.Next(current) {
		var r struct {
			ID     string `bson:"_id"`
			Branch string `bson:"branch_id"`
			Policy string `bson:"policy_sha256"`
		}
		if cursor.Decode(&r) != nil || !validUUID(r.Branch) || !validSHA(r.Policy) || r.ID != key(s.epoch, "branch", r.Branch) {
			err = ErrConflict
			break
		}
		out = append(out, KafkaOnlinePending{BranchID: r.Branch, PolicySHA256: r.Policy, Cursor: r.ID})
	}
	if err == nil {
		err = cursor.Err()
	}
	closeErr := cursor.Close(current)
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	return out, nil
}
