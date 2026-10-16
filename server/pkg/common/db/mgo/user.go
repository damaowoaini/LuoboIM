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
	"time"

	"github.com/OpenIMSDK/protocol/user"

	"fmt"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/mgoutil"
	"github.com/OpenIMSDK/tools/pagination"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/registrationproof"
)

func NewUserMongo(db *mongo.Database) (relation.UserModelInterface, error) {
	coll := db.Collection("user")
	_, err := coll.Indexes().CreateOne(context.Background(), mongo.IndexModel{
		Keys: bson.D{
			{Key: "user_id", Value: 1},
		},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, err
	}
	_, err = db.Collection("userCommands").Indexes().CreateOne(context.Background(), mongo.IndexModel{Keys: bson.D{{Key: "userID", Value: 1}, {Key: "type", Value: 1}, {Key: "uuid", Value: 1}}, Options: options.Index().SetName("command_owner_type_uuid").SetUnique(true)})
	if err != nil {
		return nil, fmt.Errorf("user command identity index could not be created; preserve and resolve conflicting historical records before startup: %w", err)
	}
	return &UserMgo{coll: coll}, nil
}

type UserMgo struct {
	coll *mongo.Collection
}

func (u *UserMgo) Create(ctx context.Context, users []*relation.UserModel) error {
	for _, user := range users {
		if user == nil || user.UserID == "" {
			return errs.ErrArgs.Wrap("user is required")
		}
		if err := registrationproof.CheckCreate(ctx, user.UserID, user.Ex); err != nil {
			return errs.ErrNoPermission.Wrap("开户保留字段不可由用户设置")
		}
	}
	return mgoutil.InsertMany(ctx, u.coll, users)
}

func (u *UserMgo) CreateIfAbsent(ctx context.Context, user *relation.UserModel) error {
	if user == nil || user.UserID == "" {
		return errs.ErrArgs.Wrap("bootstrap account ID is required")
	}
	if err := registrationproof.CheckCreate(ctx, user.UserID, user.Ex); err != nil {
		return errs.ErrNoPermission.Wrap("开户保留字段不可由用户设置")
	}
	filter := bson.M{"user_id": user.UserID}
	_, err := u.coll.UpdateOne(ctx, filter, bson.M{"$setOnInsert": user}, options.Update().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) {
		// An overlapping startup may have won the unique user_id insertion. Do
		// not ignore unrelated duplicate-key failures or overwrite the winner.
		return u.coll.FindOne(ctx, filter, options.FindOne().SetProjection(bson.M{"_id": 1})).Err()
	}
	return err
}

func (u *UserMgo) UpdateByMap(ctx context.Context, userID string, args map[string]any) (err error) {
	if len(args) == 0 {
		return nil
	}
	if err := registrationproof.CheckUpdateMap(args); err != nil {
		return errs.ErrArgs.Wrap("用户扩展资料字段无效")
	}
	if value, updating := args["ex"]; updating {
		var original struct {
			Ex string `bson:"ex"`
		}
		if err := u.coll.FindOne(ctx, bson.M{"user_id": userID}, options.FindOne().SetProjection(bson.M{"ex": 1})).Decode(&original); err != nil {
			if err == mongo.ErrNoDocuments {
				return errs.ErrRecordNotFound.Wrap()
			}
			return err
		}
		protected, err := registrationproof.ProtectUpdate(original.Ex, value.(string))
		if err != nil {
			return errs.ErrNoPermission.Wrap("开户保留字段不能修改")
		}
		update := make(map[string]any, len(args))
		for key, value := range args {
			update[key] = value
		}
		update["ex"] = protected
		filter := bson.M{"user_id": userID, "ex": original.Ex}
		if original.Ex == "" {
			filter["ex"] = bson.M{"$in": bson.A{"", nil}}
		}
		// The session transaction normally serializes this read/write. The CAS
		// also prevents a concurrent proof insertion from being erased in a
		// standalone/legacy deployment or through a direct model caller.
		result, err := u.coll.UpdateOne(ctx, filter, bson.M{"$set": update})
		if err != nil {
			return err
		}
		if result.MatchedCount != 1 {
			return errs.ErrArgs.Wrap("用户资料已变化，请重试")
		}
		return nil
	}
	return mgoutil.UpdateOne(ctx, u.coll, bson.M{"user_id": userID}, bson.M{"$set": args}, true)
}

func (u *UserMgo) Find(ctx context.Context, userIDs []string) (users []*relation.UserModel, err error) {
	return mgoutil.Find[*relation.UserModel](ctx, u.coll, bson.M{"user_id": bson.M{"$in": userIDs}})
}

func (u *UserMgo) Take(ctx context.Context, userID string) (user *relation.UserModel, err error) {
	return mgoutil.FindOne[*relation.UserModel](ctx, u.coll, bson.M{"user_id": userID})
}

func (u *UserMgo) TakeNotification(ctx context.Context, level int64) (user []*relation.UserModel, err error) {
	return mgoutil.Find[*relation.UserModel](ctx, u.coll, bson.M{"app_manger_level": level})
}

func (u *UserMgo) TakeByNickname(ctx context.Context, nickname string) (user []*relation.UserModel, err error) {
	return mgoutil.Find[*relation.UserModel](ctx, u.coll, bson.M{"nickname": nickname})
}

func (u *UserMgo) Page(ctx context.Context, pagination pagination.Pagination) (count int64, users []*relation.UserModel, err error) {
	return mgoutil.FindPage[*relation.UserModel](ctx, u.coll, bson.M{}, pagination)
}

func (u *UserMgo) PageFindUser(ctx context.Context, level int64, pagination pagination.Pagination) (count int64, users []*relation.UserModel, err error) {
	return mgoutil.FindPage[*relation.UserModel](ctx, u.coll, bson.M{"app_manger_level": level}, pagination)
}

func (u *UserMgo) GetAllUserID(ctx context.Context, pagination pagination.Pagination) (int64, []string, error) {
	return mgoutil.FindPage[string](ctx, u.coll, bson.M{}, pagination, options.Find().SetProjection(bson.M{"user_id": 1}))
}

func (u *UserMgo) Exist(ctx context.Context, userID string) (exist bool, err error) {
	return mgoutil.Exist(ctx, u.coll, bson.M{"user_id": userID})
}

func (u *UserMgo) GetUserGlobalRecvMsgOpt(ctx context.Context, userID string) (opt int, err error) {
	return mgoutil.FindOne[int](ctx, u.coll, bson.M{"user_id": userID}, options.FindOne().SetProjection(bson.M{"global_recv_msg_opt": 1}))
}

func (u *UserMgo) CountTotal(ctx context.Context, before *time.Time) (count int64, err error) {
	if before == nil {
		return mgoutil.Count(ctx, u.coll, bson.M{})
	}
	return mgoutil.Count(ctx, u.coll, bson.M{"create_time": bson.M{"$lt": before}})
}

// Add is idempotent for the same owner/type/UUID/payload. The compound
// unique index enforces this under concurrent requests; conflicting retries fail.
func (u *UserMgo) AddUserCommand(ctx context.Context, userID string, kind int32, id, value, ex string) error {
	collection := u.coll.Database().Collection("userCommands")
	filter := bson.M{"userID": userID, "type": kind, "uuid": id}
	_, err := collection.UpdateOne(ctx, filter, bson.M{"$setOnInsert": bson.M{"userID": userID, "type": kind, "uuid": id, "value": value, "ex": ex, "createTime": time.Now().Unix()}}, options.Update().SetUpsert(true))
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return err
	}
	var existing struct {
		Value string `bson:"value"`
		Ex    string `bson:"ex"`
	}
	if err := collection.FindOne(ctx, filter).Decode(&existing); err != nil {
		return err
	}
	if existing.Value != value || existing.Ex != ex {
		return errs.ErrDuplicateKey.Wrap("command UUID already has a different payload")
	}
	return nil
}
func (u *UserMgo) DeleteUserCommand(ctx context.Context, userID string, kind int32, id string) error {
	_, err := u.coll.Database().Collection("userCommands").DeleteOne(ctx, bson.M{"userID": userID, "type": kind, "uuid": id})
	return err
}
func (u *UserMgo) UpdateUserCommand(ctx context.Context, userID string, kind int32, id string, value, ex *string) error {
	updates := bson.M{}
	if value != nil {
		updates["value"] = *value
	}
	if ex != nil {
		updates["ex"] = *ex
	}
	if len(updates) == 0 {
		return errs.ErrArgs.Wrap("no command fields to update")
	}
	result, err := u.coll.Database().Collection("userCommands").UpdateOne(ctx, bson.M{"userID": userID, "type": kind, "uuid": id}, bson.M{"$set": updates})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return errs.ErrRecordNotFound.Wrap("command not found")
	}
	return nil
}
func (u *UserMgo) GetUserCommand(ctx context.Context, userID string, kind int32) ([]*user.CommandInfoResp, error) {
	filter := bson.M{"userID": userID}
	if kind != 0 {
		filter["type"] = kind
	}
	cursor, err := u.coll.Database().Collection("userCommands").Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "createTime", Value: -1}, {Key: "uuid", Value: 1}}).SetLimit(10001))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	commands := make([]*user.CommandInfoResp, 0)
	for cursor.Next(ctx) {
		var document struct {
			Type       int32  `bson:"type"`
			UUID       string `bson:"uuid"`
			Value      string `bson:"value"`
			Ex         string `bson:"ex"`
			CreateTime int64  `bson:"createTime"`
		}
		if err := cursor.Decode(&document); err != nil {
			return nil, err
		}
		commands = append(commands, &user.CommandInfoResp{Type: document.Type, Uuid: document.UUID, Value: document.Value, Ex: document.Ex, CreateTime: document.CreateTime})
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	if len(commands) > 10000 {
		return nil, errs.ErrArgs.Wrap("too many commands for unpaginated protocol")
	}
	return commands, nil
}

func (u *UserMgo) CountRangeEverydayTotal(ctx context.Context, start time.Time, end time.Time) (map[string]int64, error) {
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
	items, err := mgoutil.Aggregate[Item](ctx, u.coll, pipeline)
	if err != nil {
		return nil, err
	}
	res := make(map[string]int64, len(items))
	for _, item := range items {
		res[item.Date] = item.Count
	}
	return res, nil
}
