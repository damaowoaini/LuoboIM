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

	"github.com/OpenIMSDK/tools/mgoutil"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
)

func NewSensitiveWordMongo(db *mongo.Database) (relation.SensitiveWordModelInterface, error) {
	return NewSensitiveWordMongoWithContext(context.Background(), db)
}

// An explicitly owned API uses its actual cancellable initialization lifetime.
// The legacy constructor above preserves its original context and index schema.
func NewSensitiveWordMongoWithContext(ctx context.Context, db *mongo.Database) (relation.SensitiveWordModelInterface, error) {
	coll := db.Collection(relation.SensitiveWordModelTableName)
	_, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "word", Value: 1},
		},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, err
	}
	_, err = coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "status", Value: 1},
		},
	})
	if err != nil {
		return nil, err
	}
	return &SensitiveWordMgo{coll: coll}, nil
}

type SensitiveWordMgo struct {
	coll *mongo.Collection
}

func (s *SensitiveWordMgo) Create(ctx context.Context, sensitiveWords []*relation.SensitiveWordModel) error {
	return mgoutil.InsertMany(ctx, s.coll, sensitiveWords)
}

func (s *SensitiveWordMgo) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	filter := bson.M{"_id": bson.M{"$in": ids}}
	return mgoutil.DeleteMany(ctx, s.coll, filter)
}

func (s *SensitiveWordMgo) Update(ctx context.Context, id string, data map[string]any) error {
	if len(data) == 0 {
		return nil
	}
	filter := bson.M{"_id": id}
	data["update_time"] = time.Now()
	return mgoutil.UpdateOne(ctx, s.coll, filter, bson.M{"$set": data}, false)
}

func (s *SensitiveWordMgo) Find(ctx context.Context, ids []string) ([]*relation.SensitiveWordModel, error) {
	return mgoutil.Find[*relation.SensitiveWordModel](ctx, s.coll, bson.M{"_id": bson.M{"$in": ids}})
}

func (s *SensitiveWordMgo) FindByWord(ctx context.Context, word string) (*relation.SensitiveWordModel, error) {
	return mgoutil.FindOne[*relation.SensitiveWordModel](ctx, s.coll, bson.M{"word": word})
}

func (s *SensitiveWordMgo) Search(ctx context.Context, keyword string, status *int32, pageNumber, showNumber int32) (int64, []*relation.SensitiveWordModel, error) {
	filter := bson.M{}
	if keyword != "" {
		filter["word"] = primitive.Regex{Pattern: keyword, Options: "i"}
	}
	if status != nil {
		filter["status"] = *status
	}

	// 计算总数
	total, err := s.coll.CountDocuments(ctx, filter)
	if err != nil {
		return 0, nil, err
	}

	// 分页查询
	skip := int64((pageNumber - 1) * showNumber)
	limit := int64(showNumber)
	opts := options.Find().SetSort(bson.M{"create_time": -1}).SetSkip(skip).SetLimit(limit)

	cursor, err := s.coll.Find(ctx, filter, opts)
	if err != nil {
		return 0, nil, err
	}
	defer cursor.Close(ctx)

	var results []*relation.SensitiveWordModel
	if err := cursor.All(ctx, &results); err != nil {
		return 0, nil, err
	}

	return total, results, nil
}

func (s *SensitiveWordMgo) FindAllEnabled(ctx context.Context) ([]*relation.SensitiveWordModel, error) {
	return mgoutil.Find[*relation.SensitiveWordModel](ctx, s.coll, bson.M{"status": 1})
}
