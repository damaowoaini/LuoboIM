package office

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/dbutil"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/office"
	"github.com/OpenIMSDK/tools/errs"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"time"
)

func NewWorkMomentModel(coll *mongo.Collection) (table.WorkMomentInterface, error) {
	//indexModel := mongo.IndexModel{
	//	Keys:    bson.D{{"user_id", 1}},
	//	Options: options.Index(),
	//}
	//if _, err := coll.Indexes().CreateOne(context.Background(), indexModel); err != nil {
	//	return nil, err
	//}
	return &WorkMomentModel{coll: coll}, nil
}

type WorkMomentModel struct {
	coll *mongo.Collection
}

func (o *WorkMomentModel) Create(ctx context.Context, workMoment *table.WorkMoment) error {
	if workMoment.WorkMomentID.IsZero() {
		workMoment.WorkMomentID = primitive.NewObjectID()
	}
	if workMoment.Comments == nil {
		workMoment.Comments = []*table.Comment{}
	}
	if workMoment.LikeUsers == nil {
		workMoment.LikeUsers = []*table.LikeUser{}
	}
	if workMoment.AtUserIDs == nil {
		workMoment.AtUserIDs = []string{}
	}
	if workMoment.PermissionUserIDs == nil {
		workMoment.PermissionUserIDs = []string{}
	}
	if workMoment.Content.Metas == nil {
		workMoment.Content.Metas = []*table.Meta{}
	}
	return dbutil.MongoInsertOne(ctx, o.coll, workMoment)
}

func (o *WorkMomentModel) Delete(ctx context.Context, workMomentID string) error {
	id, err := dbutil.MongoID(workMomentID)
	if err != nil {
		return err
	}
	return dbutil.MongoDeleteOne(ctx, o.coll, bson.M{
		"_id": id,
	})
}

func (o *WorkMomentModel) DeleteComment(ctx context.Context, workMomentID string, commentID string) error {
	id, err := dbutil.MongoID(workMomentID)
	if err != nil {
		return err
	}
	filter := bson.M{
		"_id": id,
	}
	update := bson.M{"$pull": bson.M{"comments": bson.M{"comment_id": commentID}}}
	return dbutil.MongoUpdateOne(ctx, o.coll, filter, update)
}

func (o *WorkMomentModel) Get(ctx context.Context, workMomentID string) (*table.WorkMoment, error) {
	id, err := dbutil.MongoID(workMomentID)
	if err != nil {
		return nil, err
	}
	return dbutil.MongoFindOne[table.WorkMoment](ctx, o.coll, bson.M{
		"_id": id,
	})
}

func (o *WorkMomentModel) SetWorkMomentLike(ctx context.Context, workMomentID string, likeUserID string, like bool, time time.Time) (*table.WorkMoment, error) {
	id, err := dbutil.MongoID(workMomentID)
	if err != nil {
		return nil, err
	}
	filter := bson.M{
		"_id": id,
	}
	var update any
	if like {
		// The RPC's initial read is not a concurrency lock. Decide membership
		// inside this single document update so parallel taps cannot add the
		// same actor twice or invent a fresh unread time on an idempotent like.
		likes := bson.M{"$ifNull": bson.A{"$like_users", bson.A{}}}
		actors := bson.M{"$map": bson.M{"input": likes, "as": "like", "in": "$$like.user_id"}}
		update = mongo.Pipeline{bson.D{{Key: "$set", Value: bson.M{
			"like_users": bson.M{"$cond": bson.A{
				bson.M{"$in": bson.A{bson.M{"$literal": likeUserID}, actors}}, likes,
				bson.M{"$concatArrays": bson.A{likes, bson.M{"$literal": bson.A{table.LikeUser{UserID: likeUserID, CreateTime: time}}}}},
			}},
		}}}}
	} else {
		update = bson.M{
			"$pull": bson.M{"like_users": bson.M{"user_id": likeUserID}},
		}
	}
	return dbutil.MongoFindUpdateOne[table.WorkMoment](ctx, o.coll, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After))
}

