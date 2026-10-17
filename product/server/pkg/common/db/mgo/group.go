// Copyright © 2023 OpenIM. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mgo

import (
	"context"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"

	"github.com/OpenIMSDK/tools/mgoutil"
	"github.com/OpenIMSDK/tools/pagination"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
)

func NewGroupMongo(db *mongo.Database) (relation.GroupModelInterface, error) {
	return NewGroupMongoContext(context.Background(), db)
}

func NewGroupMongoContext(ctx context.Context, db *mongo.Database) (relation.GroupModelInterface, error) {

	coll := db.Collection("group")
	_, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "group_id", Value: 1},
		},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, err
	}
	return &GroupMgo{coll: coll}, nil
}

type GroupMgo struct {
	coll *mongo.Collection
}

func (g *GroupMgo) Create(ctx context.Context, groups []*relation.GroupModel) (err error) {
	if len(groups) == 0 {
		return nil
	}
	documents := make([]any, 0, len(groups))
	for _, group := range groups {
		if group == nil {
			return groupauthority.ErrInvalidScope
		}
		document, err := groupauthority.NewGroupDocument(ctx, g.coll.Database(), group)
		if err != nil {
			return err
		}
		documents = append(documents, document)
	}
	_, err = g.coll.InsertMany(ctx, documents)
	return err
}

func (g *GroupMgo) UpdateStatus(ctx context.Context, groupID string, status int32) (err error) {
	return g.UpdateMap(ctx, groupID, map[string]any{"status": status})
}

func (g *GroupMgo) UpdateMap(ctx context.Context, groupID string, args map[string]any) (err error) {
	if err := groupauthority.Require(ctx, g.coll.Database(), groupID); err != nil {
		return err
	}
	if err := groupauthority.CheckFields(args); err != nil {
		return err
	}
	if len(args) == 0 {
		return nil
	}
	filter, err := groupAuthorityWriteFilter(groupID, args)
	if err != nil {
		return err
	}
	return mgoutil.UpdateOne(ctx, g.coll, filter, bson.M{"$set": args}, true)
}

// Defense in depth below every RPC: a dismissed group cannot be made active or
// edited through a future scoped caller that forgets the final state check.
// Repeating the status=Dismissed write remains legal for explicit hard cleanup.
func groupAuthorityWriteFilter(groupID string, args map[string]any) (bson.M, error) {
	filter := bson.M{"group_id": groupID}
	if value, present := args["status"]; present {
		status, ok := value.(int32)
		if !ok {
			return nil, errs.ErrArgs.Wrap("invalid group status type")
		}
		if status == constant.GroupStatusDismissed && len(args) == 1 {
			return filter, nil
		}
	}
	filter["status"] = bson.M{"$ne": int32(constant.GroupStatusDismissed)}
	return filter, nil
}

func (g *GroupMgo) Find(ctx context.Context, groupIDs []string) (groups []*relation.GroupModel, err error) {
	return mgoutil.Find[*relation.GroupModel](ctx, g.coll, bson.M{"group_id": bson.M{"$in": groupIDs}})
}

func (g *GroupMgo) Take(ctx context.Context, groupID string) (group *relation.GroupModel, err error) {
	return mgoutil.FindOne[*relation.GroupModel](ctx, g.coll, bson.M{"group_id": groupID})
}

func (g *GroupMgo) Search(ctx context.Context, keyword string, pagination pagination.Pagination) (total int64, groups []*relation.GroupModel, err error) {
	return mgoutil.FindPage[*relation.GroupModel](ctx, g.coll, bson.M{"group_name": bson.M{"$regex": keyword}}, pagination)
}

func (g *GroupMgo) CountTotal(ctx context.Context, before *time.Time) (count int64, err error) {
	if before == nil {
		return mgoutil.Count(ctx, g.coll, bson.M{})
	}
	return mgoutil.Count(ctx, g.coll, bson.M{"create_time": bson.M{"$lt": before}})
}

func (g *GroupMgo) CountRangeEverydayTotal(ctx context.Context, start time.Time, end time.Time) (map[string]int64, error) {
	pipeline := bson.A{
		bson.M{
			"$match": bson.M{
				"create_time": bson.M{
					"$gte": start,
					"$lt":  end,
				},
			},
		},
		bson.M{
			"$group": bson.M{
				"_id": bson.M{
					"$dateToString": bson.M{
						"format": "%Y-%m-%d",
						"date":   "$create_time",
					},
				},
				"count": bson.M{
					"$sum": 1,
				},
			},
		},
	}
	type Item struct {
		Date  string `bson:"_id"`
		Count int64  `bson:"count"`
	}
	items, err := mgoutil.Aggregate[Item](ctx, g.coll, pipeline)
	if err != nil {
		return nil, err
	}
	res := make(map[string]int64, len(items))
	for _, item := range items {
		res[item.Date] = item.Count
	}
	return res, nil
}
