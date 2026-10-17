package groupauthority

import (
	"context"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

// Cache obligations are constructed from the exact, authorized typed targets.
// There is deliberately no exported raw-key enqueue method: a caller cannot
// smuggle another group or an unrelated user's joined-groups cache prefix.
func (scope *mutationScope) prepareInvalidations(ctx context.Context) error {
	for groupID, target := range scope.targets {
		if target.all {
			cursor, err := scope.store.db.Collection("group_member").Find(ctx, bson.M{"group_id": groupID}, options.Find().SetProjection(bson.M{"user_id": 1}))
			if err != nil {
				return err
			}
			for cursor.Next(ctx) {
				var row struct {
					UserID string `bson:"user_id"`
				}
				if cursor.Decode(&row) != nil || row.UserID == "" {
					cursor.Close(ctx)
					return ErrInvalidScope
				}
				target.members[row.UserID] = true
			}
			if cursor.Err() != nil {
				err := cursor.Err()
				cursor.Close(ctx)
				return err
			}
			if err := cursor.Close(ctx); err != nil {
				return err
			}
		}
		for _, key := range []string{"GROUP_INFO:" + groupID, "GROUP_MEMBER_IDS:" + groupID, "GROUP_MEMBERS_HASH2:" + groupID, "GROUP_MEMBER_NUM_CACHE:" + groupID, "GROUP_ROLE_LEVEL_MEMBER_IDS:" + groupID + "-20", "GROUP_ROLE_LEVEL_MEMBER_IDS:" + groupID + "-60", "GROUP_ROLE_LEVEL_MEMBER_IDS:" + groupID + "-100"} {
			if !cacheKey(key) {
				return ErrInvalidScope
			}
			scope.cacheKeys[key] = struct{}{}
		}
		for id := range target.members {
			for _, key := range []string{"GROUP_MEMBER_INFO:" + groupID + "-" + id, "JOIN_GROUPS_KEY:" + id} {
				if !cacheKey(key) {
					return ErrInvalidScope
				}
				scope.cacheKeys[key] = struct{}{}
			}
		}
	}
	if len(scope.cacheKeys) > 100000 {
		return ErrInvalidScope
	}
	return nil
}

func cacheKey(key string) bool {
	if len(key) > 1024 || strings.ContainsRune(key, 0) {
		return false
	}
	for _, prefix := range []string{"GROUP_INFO:", "GROUP_MEMBER_IDS:", "GROUP_MEMBERS_HASH2:", "GROUP_MEMBER_INFO:", "JOIN_GROUPS_KEY:", "GROUP_MEMBER_NUM_CACHE:", "GROUP_ROLE_LEVEL_MEMBER_IDS:"} {
		if strings.HasPrefix(key, prefix) && len(key) > len(prefix) {
			return true
		}
	}
	return false
}

type invalidation struct {
	ID        string    `bson:"_id"`
	Kind      string    `bson:"kind"`
	CreatedAt time.Time `bson:"created_at"`
	Keys      []string  `bson:"keys"`
}

func (s *Store) insertInvalidation(ctx context.Context, id string, created time.Time, keys []string) error {
	if err := strongSession(ctx, s.db.Client()); err != nil {
		return err
	}
	_, err := s.db.Collection(InvalidationsCollection).InsertOne(ctx, invalidation{ID: id, Kind: "group-cache-v1", CreatedAt: created, Keys: keys})
	return err
}

// Flush performs only idempotent cache invalidation, after confirmed commit.
// apply MUST use the cache's context-aware TagAsDeleted2/DelKey and propagate
// errors; legacy ExecDel currently swallows exhausted retries and is unsuitable.
// A failed/unknown invalidation or delete keeps a durable retry obligation.
func (commit *Commit) Flush(ctx context.Context, apply func(context.Context, string) error) error {
	if commit == nil || commit.store == nil || ctx == nil || apply == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrUnavailable
	}
	if commit.cacheTask == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, cacheBatchTimeout)
	defer cancel()
	if err := commit.store.CheckReady(ctx); err != nil {
		return ErrCacheSyncPending
	}
	var job invalidation
	err := commit.store.primaryInvalidations().FindOne(ctx, bson.M{"_id": commit.cacheTask}).Decode(&job)
	if err == mongo.ErrNoDocuments {
		return nil // A background worker already acknowledged all keys.
	}
	if err != nil || commit.store.flushJob(ctx, job, apply) != nil {
		return ErrCacheSyncPending
	}
	return nil
}

func (s *Store) primaryInvalidations() *mongo.Collection {
	return s.db.Collection(InvalidationsCollection, options.Collection().SetReadPreference(readpref.Primary()).SetReadConcern(readconcern.Majority()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
}

func (s *Store) flushJob(ctx context.Context, job invalidation, apply func(context.Context, string) error) error {
	if job.ID == "" || job.Kind != "group-cache-v1" || len(job.Keys) == 0 || len(job.Keys) > 100000 {
		return ErrCacheSyncPending
	}
	for _, key := range job.Keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !cacheKey(key) {
			return ErrCacheSyncPending
		}
		if err := apply(ctx, key); err != nil {
			return err
		}
	}
	result, err := s.primaryInvalidations().DeleteOne(ctx, bson.M{"_id": job.ID, "kind": job.Kind, "keys": job.Keys})
	if err != nil {
		return err
	}
	if result.DeletedCount == 1 {
		return nil
	}
	// Another worker may have acknowledged it. An unexpectedly changed row
	// must not be reported as successful synchronization.
	if err := s.primaryInvalidations().FindOne(ctx, bson.M{"_id": job.ID}).Err(); err == mongo.ErrNoDocuments {
		return nil
	}
	return ErrCacheSyncPending
}

// FlushPending is the bounded restart recovery hook for GroupRPC. It touches
// only this dedicated collection and the exact server-written group cache keys;
// there is no Redis SCAN/FLUSH and no user-message/notification replay. Multiple
// workers may invalidate the same job safely. Completed jobs alone are deleted.
func (s *Store) FlushPending(ctx context.Context, limit int64, apply func(context.Context, string) error) error {
	if s == nil || s.db == nil || ctx == nil || apply == nil || limit < 1 || limit > 64 || mongo.SessionFromContext(ctx) != nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, cacheBatchTimeout)
	defer cancel()
	if err := s.CheckReady(ctx); err != nil {
		return ErrCacheSyncPending
	}
	cursor, err := s.primaryInvalidations().Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetHint("_id_").SetLimit(limit))
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	failed := false
	for cursor.Next(ctx) {
		var job invalidation
		if cursor.Decode(&job) != nil || s.flushJob(ctx, job, apply) != nil {
			failed = true
		}
	}
	if cursor.Err() != nil || cursor.Close(ctx) != nil || failed {
		return ErrCacheSyncPending
	}
	return nil
}
