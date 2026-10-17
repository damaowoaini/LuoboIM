package office

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/dbutil"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/office"
	"github.com/OpenIMSDK/tools/errs"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"time"
)

func NewWorkMomentReadModel(coll *mongo.Collection) (table.WorkMomentReadInterface, error) {
	return &WorkMomentReadModel{coll: coll}, nil
}

type WorkMomentReadModel struct {
	coll *mongo.Collection
}

func (o *WorkMomentReadModel) Set(ctx context.Context, userID string, time time.Time, setType int32) error {
	var set bson.M
	switch setType {
	case constant.OfficeReadTypeCount:
		set = bson.M{
			"count_time": time,
		}
	case constant.OfficeReadTypeList:
		set = bson.M{
			"list_time": time,
		}
	default:
		set = bson.M{
			"count_time": time,
			"list_time":  time,
		}
	}
	return dbutil.MongoUpdateOne(ctx, o.coll, bson.M{"_id": userID}, bson.M{"$set": set}, options.Update().SetUpsert(true))
}

func (o *WorkMomentReadModel) Get(ctx context.Context, userID string) (*table.WorkMomentRead, error) {
	wmr, err := dbutil.MongoFindOne[table.WorkMomentRead](ctx, o.coll, bson.M{"_id": userID})
	if err == nil {
		return wmr, nil
	} else if errs.Unwrap(err) == mongo.ErrNoDocuments {
		return &table.WorkMomentRead{UserID: userID}, nil
	} else {
		return nil, err
	}
}
