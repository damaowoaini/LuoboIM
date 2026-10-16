// Package visibilitymigration preserves pre-upgrade Redis visibility floors.
// It is an explicit maintenance tool, never an automatic trust/backfill path.
package visibilitymigration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	ut "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

type StoredFloor struct {
	ut.MessageVisibilityFloor `bson:",inline"`
	MigrationID               string `bson:"migration_id,omitempty" json:"migrationID,omitempty"`
}
type Entry struct {
	Key      string                    `json:"redisKey"`
	Value    string                    `json:"redisValue"`
	Floor    ut.MessageVisibilityFloor `json:"floor"`
	Original *StoredFloor              `json:"original"`
}
type Unresolved struct{ Key, Value, Reason string }
type Backup struct {
	Version    int          `json:"version"`
	ID         string       `json:"id"`
	Tenant     string       `json:"tenant"`
	Database   string       `json:"database"`
	Instance   string       `json:"instance"`
	AppliedAt  time.Time    `json:"appliedAt"`
	Entries    []Entry      `json:"entries"`
	Unresolved []Unresolved `json:"unresolved"`
	Selection  *Selection   `json:"selection,omitempty"`
}

type Selection struct {
	SourcePath        string `json:"sourcePath"`
	SourceSHA256      string `json:"sourceSHA256"`
	SkippedKnown      int    `json:"skippedKnown"`
	SkippedUnresolved int    `json:"skippedUnresolved"`
}

// Select explicitly reviewed known tuple IDs without dropping the full backup
// or guessing unresolved owners. The derived manifest remains bound to the
// unchanged private source file and preserves its original CAS preimages.
func Select(sourcePath string, ids []string) (*Backup, error) {
	sourcePath, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, err
	}
	backup, err := Load(sourcePath)
	if err != nil {
		return nil, err
	}
	if backup.Selection != nil {
		return nil, fmt.Errorf("selection requires the original full backup")
	}
	selected := map[string]bool{}
	for _, id := range ids {
		if id == "" || selected[id] {
			return nil, fmt.Errorf("invalid/duplicate reviewed tuple ID")
		}
		selected[id] = true
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no reviewed tuples selected")
	}
	entries := []Entry{}
	for _, entry := range backup.Entries {
		if selected[entry.Floor.ID] {
			entries = append(entries, entry)
			delete(selected, entry.Floor.ID)
		}
	}
	if len(selected) > 0 {
		return nil, fmt.Errorf("reviewed tuple is not in full backup")
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	backup.Selection = &Selection{SourcePath: sourcePath, SourceSHA256: hex.EncodeToString(hash[:]), SkippedKnown: len(backup.Entries) - len(entries), SkippedUnresolved: len(backup.Unresolved)}
	backup.Entries = entries
	backup.Unresolved = nil
	return backup, nil
}

func validateSelection(backup *Backup) error {
	if backup.Selection == nil {
		return nil
	}
	selection := backup.Selection
	source, err := Load(selection.SourcePath)
	if err != nil {
		return err
	}
	if source.Selection != nil {
		return fmt.Errorf("invalid nested migration selection")
	}
	data, err := os.ReadFile(selection.SourcePath)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != selection.SourceSHA256 || source.ID != backup.ID || source.Tenant != backup.Tenant || source.Database != backup.Database || source.Instance != backup.Instance || !source.AppliedAt.Equal(backup.AppliedAt) || source.Version != backup.Version || selection.SkippedKnown != len(source.Entries)-len(backup.Entries) || selection.SkippedUnresolved != len(source.Unresolved) {
		return fmt.Errorf("reviewed source backup binding changed")
	}
	original := map[string]Entry{}
	for _, entry := range source.Entries {
		original[entry.Floor.ID] = entry
	}
	for _, entry := range backup.Entries {
		old, ok := original[entry.Floor.ID]
		before, _ := json.Marshal(old)
		after, _ := json.Marshal(entry)
		if !ok || !bytes.Equal(before, after) {
			return fmt.Errorf("reviewed tuple differs from full backup")
		}
	}
	return nil
}

