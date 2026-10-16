package unrelation

import (
	"context"
	"time"

	"github.com/OpenIMSDK/tools/errs"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
)

func NewMsgGroupRead(database *mongo.Database) (unrelation.GroupMsgReadInterface, error) {
	collection := database.Collection(new(unrelation.GroupMsgReadModel).TableName())
	indexModel := mongo.IndexModel{
		Keys: bson.D{
			{Key: "conversation_id", Value: 1},
			{Key: "index", Value: -1},
		},
	}
	_, err := collection.Indexes().CreateOne(context.Background(), indexModel, options.CreateIndexes().SetMaxTime(10*time.Second))
	if err != nil {
		return nil, err
	}
	return &msgGroupRead{collection: collection}, nil
}

type msgGroupRead struct {
	collection *mongo.Collection
}

func (m *msgGroupRead) GetMaxIndex(ctx context.Context, conversationID string) (index int, num int, err error) {
	cur, err := m.collection.Aggregate(ctx, []bson.M{
		{"$match": bson.M{"conversation_id": conversationID}},
		{"$project": bson.M{"_id": 0, "index": 1, "num": bson.M{"$size": bson.M{"$objectToArray": "$msgs"}}}},
		{"$sort": bson.M{"index": -1}},
		{"$limit": 1},
	})
	if err != nil {
		return 0, 0, errs.Wrap(err)
	}
	defer cur.Close(ctx)
	if !cur.Next(ctx) {
		return 0, 0, nil
	}
	var res struct {
		Index int `bson:"index"`
		Num   int `bson:"num"`
	}
	if err := cur.Decode(&res); err != nil {
		return 0, 0, errs.Wrap(err)
	}
	return res.Index, res.Num, nil
}

func (m *msgGroupRead) GetNum(ctx context.Context, conversationID string, index int) (int, error) {
	cur, err := m.collection.Aggregate(ctx, []bson.M{
		{"$match": bson.M{"conversation_id": conversationID, "index": index}},
		{"$project": bson.M{"_id": 0, "index": 0, "num": bson.M{"$size": bson.M{"$objectToArray": "$msgs"}}}},
	})
	if err != nil {
		return 0, err
	}
	if !cur.Next(ctx) {
		return 0, nil
	}
	val := struct {
		Num int `bson:"num"`
	}{}
	if err := cur.Decode(&val); err != nil {
		return 0, err
	}
	return val.Num, nil
}

func (m *msgGroupRead) Insert(ctx context.Context, model *unrelation.GroupMsgReadModel) error {
	_, err := m.collection.InsertOne(ctx, model)
	return errs.Wrap(err)
}

func (m *msgGroupRead) InsertMsg(ctx context.Context, conversationID string, index int, clientMsgID string, users map[string]*time.Time) error {
	key := "msgs." + clientMsgID
	filter := bson.M{
		"conversation_id": conversationID,
		"index":           index,
		//key: bson.M{
		//	"$exists": false,
		//},
	}
	update := bson.M{
		"$set": bson.M{
			key: users,
		},
	}
	res, err := m.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errs.Wrap(mongo.ErrNilDocument)
	}
	return nil
}

func (m *msgGroupRead) SetMsgRead(ctx context.Context, conversationID string, index int, clientMsgID string, userID string, readTime *time.Time) error {
	key := "msgs." + clientMsgID + "." + userID
	filter := bson.M{
		"conversation_id": conversationID,
		"index":           index,
	}
	update := bson.M{
		"$set": bson.M{
			key: readTime,
		},
	}
	res, err := m.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errs.Wrap(mongo.ErrNilDocument)
	}
	return nil
}

func (m *msgGroupRead) GetMsgRead(ctx context.Context, conversationID string, index int, clientMsgID string) (map[string]*time.Time, error) {
	key := "msgs." + clientMsgID
	cur, err := m.collection.Aggregate(ctx, []bson.M{
		{"$match": bson.M{"conversation_id": conversationID, "index": index, key: bson.M{"$exists": true}}},
		{"$project": bson.M{"_id": 0, key: 1}},
		{"$limit": 1},
	})
	if err != nil {
		return nil, errs.Wrap(err)
	}
	if !cur.Next(ctx) {
		return map[string]*time.Time{}, nil
	}
	val := struct {
		Msgs map[string]map[string]*time.Time `bson:"msgs"`
	}{}
	if err := cur.Decode(&val); err != nil {
		return nil, errs.Wrap(err)
	}
	return val.Msgs[clientMsgID], nil
}

func (m *msgGroupRead) GetMsgIndex(ctx context.Context, conversationID string, clientMsgID string) (int, error) {
	res := struct {
		Index int `bson:"index"`
	}{}
	err := m.collection.FindOne(ctx, bson.M{
		"conversation_id": conversationID,
		"msgs." + clientMsgID: bson.M{
			"$exists": true,
		},
	}, &options.FindOneOptions{Projection: bson.M{
		"index": 1,
	}}).Decode(&res)
	if err == nil {
		return res.Index, err
	} else if err == mongo.ErrNoDocuments {
		return 0, nil
	} else {
		return 0, errs.Wrap(err)
	}
}
