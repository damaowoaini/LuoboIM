package messagedelivery

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// This is a code-owned, branch-specific recovery context, not a network actor
// capability or a member/request writer scope. No exported constructor accepts
// caller metadata as proof. Legacy worker methods remain closed to new policy
// duties unless invoked by the exact ordinary recovery entry below.
type kafkaRecoveryKey struct{}
type kafkaRecoveryScope struct {
	store          *Store
	branch, policy string
	producer       string
}

func (s *Store) kafkaRecoveryAuthorized(ctx context.Context, d deliveryDuty) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	if _, ok := ctx.Deadline(); !ok {
		return false
	}
	r, ok := ctx.Value(kafkaRecoveryKey{}).(kafkaRecoveryScope)
	return ok && r.store == s && r.branch == d.BranchID && r.policy == d.PolicySHA && (d.ProducerVersion == "ordinary-kafka-v1" && r.producer == "" || d.ProducerVersion == ownedGroupProducer && r.producer == ownedGroupProducer)
}

// Only an owned adapter constructed for this frozen configuration may process
// the duty. Its implementation must enforce durable configured callback gates
// before network dispatch; a matching digest is a binding, not callback ACK.
type KafkaOnlineTransport interface {
	OnlineTransport
	PolicyFingerprint() string
}

func (s *Store) CheckKafkaReady(ctx context.Context) error {
	if s == nil || !s.membershipRequired {
		return ErrUnprepared
	}
	return s.CheckReady(ctx)
}

// RecoverKafkaOnline uses the same positive-seq projection, owner visibility
// pages, final claim checks and gateway ACK storage as the accepted receiver.
// It does not reallocate/rebuild a message and never converts n_ Seq0 to history.
func (s *Store) RecoverKafkaOnline(ctx context.Context, branchID string, reader VisibilityReader, projection Projection, transport KafkaOnlineTransport) error {
	if s == nil || !s.membershipRequired || ctx == nil || reader == nil || projection == nil || transport == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.CheckReady(current); err != nil {
		return err
	}
	d, err := s.readDutyRecord(current, branchID)
	if err != nil {
		return err
	}
	if d.ProducerVersion != "ordinary-kafka-v1" || !validSHA(d.PolicySHA) || transport.PolicyFingerprint() != d.PolicySHA {
		return ErrUnprepared
	}
	if !d.Persist || d.Seq < 1 {
		return ErrUnprepared
	} // Separate non-replay Seq0 obligation is still required.
	current = context.WithValue(current, kafkaRecoveryKey{}, kafkaRecoveryScope{store: s, branch: branchID, policy: d.PolicySHA})
	if err := s.Project(current, branchID, projection); err != nil {
		return err
	}
	return s.RecoverOnline(current, branchID, reader, transport)
}

type KafkaOnlinePending struct{ BranchID, PolicySHA256, Cursor string }

// A stable rotating cursor prevents one busy/unknown callback at the front of
// the collection from starving other already committed messages. The cursor
// is an internal iteration position, not a success receipt or source retry ID.
func (s *Store) ListKafkaOnlinePending(ctx context.Context, after string, limit int64) ([]KafkaOnlinePending, error) {
	if s == nil || !s.membershipRequired || ctx == nil || mongo.SessionFromContext(ctx) != nil || limit < 1 || limit > 32 || after != "" && !validSHA(after) {
		return nil, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.CheckReady(current); err != nil {
		return nil, err
	}
	filter := bson.M{"epoch": s.epoch, "producer_version": "ordinary-kafka-v1", "online_pending": true, "persist": true, "state": "ready"}
	if after != "" {
		filter["_id"] = bson.M{"$gt": after}
	}
	cursor, err := s.db.Collection(Effects).Find(current, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(limit).SetProjection(bson.M{"_id": 1, "branch_id": 1, "policy_sha256": 1}))
	if err != nil {
		return nil, err
	}
	var out []KafkaOnlinePending
	for cursor.Next(current) {
		var row struct {
			ID     string `bson:"_id"`
			Branch string `bson:"branch_id"`
			Policy string `bson:"policy_sha256"`
		}
		if cursor.Decode(&row) != nil || !validSHA(row.ID) || !validUUID(row.Branch) || !validSHA(row.Policy) || row.ID != key(s.epoch, "branch", row.Branch) {
			err = ErrConflict
			break
		}
		out = append(out, KafkaOnlinePending{BranchID: row.Branch, PolicySHA256: row.Policy, Cursor: row.ID})
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
