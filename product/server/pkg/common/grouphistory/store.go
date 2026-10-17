// Package grouphistory records membership from authoritative server writes.
// Arbitrary conversation rows are deliberately never treated as membership proof.
package grouphistory

import (
	"context"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"time"
)

type Store struct {
	collection *mongo.Collection
	current    *mongo.Collection
}
type Membership struct {
	GroupID       string     `bson:"group_id"`
	UserID        string     `bson:"user_id"`
	FirstJoinedAt time.Time  `bson:"first_joined_at"`
	LastLeftAt    *time.Time `bson:"last_left_at"`
	Evidence      string     `bson:"evidence"`
}

func New(ctx context.Context, db *mongo.Database) (*Store, error) {
	collection := db.Collection("group_membership_history")
	_, err := collection.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "group_id", Value: 1}, {Key: "user_id", Value: 1}}, Options: options.Index().SetUnique(true).SetName("membership_group_user")})
	if err != nil {
		return nil, err
	}
	return &Store{collection: collection, current: db.Collection("group_member")}, nil
}
func (s *Store) Joined(ctx context.Context, group, user string, at time.Time, evidence string) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := s.collection.UpdateOne(ctx, bson.M{"group_id": group, "user_id": user}, bson.M{"$set": bson.M{"last_left_at": nil, "evidence": evidence}, "$setOnInsert": bson.M{"group_id": group, "user_id": user, "first_joined_at": at}}, options.Update().SetUpsert(true))
	return err
}
func (s *Store) Left(ctx context.Context, group, user string, joined, left time.Time) error {
	_, err := s.collection.UpdateOne(ctx, bson.M{"group_id": group, "user_id": user}, bson.M{"$set": bson.M{"last_left_at": left, "evidence": "authoritative_membership_delete"}, "$setOnInsert": bson.M{"group_id": group, "user_id": user, "first_joined_at": joined}}, options.Update().SetUpsert(true))
	return err
}
func (s *Store) LastLeft(ctx context.Context, group, user string) (*time.Time, error) {
	var record Membership
	err := s.collection.FindOne(ctx, bson.M{"group_id": group, "user_id": user}).Decode(&record)
	if err != nil {
		return nil, err
	}
	return record.LastLeftAt, nil
}

func (s *Store) Current(ctx context.Context, group, user string) (bool, error) {
	err := s.current.FindOne(ctx, bson.M{"group_id": group, "user_id": user}, options.FindOne().SetProjection(bson.M{"_id": 1})).Err()
	if err == mongo.ErrNoDocuments {
		return false, nil
	}
	return err == nil, err
}

// Observation is insertion-only, so an in-flight read cannot clear a concurrent
// authoritative leave. Rejoining is recorded by the membership write itself.
func (s *Store) ObserveCurrent(ctx context.Context, group, user string) error {
	_, err := s.collection.UpdateOne(ctx, bson.M{"group_id": group, "user_id": user}, bson.M{"$setOnInsert": bson.M{"group_id": group, "user_id": user, "first_joined_at": time.Now().UTC(), "last_left_at": nil, "evidence": "verified_current_member"}}, options.Update().SetUpsert(true))
	return err
}
