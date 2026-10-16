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

package api

import (
	"time"

	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/gin-gonic/gin"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/sensitiveword"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type SensitiveWordApi struct {
	db      relation.SensitiveWordModelInterface
	checker *sensitiveword.SensitiveWordChecker
}

func NewSensitiveWordApi() (*SensitiveWordApi, error) {
	mongoClient, err := unrelation.NewMongo()
	if err != nil {
		return nil, err
	}
	db, err := mgo.NewSensitiveWordMongo(mongoClient.GetDatabase())
	if err != nil {
		return nil, err
	}
	rdb, err := cache.NewRedis()
	if err != nil {
		return nil, err
	}
	checker := sensitiveword.NewSensitiveWordChecker(db, rdb)
	return &SensitiveWordApi{
		db:      db,
		checker: checker,
	}, nil
}

// AddSensitiveWord 添加敏感词
func (s *SensitiveWordApi) AddSensitiveWord(c *gin.Context) {
	if err := authverify.CheckAdmin(c); err != nil {
		apiresp.GinError(c, err)
		return
	}
	var req struct {
		Words []string `json:"words" binding:"required"`
	}
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}

	now := time.Now()
	words := make([]*relation.SensitiveWordModel, 0, len(req.Words))
	for _, word := range req.Words {
		if word == "" {
			continue
		}
		words = append(words, &relation.SensitiveWordModel{
			ID:         generateID(),
			Word:       word,
			Status:     1, // 默认启用
			CreateTime: now,
			UpdateTime: now,
		})
	}

	if len(words) == 0 {
		apiresp.GinError(c, errs.ErrArgs.Wrap("敏感词不能为空"))
		return
	}

	if err := s.db.Create(c, words); err != nil {
		log.ZError(c, "添加敏感词失败", err)
		apiresp.GinError(c, err)
		return
	}

	// 强制刷新缓存
	_ = s.checker.ForceReload(c)

	apiresp.GinSuccess(c, nil)
}

// DeleteSensitiveWord 删除敏感词
func (s *SensitiveWordApi) DeleteSensitiveWord(c *gin.Context) {
	if err := authverify.CheckAdmin(c); err != nil {
		apiresp.GinError(c, err)
		return
	}
	var req struct {
		IDs []string `json:"ids" binding:"required"`
	}
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}

	if err := s.db.Delete(c, req.IDs); err != nil {
		log.ZError(c, "删除敏感词失败", err)
		apiresp.GinError(c, err)
		return
	}

	// 强制刷新缓存
	_ = s.checker.ForceReload(c)

	apiresp.GinSuccess(c, nil)
}

// UpdateSensitiveWord 更新敏感词
func (s *SensitiveWordApi) UpdateSensitiveWord(c *gin.Context) {
	if err := authverify.CheckAdmin(c); err != nil {
		apiresp.GinError(c, err)
		return
	}
	var req struct {
		ID     string  `json:"id" binding:"required"`
		Word   *string `json:"word"`
		Status *int32  `json:"status"`
	}
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}

	data := make(map[string]any)
	if req.Word != nil {
		data["word"] = *req.Word
	}
	if req.Status != nil {
		data["status"] = *req.Status
	}

	if len(data) == 0 {
		apiresp.GinError(c, errs.ErrArgs.Wrap("没有要更新的字段"))
		return
	}

	if err := s.db.Update(c, req.ID, data); err != nil {
		log.ZError(c, "更新敏感词失败", err)
		apiresp.GinError(c, err)
		return
	}

	// 强制刷新缓存
	_ = s.checker.ForceReload(c)

	apiresp.GinSuccess(c, nil)
}

// SearchSensitiveWord 搜索敏感词
func (s *SensitiveWordApi) SearchSensitiveWord(c *gin.Context) {
	if err := authverify.CheckAdmin(c); err != nil {
		apiresp.GinError(c, err)
		return
	}
	var req struct {
		Keyword    string `json:"keyword"`
		Status     *int32 `json:"status"`
		PageNumber int32  `json:"pageNumber" binding:"required"`
		ShowNumber int32  `json:"showNumber" binding:"required"`
	}
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}

	total, words, err := s.db.Search(c, req.Keyword, req.Status, req.PageNumber, req.ShowNumber)
	if err != nil {
		log.ZError(c, "搜索敏感词失败", err)
		apiresp.GinError(c, err)
		return
	}

	apiresp.GinSuccess(c, gin.H{
		"total": total,
		"words": words,
	})
}

// ReloadSensitiveWord 重新加载敏感词缓存
func (s *SensitiveWordApi) ReloadSensitiveWord(c *gin.Context) {
	if err := authverify.CheckAdmin(c); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := s.checker.ForceReload(c); err != nil {
		log.ZError(c, "重新加载敏感词失败", err)
		apiresp.GinError(c, err)
		return
	}

	apiresp.GinSuccess(c, nil)
}

// generateID 生成唯一ID
func generateID() string {
	return primitive.NewObjectID().Hex()
}