func floorCollection(db *mongo.Database) *mongo.Collection {
	return db.Collection(unrelation.MessageVisibilityCollection, options.Collection().SetReadConcern(readconcern.Majority()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
}
func identity(ctx context.Context, db *mongo.Database, create bool) (string, error) {
	collection := db.Collection("luobo_storage_identity", options.Collection().SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
	var row struct {
		Instance string `bson:"instance"`
	}
	var err error
	if create {
		err = collection.FindOneAndUpdate(ctx, bson.M{"_id": "message-visibility-v1"}, bson.M{"$setOnInsert": bson.M{"instance": uuid.NewString()}}, options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&row)
	} else {
		err = collection.FindOne(ctx, bson.M{"_id": "message-visibility-v1"}).Decode(&row)
	}
	if err == nil && row.Instance == "" {
		err = fmt.Errorf("empty storage identity")
	}
	return row.Instance, err
}

// Resolve exact legacy keys from authoritative owner/conversation records;
// never split an ambiguous legacy delimiter and guess its account owner.
func knownKeys(ctx context.Context, db *mongo.Database) (map[string]ut.MessageVisibilityFloor, error) {
	cur, err := db.Collection("conversation").Find(ctx, bson.M{}, options.Find().SetProjection(bson.M{"owner_user_id": 1, "conversation_id": 1}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	known := map[string]ut.MessageVisibilityFloor{}
	ambiguous := map[string]bool{}
	for cur.Next(ctx) {
		var row struct {
			Owner        string `bson:"owner_user_id"`
			Conversation string `bson:"conversation_id"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, err
		}
		if row.Owner == "" || row.Conversation == "" {
			continue
		}
		for key, floor := range map[string]ut.MessageVisibilityFloor{"MIN_SEQ:" + row.Conversation: {ConversationID: row.Conversation}, "CON_USER_MIN_SEQ:" + row.Conversation + "u:" + row.Owner: {ConversationID: row.Conversation, UserID: row.Owner}} {
			if prior, ok := known[key]; ok && (prior.ConversationID != floor.ConversationID || prior.UserID != floor.UserID) {
				ambiguous[key] = true
			}
			known[key] = floor
		}
	}
	for key := range ambiguous {
		delete(known, key)
	}
	return known, cur.Err()
}

func resolveKey(known map[string]ut.MessageVisibilityFloor, key string) (ut.MessageVisibilityFloor, bool) {
	// The global key has one unambiguous raw conversation suffix and no
	// account owner to infer. Preserve its existing restriction, including
	// notification streams without a user-facing conversation row. Personal
	// legacy keys still require an exact authoritative owner tuple.
	if strings.HasPrefix(key, "MIN_SEQ:") {
		conversation := strings.TrimPrefix(key, "MIN_SEQ:")
		if conversation != "" {
			return ut.MessageVisibilityFloor{ConversationID: conversation}, true
		}
	}
	floor, ok := known[key]
	return floor, ok
}

func Snapshot(ctx context.Context, db *mongo.Database, rdb redis.UniversalClient, tenant string) (*Backup, error) {
	if tenant == "" {
		return nil, fmt.Errorf("tenant identity required")
	}
	instance, err := identity(ctx, db, true)
	if err != nil {
		return nil, err
	}
	known, err := knownKeys(ctx, db)
	if err != nil {
		return nil, err
	}
	backup := &Backup{Version: 1, ID: uuid.NewString(), Tenant: tenant, Database: db.Name(), Instance: instance, AppliedAt: time.Now().UTC().Truncate(time.Millisecond)}
	seen := map[string]bool{}
	for _, pattern := range []string{"MIN_SEQ:*", "CON_USER_MIN_SEQ:*"} {
		var cursor uint64
		for {
			keys, next, err := rdb.Scan(ctx, cursor, pattern, 500).Result()
			if err != nil {
				return nil, err
			}
			for _, key := range keys {
				if seen[key] {
					continue
				}
				seen[key] = true
				value, err := rdb.Get(ctx, key).Result()
				if err == redis.Nil {
					return nil, fmt.Errorf("Redis changed during snapshot; retry during maintenance")
				}
				if err != nil {
					return nil, err
				}
				floor, ok := resolveKey(known, key)
				seq, parseErr := strconv.ParseInt(value, 10, 64)
				if !ok || parseErr != nil || seq < 0 {
					backup.Unresolved = append(backup.Unresolved, Unresolved{key, value, "unknown/ambiguous owner-conversation tuple or invalid sequence"})
					continue
				}
				maxSeq, err := rdb.Get(ctx, "MAX_SEQ:"+floor.ConversationID).Int64()
				if err != nil {
					return nil, fmt.Errorf("maximum sequence unavailable; snapshot aborted")
				}
				if seq > maxSeq && seq-maxSeq > 1 {
					backup.Unresolved = append(backup.Unresolved, Unresolved{key, value, "floor exceeds known maxSeq+1; manual reconciliation required"})
					continue
				}
				floor.ID = unrelation.MessageVisibilityID(floor.ConversationID, floor.UserID)
				floor.MinSeq = seq
				var original StoredFloor
				err = floorCollection(db).FindOne(ctx, bson.M{"_id": floor.ID}).Decode(&original)
				if err != nil && err != mongo.ErrNoDocuments {
					return nil, err
				}
				entry := Entry{Key: key, Value: value, Floor: floor}
				if err == nil {
					if original.ConversationID != floor.ConversationID || original.UserID != floor.UserID {
						return nil, fmt.Errorf("stored floor tuple mismatch")
					}
					entry.Original = &original
				}
				backup.Entries = append(backup.Entries, entry)
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	sort.Slice(backup.Entries, func(i, j int) bool { return backup.Entries[i].Key < backup.Entries[j].Key })
	return backup, nil
}

// Save completes exclusive private-file and directory fsync before Apply can
// be called. Existing backups are never overwritten or silently replaced.
func Save(path string, backup *Backup) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = json.NewEncoder(file).Encode(backup); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	err = dir.Sync()
	closeErr = dir.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func Load(path string) (*Backup, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("backup must be a private regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var backup Backup
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&backup); err != nil {
		return nil, err
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, fmt.Errorf("backup must contain one JSON object")
	}
	return &backup, nil
}

func validate(ctx context.Context, db *mongo.Database, tenant string, backup *Backup) error {
	if err := validateSelection(backup); err != nil {
		return err
	}
	instance, err := identity(ctx, db, false)
	if err != nil {
		return err
	}
	if backup.Version != 1 || backup.ID == "" || backup.AppliedAt.IsZero() || backup.Tenant != tenant || tenant == "" || backup.Database != db.Name() || backup.Instance != instance {
		return fmt.Errorf("backup tenant/database/instance mismatch")
	}
	if len(backup.Unresolved) > 0 {
		return fmt.Errorf("unresolved legacy keys require explicit reconciliation before apply")
	}
	known, err := knownKeys(ctx, db)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, entry := range backup.Entries {
		floor, ok := resolveKey(known, entry.Key)
		seq, err := strconv.ParseInt(entry.Value, 10, 64)
		if !ok || err != nil || seq < 0 || entry.Floor.MinSeq != seq || floor.ConversationID != entry.Floor.ConversationID || floor.UserID != entry.Floor.UserID || entry.Floor.ID != unrelation.MessageVisibilityID(floor.ConversationID, floor.UserID) || seen[entry.Key] {
			return fmt.Errorf("backup tuple/sequence mismatch")
		}
		seen[entry.Key] = true
	}
	return nil
}
func sameOriginal(actual *StoredFloor, original *StoredFloor) bool {
	if actual == nil || original == nil {
		return actual == nil && original == nil
	}
	return actual.ID == original.ID && actual.ConversationID == original.ConversationID && actual.UserID == original.UserID && actual.MinSeq == original.MinSeq && actual.UpdatedAt.Equal(original.UpdatedAt) && actual.MigrationID == original.MigrationID
}
func applied(entry Entry, backup *Backup) StoredFloor {
	value := entry.Floor
	if entry.Original != nil {
		value.MinSeq = max(value.MinSeq, entry.Original.MinSeq)
	}
	value.UpdatedAt = backup.AppliedAt
	return StoredFloor{value, backup.ID}
}
func current(ctx context.Context, collection *mongo.Collection, id string) (*StoredFloor, error) {
	var row StoredFloor
	err := collection.FindOne(ctx, bson.M{"_id": id}).Decode(&row)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return &row, err
}

// Apply is resumable from this same durable backup. It never changes Redis.
// A mismatched preimage stops instead of overwriting a newer application clear.
func Apply(ctx context.Context, db *mongo.Database, rdb redis.UniversalClient, tenant string, backup *Backup) error {
	if err := validate(ctx, db, tenant, backup); err != nil {
		return err
	}
	collection := floorCollection(db)
	for _, entry := range backup.Entries {
		value, err := rdb.Get(ctx, entry.Key).Result()
		if err != nil || value != entry.Value {
			return fmt.Errorf("Redis preimage changed; take a new maintenance snapshot")
		}
	}
	for _, entry := range backup.Entries {
		want := applied(entry, backup)
		actual, err := current(ctx, collection, entry.Floor.ID)
		if err != nil {
			return err
		}
		if sameOriginal(actual, &want) {
			continue
		}
		if !sameOriginal(actual, entry.Original) {
			return fmt.Errorf("durable floor advanced after backup; refusing overwrite")
		}
		if actual == nil {
			_, err = collection.InsertOne(ctx, want)
		} else {
			var result *mongo.UpdateResult
			result, err = collection.ReplaceOne(ctx, bson.M{"_id": actual.ID, "min_seq": actual.MinSeq, "updated_at": actual.UpdatedAt, "migration_id": bson.M{"$in": bson.A{actual.MigrationID, nil}}}, want)
			if err == nil && result.MatchedCount != 1 {
				err = fmt.Errorf("durable floor changed during apply")
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Rollback is CAS guarded and only removes this migration's exact writes.
// It never lowers or removes a floor subsequently touched by the application.
func Rollback(ctx context.Context, db *mongo.Database, tenant string, backup *Backup) error {
	if err := validate(ctx, db, tenant, backup); err != nil {
		return err
	}
	collection := floorCollection(db)
	for _, entry := range backup.Entries {
		want := applied(entry, backup)
		actual, err := current(ctx, collection, want.ID)
		if err != nil {
			return err
		}
		if sameOriginal(actual, entry.Original) {
			continue
		}
		if !sameOriginal(actual, &want) {
			return fmt.Errorf("floor advanced after migration; refusing rollback")
		}
		filter := bson.M{"_id": want.ID, "min_seq": want.MinSeq, "updated_at": want.UpdatedAt, "migration_id": backup.ID}
		if entry.Original == nil {
			var result *mongo.DeleteResult
			result, err = collection.DeleteOne(ctx, filter)
			if err == nil && result.DeletedCount != 1 {
				err = errors.New("floor changed during rollback")
			}
		} else {
			var result *mongo.UpdateResult
			result, err = collection.ReplaceOne(ctx, filter, entry.Original)
			if err == nil && result.MatchedCount != 1 {
				err = errors.New("floor changed during rollback")
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}
