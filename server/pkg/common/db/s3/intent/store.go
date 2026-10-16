// Package intent holds owner-bound upload capabilities in the tenant database.
// Client IDs are opaque references, never trusted object locations.
package intent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

const Prefix = "luobo-upload-v1:"
const MaxLifetime = 7 * 24 * time.Hour

type record struct {
	ID        string    `bson:"_id"`
	Owner     string    `bson:"owner"`
	Engine    string    `bson:"engine"`
	Purpose   string    `bson:"purpose"`
	Payload   []byte    `bson:"payload"`
	CreatedAt time.Time `bson:"created_at"`
	ExpiresAt time.Time `bson:"expires_at"`
}
type Store struct {
	collection *mongo.Collection
	proofs     *mongo.Collection
	namespace  string
}

func New(ctx context.Context, db *mongo.Database) (*Store, error) {
	c := db.Collection("object_upload_intents", options.Collection().SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))).SetReadConcern(readconcern.Majority()).SetReadPreference(readpref.Primary()))
	if _, err := c.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "expires_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)}); err != nil {
		return nil, err
	}
	proofs, namespace, err := newCompletionStore(ctx, db)
	if err != nil {
		return nil, err
	}
	return &Store{collection: c, proofs: proofs, namespace: namespace}, nil
}
func actor(ctx context.Context) (string, error) {
	v := mcontext.GetOpUserID(ctx)
	if v == "" {
		return "", errs.ErrNoPermission.Wrap("upload actor required")
	}
	return v, nil
}
func (s *Store) put(ctx context.Context, id, purpose, engine string, payload any, duration time.Duration) (string, error) {
	owner, err := actor(ctx)
	if err != nil {
		return "", err
	}
	if s == nil || s.collection == nil || purpose == "" || engine == "" || duration <= 0 || duration > MaxLifetime {
		return "", errs.ErrArgs.Wrap("invalid upload intent")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	if len(data) > 64*1024 {
		return "", errs.ErrArgs.Wrap("upload intent too large")
	}
	now := time.Now().UTC()
	value := record{ID: id, Owner: owner, Engine: engine, Purpose: purpose, Payload: data, CreatedAt: now, ExpiresAt: now.Add(duration)}
	_, err = s.collection.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$setOnInsert": value}, options.Update().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) {
		err = nil
	}
	return id, err
}
func (s *Store) Create(ctx context.Context, purpose, engine string, payload any, duration time.Duration) (string, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	return s.put(ctx, Prefix+id.String(), purpose, engine, payload, duration)
}
func (s *Store) Load(ctx context.Context, id, purpose, engine string, out any) (time.Time, error) {
	owner, err := actor(ctx)
	if err != nil {
		return time.Time{}, err
	}
	if s == nil || s.collection == nil || len(id) > 256 || !strings.HasPrefix(id, Prefix) {
		return time.Time{}, errs.ErrNoPermission.Wrap("upload intent is not authenticated")
	}
	var value record
	if err = s.collection.FindOne(ctx, bson.M{"_id": id, "owner": owner, "purpose": purpose, "engine": engine, "expires_at": bson.M{"$gt": time.Now().UTC()}}).Decode(&value); err != nil {
		if err == mongo.ErrNoDocuments {
			return time.Time{}, errs.ErrNoPermission.Wrap("upload intent is missing, expired or belongs to another account")
		}
		return time.Time{}, err
	}
	if json.Unmarshal(value.Payload, out) != nil {
		return time.Time{}, fmt.Errorf("stored upload intent is invalid")
	}
	return value.ExpiresAt, nil
}
func legacyID(ctx context.Context, engine, source string) (string, error) {
	owner, err := actor(ctx)
	if err != nil {
		return "", err
	}
	if source == "" || len(source) > 8192 {
		return "", errs.ErrArgs.Wrap("invalid legacy upload intent")
	}
	data, _ := json.Marshal([]string{owner, engine, source})
	sum := sha256.Sum256(data)
	return Prefix + "legacy:" + hex.EncodeToString(sum[:]), nil
}

// RecoverLegacy stores only a fresh server-generated target. The source is a
// deduplication label and never grants access to any key embedded by the client.
func (s *Store) RecoverLegacy(ctx context.Context, source, engine string, payload any, duration time.Duration, out any) (time.Time, error) {
	id, err := legacyID(ctx, engine, source)
	if err != nil {
		return time.Time{}, err
	}
	if _, err = s.put(ctx, id, "multipart", engine, payload, duration); err != nil {
		return time.Time{}, err
	}
	return s.Load(ctx, id, "multipart", engine, out)
}
func (s *Store) LoadLegacy(ctx context.Context, source, engine string, out any) (time.Time, error) {
	id, err := legacyID(ctx, engine, source)
	if err != nil {
		return time.Time{}, err
	}
	return s.Load(ctx, id, "multipart", engine, out)
}
