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

package relation

import (
	"context"
	"time"
)

const (
	SensitiveWordModelTableName = "sensitive_word"
)

type SensitiveWordModel struct {
	ID         string    `bson:"_id" json:"id"`
	Word       string    `bson:"word" json:"word"`
	Status     int32     `bson:"status" json:"status"`           // 1:启用 0:禁用
	CreateTime time.Time `bson:"create_time" json:"createTime"`
	UpdateTime time.Time `bson:"update_time" json:"updateTime"`
}

func (SensitiveWordModel) TableName() string {
	return SensitiveWordModelTableName
}

type SensitiveWordModelInterface interface {
	Create(ctx context.Context, sensitiveWords []*SensitiveWordModel) error
	Delete(ctx context.Context, ids []string) error
	Update(ctx context.Context, id string, data map[string]any) error
	Find(ctx context.Context, ids []string) ([]*SensitiveWordModel, error)
	FindByWord(ctx context.Context, word string) (*SensitiveWordModel, error)
	Search(ctx context.Context, keyword string, status *int32, pageNumber, showNumber int32) (int64, []*SensitiveWordModel, error)
	FindAllEnabled(ctx context.Context) ([]*SensitiveWordModel, error)
}
