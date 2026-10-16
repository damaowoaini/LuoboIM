package unrelation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

const MessageVisibilityCollection = "message_visibility_floors"

func MessageVisibilityID(conversationID, userID string) string {
	// JSON array is unambiguous even for legacy IDs containing separators.
	encoded, _ := json.Marshal([2]string{conversationID, userID})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (m *MsgMongoDriver) visibilityCollection() *mongo.Collection {
	return m.MsgCollection.Database().Collection(MessageVisibilityCollection, options.Collection().
		SetReadConcern(readconcern.Majority()).SetReadPreference(readpref.Primary()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
}

// Each tuple is an atomic, idempotent monotonic write. A failed batch may have
// advanced some tuples; callers receive an error and can safely retry it.
func (m *MsgMongoDriver) rawAdvanceMessageVisibility(ctx context.Context, floors []table.MessageVisibilityFloor) error {
	for _, floor := range floors {
		if floor.ConversationID == "" || floor.MinSeq < 0 {
			return fmt.Errorf("invalid message visibility floor")
		}
	}
	collection := m.visibilityCollection()
	for _, floor := range floors {
		id := MessageVisibilityID(floor.ConversationID, floor.UserID)
		filter := bson.M{"_id": id, "conversation_id": floor.ConversationID, "user_id": floor.UserID}
		update := bson.M{"$setOnInsert": bson.M{"conversation_id": floor.ConversationID, "user_id": floor.UserID}, "$max": bson.M{"min_seq": floor.MinSeq, "updated_at": time.Now().UTC()}}
		_, err := collection.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
		if mongo.IsDuplicateKeyError(err) {
			// Concurrent first writers must still apply their higher floor.
			var result *mongo.UpdateResult
			result, err = collection.UpdateOne(ctx, filter, update)
			if err == nil && result.MatchedCount != 1 {
				err = fmt.Errorf("message visibility tuple mismatch")
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *MsgMongoDriver) ReadMessageVisibility(ctx context.Context, keys []table.MessageVisibilityFloor) ([]table.MessageVisibilityFloor, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(keys))
	expected := make(map[string]table.MessageVisibilityFloor, len(keys))
	for _, key := range keys {
		if key.ConversationID == "" {
			return nil, fmt.Errorf("invalid message visibility key")
		}
		id := MessageVisibilityID(key.ConversationID, key.UserID)
		ids = append(ids, id)
		expected[id] = key
	}
	cur, err := m.visibilityCollection().Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var floors []table.MessageVisibilityFloor
	if err := cur.All(ctx, &floors); err != nil {
		return nil, err
	}
	for _, floor := range floors {
		key, ok := expected[floor.ID]
		if !ok || floor.ConversationID != key.ConversationID || floor.UserID != key.UserID || floor.MinSeq < 0 {
			return nil, fmt.Errorf("message visibility tuple mismatch")
		}
	}
	return floors, nil
}
