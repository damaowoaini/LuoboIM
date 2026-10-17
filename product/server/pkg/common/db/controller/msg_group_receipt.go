package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

const receiptMessages = "group_receipt_messages_v2"
const receiptRecipients = "group_receipt_recipients_v2"

type persistentGroupRead struct {
	database *mongo.Database
}

func NewPersistentMsgGroupReadDatabase(ctx context.Context, database *mongo.Database) (MsgGroupReadDatabase, error) {
	database = database.Client().Database(database.Name(), options.Database().SetReadConcern(readconcern.Majority()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
	if _, err := database.Collection(receiptMessages).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "conversation_id", Value: 1}, {Key: "client_msg_id", Value: 1}}, Options: options.Index().SetUnique(true).SetName("receipt_message_identity")}); err != nil {
		return nil, err
	}
	_, err := database.Collection(receiptRecipients).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "conversation_id", Value: 1}, {Key: "client_msg_id", Value: 1}, {Key: "user_id", Value: 1}}, Options: options.Index().SetUnique(true).SetName("receipt_recipient_identity")},
		{Keys: bson.D{{Key: "conversation_id", Value: 1}, {Key: "client_msg_id", Value: 1}, {Key: "read_at", Value: 1}, {Key: "user_id", Value: 1}}, Options: options.Index().SetName("receipt_read_page")},
	})
	if err != nil {
		return nil, err
	}
	return &persistentGroupRead{database: database}, nil
}

