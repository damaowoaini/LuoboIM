package office

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/db/dbutil"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/office"
	"github.com/OpenIMSDK/tools/utils"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

func NewTagModel(coll *mongo.Collection) (table.TagInterface, error) {
	//indexModel := mongo.IndexModel{
	//	Keys:    bson.D{{"user_id", 1}},
	//	Options: options.Index(),
	//}
	//if _, err := coll.Indexes().CreateOne(context.Background(), indexModel); err != nil {
	//	return nil, err
	//}
	return &TagModel{coll: coll}, nil
}

type TagModel struct {
	coll *mongo.Collection
}

func (o *TagModel) GetUserTags(ctx context.Context, userID string) ([]*table.Tag, error) {
	return dbutil.MongoFindAll[table.Tag](ctx, o.coll, bson.M{"user_id": userID})
}

func (o *TagModel) Create(ctx context.Context, tag *table.Tag) error {
	if tag.TagID.IsZero() {
		tag.TagID = primitive.NewObjectID()
	}
	utils.InitSlice(&tag.UserIDs)
	utils.InitSlice(&tag.GroupIDs)
	return dbutil.MongoInsertOne(ctx, o.coll, tag)
}

func (o *TagModel) Get(ctx context.Context, tagID string) (*table.Tag, error) {
	id, err := dbutil.MongoID(tagID)
	if err != nil {
		return nil, err
	}
	return dbutil.MongoFindOne[table.Tag](ctx, o.coll, bson.M{
		"_id": id,
	})
}

func (o *TagModel) Find(ctx context.Context, tagIDs []string) ([]*table.Tag, error) {
	ids := make([]primitive.ObjectID, 0, len(tagIDs))
	for _, tagID := range tagIDs {
		id, err := dbutil.MongoID(tagID)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return dbutil.MongoFindAll[table.Tag](ctx, o.coll, bson.M{
		"_id": bson.M{
			"$in": ids,
		},
	})
}

func (o *TagModel) Delete(ctx context.Context, tagID string) error {
	id, err := dbutil.MongoID(tagID)
	if err != nil {
		return err
	}
	return dbutil.MongoDeleteOne(ctx, o.coll, bson.M{
		"_id": id,
	})
}

func (o *TagModel) Update(ctx context.Context, tagID, name string, addUserIDs []string, delUserIDs []string, addGroupIDs []string, delGroupIDs []string) error {
	id, err := dbutil.MongoID(tagID)
	if err != nil {
		return err
	}
	filter := bson.M{
		"_id": id,
	}
	update := bson.M{}
	if name != "" {
		update["$set"] = bson.M{
			"tag_name": name,
		}
	}
	if len(addUserIDs) > 0 {
		update["$addToSet"] = bson.M{
			"user_ids": bson.M{
				"$each": addUserIDs,
			},
		}
	}
	if len(addGroupIDs) > 0 {
		update["$addToSet"] = bson.M{
			"group_ids": bson.M{
				"$each": addGroupIDs,
			},
		}
	}
	if len(update) > 0 {
		if err := dbutil.MongoUpdateOne(ctx, o.coll, filter, update); err != nil {
			return err
		}
		if len(delUserIDs)+len(delGroupIDs) == 0 {
			return nil
		}
		update = bson.M{}
	}
	if len(delUserIDs) > 0 {
		update["$pull"] = bson.M{
			"user_ids": bson.M{
				"$in": delUserIDs,
			},
		}
	}
	if len(delGroupIDs) > 0 {
		update["$pull"] = bson.M{
			"group_ids": bson.M{
				"$in": delGroupIDs,
			},
		}
	}
	if len(update) == 0 {
		return nil
	}
	return dbutil.MongoUpdateOne(ctx, o.coll, filter, update)
}
