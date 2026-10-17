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

	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/mgoutil"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
)

func NewS3Mongo(db *mongo.Database) (relation.ObjectInfoModelInterface, error) {
	return NewS3MongoWithContext(context.Background(), db)
}

// The explicit caller lifetime lets an owned role cancel index construction.
// Database identity must be bound to its bootstrap BEFORE this call.
func NewS3MongoWithContext(ctx context.Context, db *mongo.Database) (relation.ObjectInfoModelInterface, error) {
	coll := db.Collection("s3")
	_, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "name", Value: 1},
		},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, err
	}
	return &S3Mongo{coll: coll}, nil
}

type S3Mongo struct {
	coll *mongo.Collection
}

func (o *S3Mongo) SetObject(ctx context.Context, obj *relation.ObjectModel) error {
	if obj == nil || obj.Name == "" || obj.Engine == "" || obj.UserID == "" {
		return errs.ErrArgs.Wrap("object owner, name and engine required")
	}
	// Keep the historical globally unique name index. A concurrent insert for
	// another owner/engine, or a legacy row without an owner, must collide with
	// that index instead of being silently adopted by this upsert.
	filter := bson.M{"name": obj.Name, "engine": obj.Engine, "user_id": obj.UserID}
	update := bson.M{
		"name":                obj.Name,
		"user_id":             obj.UserID,
		"hash":                obj.Hash,
		"engine":              obj.Engine,
		"key":                 obj.Key,
		"size":                obj.Size,
		"content_type":        obj.ContentType,
		"group":               obj.Group,
		"create_time":         obj.CreateTime,
		"completion_proof_id": obj.CompletionProofID,
	}
	result, err := o.coll.UpdateOne(ctx, filter, bson.M{"$set": update}, options.Update().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) {
		// Same-owner concurrent first inserts are idempotent. The retry is a
		// conditional update, never an upsert that could adopt another row.
		result, err = o.coll.UpdateOne(ctx, filter, bson.M{"$set": update})
		if err == nil && result.MatchedCount != 1 {
			return errs.ErrNoPermission.Wrap("object name is already reserved")
		}
	}
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 && result.UpsertedCount != 1 {
		return errs.ErrNoPermission.Wrap("object owner could not be confirmed")
	}
	return nil
}

func (o *S3Mongo) Take(ctx context.Context, engine string, name string) (*relation.ObjectModel, error) {
	if engine == "" {
		return mgoutil.FindOne[*relation.ObjectModel](ctx, o.coll, bson.M{"name": name})
	}
	return mgoutil.FindOne[*relation.ObjectModel](ctx, o.coll, bson.M{"name": name, "engine": engine})
}

func (o *S3Mongo) Delete(ctx context.Context, engine string, name string) error {
	return mgoutil.DeleteOne(ctx, o.coll, bson.M{"name": name, "engine": engine})
}