func receiptIdentity(conversationID, clientID string) string {
	data, _ := json.Marshal([2]string{conversationID, clientID})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// EnsureGroupMessage preserves the old first-observation cohort policy, with
// one atomic, immutable recipient snapshot per verified message. The old block
// collection remains untouched. Its pre-authentication era data is not trusted
// to authorize extra recipients: import timestamps only for current verified
// recipients, and record the filtered count for operational reconciliation.
func (s *persistentGroupRead) EnsureGroupMessage(ctx context.Context, conversationID, clientID string, getUserIDs cache.GetUserIDsFunc) error {
	id := receiptIdentity(conversationID, clientID)
	metadata := s.database.Collection(receiptMessages)
	if err := metadata.FindOne(ctx, bson.M{"_id": id}).Err(); err == nil {
		return nil
	} else if err != mongo.ErrNoDocuments {
		return err
	}
	users, err := getUserIDs(ctx)
	if err != nil {
		return err
	}
	audience := make(map[string]bool, len(users))
	for _, user := range users {
		if user == "" {
			return errs.ErrArgs.Wrap("empty group receipt recipient")
		}
		audience[user] = true
	}
	session, err := s.database.Client().StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (any, error) {
		if err := metadata.FindOne(tx, bson.M{"_id": id}).Err(); err == nil {
			return nil, nil
		} else if err != mongo.ErrNoDocuments {
			return nil, err
		}
		legacyTimes := make(map[string]*time.Time)
		filtered := make(map[string]bool)
		legacy := false
		// Only client IDs without Mongo field-path metacharacters can refer to
		// the old map schema. New recipients use values, never dynamic paths.
		if !strings.ContainsAny(clientID, ".$\x00") {
			key := "msgs." + clientID
			cur, err := s.database.Collection("group_msg_read").Find(tx, bson.M{"conversation_id": conversationID, key: bson.M{"$exists": true}}, options.Find().SetProjection(bson.M{"_id": 0, key: 1}))
			if err != nil {
				return nil, err
			}
			defer cur.Close(tx)
			for cur.Next(tx) {
				var row struct {
					Messages map[string]map[string]*time.Time `bson:"msgs"`
				}
				if err := cur.Decode(&row); err != nil {
					return nil, err
				}
				legacy = true
				for user, readAt := range row.Messages[clientID] {
					if !audience[user] {
						filtered[user] = true
						continue
					}
					previous := legacyTimes[user]
					if readAt != nil && (previous == nil || readAt.Before(*previous)) {
						legacyTimes[user] = readAt
					}
				}
			}
			if err := cur.Err(); err != nil {
				return nil, err
			}
		}
		_, err := metadata.InsertOne(tx, bson.M{"_id": id, "conversation_id": conversationID, "client_msg_id": clientID, "created_at": time.Now().UTC(), "legacy_imported": legacy, "legacy_filtered_recipient_count": len(filtered), "cohort_policy": "verified-current-members-at-first-observation"})
		if err != nil {
			return nil, err
		}
		rows := make([]any, 0, len(audience))
		for user := range audience {
			rows = append(rows, bson.M{"conversation_id": conversationID, "client_msg_id": clientID, "user_id": user, "read_at": legacyTimes[user]})
		}
		if len(rows) > 0 {
			if _, err := s.database.Collection(receiptRecipients).InsertMany(tx, rows); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
	if mongo.IsDuplicateKeyError(err) && metadata.FindOne(ctx, bson.M{"_id": id}).Err() == nil {
		// The competing snapshot transaction is already committed, including
		// its recipients. There is no process-local initialization lock.
		return nil
	}
	return err
}

func (s *persistentGroupRead) MarkGroupMessageRead(ctx context.Context, conversationID, clientID, userID string, readTime *time.Time, users cache.GetUserIDsFunc) (int32, error) {
	if readTime == nil {
		return 0, errs.ErrArgs.Wrap("read time is required")
	}
	if err := s.EnsureGroupMessage(ctx, conversationID, clientID, users); err != nil {
		return 0, err
	}
	filter := bson.M{"conversation_id": conversationID, "client_msg_id": clientID, "user_id": userID}
	unread := bson.M{"conversation_id": conversationID, "client_msg_id": clientID, "user_id": userID, "read_at": nil}
	result, err := s.database.Collection(receiptRecipients).UpdateOne(ctx, unread, bson.M{"$set": bson.M{"read_at": *readTime}})
	if err != nil {
		return 0, err
	}
	if result.ModifiedCount == 1 {
		return constant.GroupMessageReadMarkNew, nil
	}
	if err := s.database.Collection(receiptRecipients).FindOne(ctx, filter).Err(); err != nil {
		if err == mongo.ErrNoDocuments {
			// A newly joined member may be allowed to read older history but is
			// not added retroactively to its immutable recipient denominator.
			return constant.GroupMessageReadMarked, nil
		}
		return 0, err
	}
	return constant.GroupMessageReadMarked, nil
}

func (s *persistentGroupRead) GetMsgNum(ctx context.Context, conversationID, clientID string) (int64, int64, error) {
	cur, err := s.database.Collection(receiptRecipients).Aggregate(ctx, []bson.M{
		{"$match": bson.M{"conversation_id": conversationID, "client_msg_id": clientID}},
		{"$group": bson.M{"_id": nil, "read": bson.M{"$sum": bson.M{"$cond": bson.A{bson.M{"$ne": bson.A{"$read_at", nil}}, 1, 0}}}, "unread": bson.M{"$sum": bson.M{"$cond": bson.A{bson.M{"$eq": bson.A{"$read_at", nil}}, 1, 0}}}}},
	})
	if err != nil {
		return 0, 0, err
	}
	defer cur.Close(ctx)
	var row struct {
		Read, Unread int64
	}
	if cur.Next(ctx) {
		if err := cur.Decode(&row); err != nil {
			return 0, 0, err
		}
	}
	return row.Read, row.Unread, cur.Err()
}

func (s *persistentGroupRead) PageReadUserList(ctx context.Context, conversationID, clientID string, read bool, pageNumber, pageSize int32) (int64, []*cache.UserReadTime, error) {
	if pageNumber < 1 || pageSize < 1 || pageSize > 100 {
		return 0, nil, errs.ErrArgs.Wrap("invalid receipt pagination")
	}
	filter := bson.M{"conversation_id": conversationID, "client_msg_id": clientID, "read_at": nil}
	if read {
		filter["read_at"] = bson.M{"$ne": nil}
	}
	cur, err := s.database.Collection(receiptRecipients).Aggregate(ctx, []bson.M{{"$match": filter}, {"$facet": bson.M{
		"count": bson.A{bson.M{"$count": "value"}},
		"rows":  bson.A{bson.M{"$sort": bson.D{{Key: "read_at", Value: 1}, {Key: "user_id", Value: 1}}}, bson.M{"$skip": int64(pageNumber-1) * int64(pageSize)}, bson.M{"$limit": int64(pageSize)}, bson.M{"$project": bson.M{"_id": 0, "user_id": 1, "read_at": 1}}},
	}}})
	if err != nil {
		return 0, nil, err
	}
	defer cur.Close(ctx)
	var result struct {
		Count []struct{ Value int64 } `bson:"count"`
		Rows  []struct {
			UserID string     `bson:"user_id"`
			ReadAt *time.Time `bson:"read_at"`
		} `bson:"rows"`
	}
	if cur.Next(ctx) {
		if err := cur.Decode(&result); err != nil {
			return 0, nil, err
		}
	}
	var count int64
	if len(result.Count) > 0 {
		count = result.Count[0].Value
	}
	rows := make([]*cache.UserReadTime, 0, len(result.Rows))
	for _, row := range result.Rows {
		value := &cache.UserReadTime{UserID: row.UserID}
		if row.ReadAt != nil {
			value.Time = row.ReadAt.UnixMilli()
		}
		rows = append(rows, value)
	}
	return count, rows, cur.Err()
}
