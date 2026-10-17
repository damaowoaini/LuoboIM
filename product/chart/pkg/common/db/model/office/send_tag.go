package office

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/db/dbutil"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/office"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func NewSendTagLogModel(coll *mongo.Collection) (table.SendTagLogInterface, error) {
	//indexModel := mongo.IndexModel{
	//	Keys:    bson.D{{"user_id", 1}},
	//	Options: options.Index(),
	//}
	//if _, err := coll.Indexes().CreateOne(context.Background(), indexModel); err != nil {
	//	return nil, err
	//}
	return &SendTagLogModel{coll: coll}, nil
}

type SendTagLogModel struct {
	coll *mongo.Collection
}

func (o *SendTagLogModel) Page(ctx context.Context, userID string, pageNumber int32, showNumber int32) ([]*table.TagSendLog, error) {
	opts := options.Find().SetLimit(int64(showNumber)).SetSkip(int64(showNumber) * int64(pageNumber-1)).SetSort(bson.M{"send_time": -1})
	return dbutil.MongoFindAll[table.TagSendLog](ctx, o.coll, bson.M{"send_user_id": userID}, opts)
}

func (o *SendTagLogModel) Create(ctx context.Context, tag *table.TagSendLog) error {
	if tag.ID.IsZero() {
		tag.ID = primitive.NewObjectID()
	}
	return dbutil.MongoInsertOne(ctx, o.coll, tag)
}

func (o *SendTagLogModel) FindSendUserID(ctx context.Context, ids []string) (map[string]string, error) {
	bIDs := make([]primitive.ObjectID, 0, len(ids))
	for _, id := range ids {
		bid, err := dbutil.MongoID(id)
		if err != nil {
			return nil, err
		}
		bIDs = append(bIDs, bid)
	}
	type Item struct {
		ID         primitive.ObjectID `bson:"_id"`
		SendUserID string             `bson:"send_user_id"`
	}
	res, err := dbutil.MongoFindAll[Item](ctx, o.coll, bson.M{
		"_id": bson.M{
			"$in": bIDs,
		},
	})
	if err != nil {
		return nil, err
	}
	ret := make(map[string]string)
	for _, re := range res {
		ret[re.ID.Hex()] = re.SendUserID
	}
	return ret, nil
}

func (o *SendTagLogModel) Delete(ctx context.Context, ids []string) error {
	bIDs := make([]primitive.ObjectID, 0, len(ids))
	for _, id := range ids {
		bid, err := dbutil.MongoID(id)
		if err != nil {
			return err
		}
		bIDs = append(bIDs, bid)
	}
	return dbutil.MongoDeleteMany(ctx, o.coll, bson.M{
		"_id": bson.M{
			"$in": bIDs,
		},
	})
}
