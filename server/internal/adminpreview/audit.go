package adminpreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
	"time"
)

type MongoAudit struct{ collection *mongo.Collection }

func NewMongoAudit(ctx context.Context, db *mongo.Database) (*MongoAudit, error) {
	c := db.Collection("admin_message_preview_audit", options.Collection().SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
	_, err := c.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "actor", Value: 1}, {Key: "target_user_id", Value: 1}, {Key: "created_at", Value: -1}}})
	return &MongoAudit{collection: c}, err
}
func (a *MongoAudit) Begin(ctx context.Context, actor, operation string, req Request) (string, error) {
	id := uuid.NewString()
	keyword := sha256.Sum256([]byte(req.Keyword))
	_, err := a.collection.InsertOne(ctx, bson.M{"_id": id, "actor": actor, "target_user_id": req.UserID, "conversation_id": req.ConversationID, "operation": operation, "keyword_sha256": hex.EncodeToString(keyword[:]), "start_time": req.StartTime, "end_time": req.EndTime, "page_size": req.PageSize, "page_number": req.PageNumber, "continued": req.Cursor != "", "created_at": time.Now().UTC(), "state": "pending"})
	return id, err
}
func (a *MongoAudit) Finish(ctx context.Context, id, state string, returned, scanned int) error {
	result, err := a.collection.UpdateOne(ctx, bson.M{"_id": id, "state": "pending"}, bson.M{"$set": bson.M{"state": state, "returned": returned, "scanned": scanned, "finished_at": time.Now().UTC()}})
	if err == nil && result.MatchedCount != 1 {
		return mongo.ErrNoDocuments
	}
	return err
}
