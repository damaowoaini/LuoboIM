package sessionlogout

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/log"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

type Job struct {
	ID          string     `bson:"_id"`
	UserID      string     `bson:"user_id"`
	Platform    int        `bson:"platform"`
	TokenHash   string     `bson:"token_hash"`
	CreatedAt   time.Time  `bson:"created_at"`
	CompletedAt *time.Time `bson:"completed_at"`
	ExpireAt    *time.Time `bson:"expire_at"`
}
type Service struct {
	jobs   *mongo.Collection
	tokens cache.MsgModel
	kick   func(context.Context, string, int, string) error
}

func New(ctx context.Context, db *mongo.Database, rdb redis.UniversalClient, kick func(context.Context, string, int, string) error) (*Service, error) {
	collection := db.Collection("session_logout_jobs", options.Collection().SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
	_, err := collection.Indexes().CreateMany(ctx, []mongo.IndexModel{{Keys: bson.D{{Key: "completed_at", Value: 1}, {Key: "created_at", Value: 1}}}, {Keys: bson.D{{Key: "expire_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)}})
	if err != nil {
		return nil, err
	}
	return &Service{jobs: collection, tokens: cache.NewMsgCacheModel(rdb), kick: kick}, nil
}

func Digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func jobID(user string, platform int, hash string) string {
	body, _ := json.Marshal([]any{user, platform, hash})
	return Digest(string(body))
}

// No raw JWT enters this durable outbox. Reconciliation resolves a token hash
// against the existing Redis status hash, deletes only that token, and then
// closes only gateway connections using the same digest. Retries are harmless
// even if a later login uses the same account and platform.
func (s *Service) Begin(ctx context.Context, user string, platform int, token string) (*Job, error) {
	if user == "" || token == "" || constant.PlatformIDToName(platform) == "" {
		return nil, fmt.Errorf("invalid logout session")
	}
	return s.BeginDigest(ctx, user, platform, Digest(token))
}

// BeginDigest is reserved for the authenticated chart service administrator.
// It enables durable compensation without storing a raw IM credential in SQL.
func (s *Service) BeginDigest(ctx context.Context, user string, platform int, hash string) (*Job, error) {
	decoded, err := hex.DecodeString(hash)
	if user == "" || constant.PlatformIDToName(platform) == "" || err != nil || len(decoded) != 32 {
		return nil, fmt.Errorf("invalid logout digest")
	}
	hash = hex.EncodeToString(decoded)
	job := Job{ID: jobID(user, platform, hash), UserID: user, Platform: platform, TokenHash: hash, CreatedAt: time.Now().UTC()}
	_, err = s.jobs.UpdateOne(ctx, bson.M{"_id": job.ID}, bson.M{"$setOnInsert": job}, options.Update().SetUpsert(true))
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return nil, err
	}
	var stored Job
	if err := s.jobs.FindOne(ctx, bson.M{"_id": job.ID, "user_id": user, "platform": platform, "token_hash": hash}).Decode(&stored); err != nil {
		return nil, err
	}
	return &stored, nil
}
func (s *Service) Process(ctx context.Context, job *Job) error {
	if job.CompletedAt != nil {
		return nil
	}
	tokens, err := s.tokens.GetTokensWithoutError(ctx, job.UserID, job.Platform)
	if err != nil {
		return err
	}
	for token := range tokens {
		if Digest(token) == job.TokenHash {
			if err := s.tokens.DeleteTokenByUidPid(ctx, job.UserID, job.Platform, []string{token}); err != nil {
				return err
			}
		}
	}
	if s.kick == nil {
		return fmt.Errorf("exact-session gateway close unavailable")
	}
	if err := s.kick(ctx, job.UserID, job.Platform, job.TokenHash); err != nil {
		return err
	}
	now := time.Now().UTC()
	expires := now.Add(7 * 24 * time.Hour)
	_, err = s.jobs.UpdateOne(ctx, bson.M{"_id": job.ID, "completed_at": nil}, bson.M{"$set": bson.M{"completed_at": now, "expire_at": expires}})
	return err
}
func (s *Service) Reconcile(ctx context.Context) error {
	cur, err := s.jobs.Find(ctx, bson.M{"completed_at": nil}, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetLimit(100))
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	var first error
	for cur.Next(ctx) {
		var job Job
		if err := cur.Decode(&job); err != nil {
			return err
		}
		if err := s.Process(ctx, &job); err != nil && first == nil {
			first = err
		}
	}
	if err := cur.Err(); err != nil {
		return err
	}
	return first
}
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, 20*time.Second)
		if err := s.Reconcile(attempt); err != nil {
			log.ZWarn(ctx, "Exact-session logout reconciliation pending", nil)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