func (o *WorkMomentModel) FindPushComment(ctx context.Context, workMomentID string, comment *table.Comment) (*table.WorkMoment, error) {
	id, err := dbutil.MongoID(workMomentID)
	if err != nil {
		return nil, err
	}
	filter := bson.M{
		"_id": id,
	}
	update := bson.M{"$push": bson.M{"comments": comment}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	return dbutil.MongoFindUpdateOne[table.WorkMoment](ctx, o.coll, filter, update, opts)
}

func (o *WorkMomentModel) GetUserSend(ctx context.Context, userID string, opUserID string, showNumber, pageNumber int32) ([]*table.WorkMoment, error) {
	var filter bson.M
	if userID == opUserID {
		filter = bson.M{
			"user_id": userID,
		}
	} else {
		filter = bson.M{
			"$or": bson.A{
				bson.M{"user_id": userID, "permission": constant.WorkMomentPublic},
				bson.M{"user_id": userID, "permission": constant.WorkMomentPermissionCanSee, "permission_user_ids": bson.M{"$in": bson.A{opUserID}}},
				bson.M{"user_id": userID, "permission": constant.WorkMomentPermissionCantSee, "permission_user_ids": bson.M{"$nin": bson.A{opUserID}}},
			},
		}
	}
	opts := options.Find().SetLimit(int64(showNumber)).SetSkip(int64(showNumber) * int64(pageNumber-1)).SetSort(bson.D{{Key: "create_time", Value: -1}, {Key: "_id", Value: -1}})
	return dbutil.MongoFindAll[table.WorkMoment](ctx, o.coll, filter, opts)
}

func (o *WorkMomentModel) GetUserRecv(ctx context.Context, userID string, friendIDs []string, showNumber, pageNumber int32) ([]*table.WorkMoment, error) {
	if friendIDs == nil {
		friendIDs = []string{}
	}
	filter := bson.M{
		"$or": bson.A{
			bson.M{"user_id": userID},
			bson.M{
				"user_id": bson.M{
					"$in": friendIDs,
				},
				"$or": bson.A{
					bson.M{"permission": constant.WorkMomentPublic},
					bson.M{"permission": constant.WorkMomentPermissionCanSee, "permission_user_ids": bson.M{"$in": bson.A{userID}}},
					bson.M{"permission": constant.WorkMomentPermissionCantSee, "permission_user_ids": bson.M{"$nin": bson.A{userID}}},
				},
			},
		},
	}
	opts := options.Find().SetLimit(int64(showNumber)).SetSkip(int64(showNumber) * int64(pageNumber-1)).SetSort(bson.D{{Key: "create_time", Value: -1}, {Key: "_id", Value: -1}})
	return dbutil.MongoFindAll[table.WorkMoment](ctx, o.coll, filter, opts)
}

func (o *WorkMomentModel) GetUnreadCount(ctx context.Context, userID string, readTime *time.Time) (int32, error) {
	if readTime == nil {
		readTime = &time.Time{}
	}
	pipeline := bson.A{
		bson.M{"$match": momentVisibilityFilter(userID)},
		bson.M{
			"$match": bson.M{
				"$or": bson.A{
					bson.M{
						"user_id": userID,
					},
					bson.M{
						"like_users.user_id": userID,
					},
					bson.M{
						"comments.reply_user_id": userID,
					},
					bson.M{
						"at_user_ids": bson.M{
							"$in": bson.A{userID},
						},
					},
				},
			},
		},
		bson.M{
			"$addFields": bson.M{
				"like_users": bson.M{
					"$filter": bson.M{
						"input": "$like_users",
						"as":    "item",
						"cond": bson.M{
							"$gt": bson.A{"$$item.create_time", readTime},
						},
					},
				},
				"comments": bson.M{
					"$filter": bson.M{
						"input": "$comments",
						"as":    "item",
						"cond": bson.M{
							"$gt": bson.A{"$$item.create_time", readTime},
						},
					},
				},
			},
		},
		// 按用户筛选
		bson.M{
			"$addFields": bson.M{
				"like_users": bson.M{
					"$filter": bson.M{
						"input": "$like_users",
						"as":    "item",
						"cond": bson.M{
							"$ne": bson.A{"$$item.user_id", userID},
						},
					},
				},
				"comments": bson.M{
					"$filter": bson.M{
						"input": "$comments",
						"as":    "item",
						"cond": bson.M{
							"$ne": bson.A{"$$item.user_id", userID},
						},
					},
				},
				"at_user_ids": bson.M{
					"$filter": bson.M{
						"input": "$at_user_ids",
						"as":    "item",
						"cond": bson.M{
							"$eq": bson.A{"$$item", userID},
						},
					},
				},
			},
		},
		bson.M{
			"$addFields": bson.M{
				"at_user_ids": bson.M{
					"$map": bson.M{
						"input": "$at_user_ids",
						"as":    "item",
						"in": bson.M{
							"user_id":     "$$item",
							"create_time": "$create_time",
						},
					},
				},
			},
		},
		bson.M{
			"$addFields": bson.M{
				"cause_list": bson.M{
					"$concatArrays": bson.A{"$like_users", "$comments", "$at_user_ids"},
				},
			},
		},
		bson.M{
			"$addFields": bson.M{
				"cause_list": bson.M{
					"$filter": bson.M{
						"input": "$cause_list",
						"as":    "item",
						"cond": bson.M{
							"$gt": bson.A{"$$item.create_time", readTime},
						},
					},
				},
			},
		},
		bson.M{
			"$match": bson.M{
				"$expr": bson.M{
					"$gt": bson.A{
						bson.M{"$size": "$cause_list"},
						0,
					},
				},
			},
		},
		bson.M{
			"$unwind": "$cause_list",
		},
		bson.M{
			"$count": "count",
		},
	}
	type Count struct {
		Count int32 `bson:"count"`
	}
	count, err := dbutil.MongoAggregateAll[Count](ctx, o.coll, pipeline)
	if err == nil {
		if len(count) == 0 {
			return 0, nil
		}
		return count[0].Count, nil
	} else if errs.Unwrap(err) == mongo.ErrNoDocuments {
		return 0, nil
	} else {
		return 0, err
	}
}

func (o *WorkMomentModel) FindRelevant(ctx context.Context, userID string, readTime *time.Time, showNumber, pageNumber int32) ([]*table.WorkMoment, error) {
	if readTime == nil {
		readTime = &time.Time{}
	}
	pipeline := bson.A{
		bson.M{"$match": momentVisibilityFilter(userID)},
		// 匹配
		bson.M{
			"$match": bson.M{
				"$or": bson.A{
					bson.M{
						"user_id": userID,
					},
					bson.M{
						"like_users.user_id": userID,
					},
					bson.M{
						"comments.reply_user_id": userID,
					},
					bson.M{
						"at_user_ids": bson.M{
							"$in": bson.A{userID},
						},
					},
				},
			},
		},
		// 按时间筛选
		bson.M{
			"$addFields": bson.M{
				"like_users": bson.M{
					"$filter": bson.M{
						"input": "$like_users",
						"as":    "item",
						"cond": bson.M{
							"$gt": bson.A{"$$item.create_time", readTime},
						},
					},
				},
				"comments": bson.M{
					"$filter": bson.M{
						"input": "$comments",
						"as":    "item",
						"cond": bson.M{
							"$gt": bson.A{"$$item.create_time", readTime},
						},
					},
				},
			},
		},
		// 按用户筛选
		bson.M{
			"$addFields": bson.M{
				"like_users": bson.M{
					"$filter": bson.M{
						"input": "$like_users",
						"as":    "item",
						"cond": bson.M{
							"$ne": bson.A{"$$item.user_id", userID},
						},
					},
				},
				"comments": bson.M{
					"$filter": bson.M{
						"input": "$comments",
						"as":    "item",
						"cond": bson.M{
							"$ne": bson.A{"$$item.user_id", userID},
						},
					},
				},
				"at_user_ids": bson.M{
					"$filter": bson.M{
						"input": "$at_user_ids",
						"as":    "item",
						"cond": bson.M{
							"$eq": bson.A{"$$item", userID},
						},
					},
				},
			},
		},
		bson.M{
			"$addFields": bson.M{
				"like_users": bson.M{
					"$map": bson.M{
						"input": "$like_users",
						"as":    "item",
						"in": bson.M{
							"$mergeObjects": bson.A{
								"$$item",
								bson.M{"cause_type": 1}, // 1: 点赞
							},
						},
					},
				},
				"comments": bson.M{
					"$map": bson.M{
						"input": "$comments",
						"as":    "item",
						"in": bson.M{
							"$mergeObjects": bson.A{
								"$$item",
								bson.M{"cause_type": 2}, // 2: 评论
							},
						},
					},
				},
				"at_user_ids": bson.M{
					"$map": bson.M{
						"input": "$at_user_ids",
						"as":    "item",
						"in": bson.M{
							"user_id":     "$$item",
							"create_time": "$create_time",
							"cause_type":  3, // 3: @
						},
					},
				},
			},
		},
		bson.M{
			"$addFields": bson.M{
				"cause_list": bson.M{
					"$concatArrays": bson.A{"$like_users", "$comments", "$at_user_ids"},
				},
			},
		},
		bson.M{
			"$addFields": bson.M{
				"cause_list": bson.M{
					"$filter": bson.M{
						"input": "$cause_list",
						"as":    "item",
						"cond": bson.M{
							"$gt": bson.A{"$$item.create_time", readTime},
						},
					},
				},
			},
		},
		bson.M{
			"$match": bson.M{
				"$expr": bson.M{
					"$gt": bson.A{
						bson.M{"$size": "$cause_list"},
						0,
					},
				},
			},
		},
		bson.M{
			"$unwind": "$cause_list",
		},
		bson.M{
			"$sort": bson.D{
				{Key: "cause_list.create_time", Value: -1},
				{Key: "_id", Value: -1},
				{Key: "cause_list.cause_type", Value: 1},
				{Key: "cause_list.comment_id", Value: 1},
				{Key: "cause_list.user_id", Value: 1},
			},
		},
		bson.M{
			"$skip": (pageNumber - 1) * showNumber,
		},
		bson.M{
			"$limit": showNumber,
		},
	}
	type Item struct {
		WorkMomentID      primitive.ObjectID       `bson:"_id"`
		UserID            string                   `bson:"user_id"`
		Content           *table.WorkMomentContent `bson:"content"`
		Permission        int32                    `bson:"permission"`
		PermissionUserIDs []string                 `bson:"permission_user_ids"`
		CreateTime        time.Time                `bson:"create_time"`
		CauseList         bson.Raw                 `bson:"cause_list"`
	}
	items, err := dbutil.MongoAggregateAll[Item](ctx, o.coll, pipeline)
	if err != nil {
		return nil, err
	}
	values := make([]*table.WorkMoment, 0, len(items))
	for _, item := range items {
		wm := table.WorkMoment{
			WorkMomentID:      item.WorkMomentID,
			UserID:            item.UserID,
			Content:           item.Content,
			PermissionUserIDs: item.PermissionUserIDs,
			Permission:        item.Permission,
			CreateTime:        item.CreateTime,
		}
		causeType, ok := item.CauseList.Lookup("cause_type").Int32OK()
		if !ok {
			return nil, errs.ErrInternalServer.Wrap("MongoDB查询错误：cause_type不是int32类型")
		}
		switch causeType {
		case 1: // 点赞
			var user table.LikeUser
			if err := bson.Unmarshal(item.CauseList, &user); err != nil {
				return nil, err
			}
			wm.LikeUsers = append(wm.LikeUsers, &user)
		case 2: // 评论
			var comment table.Comment
			if err := bson.Unmarshal(item.CauseList, &comment); err != nil {
				return nil, err
			}
			wm.Comments = append(wm.Comments, &comment)
		case 3: // @
			var atUser struct {
				UserID string `bson:"user_id"`
			}
			if err := bson.Unmarshal(item.CauseList, &atUser); err != nil {
				return nil, err
			}
			wm.AtUserIDs = append(wm.AtUserIDs, atUser.UserID)
		default:
			return nil, errs.ErrInternalServer.Wrap("MongoDB查询错误：cause_type必须是1、2或3")
		}
		values = append(values, &wm)
	}
	return values, nil
}

// Historical private/excluded posts may contain mentions written by older
// clients. An interaction reference never overrides the post's visibility.
func momentVisibilityFilter(userID string) bson.M {
	return bson.M{"$or": bson.A{
		bson.M{"user_id": userID},
		bson.M{"permission": constant.WorkMomentPublic},
		bson.M{"permission": constant.WorkMomentPermissionCanSee, "permission_user_ids": userID},
		bson.M{"permission": constant.WorkMomentPermissionCantSee, "permission_user_ids": bson.M{"$ne": userID}},
	}}
}
