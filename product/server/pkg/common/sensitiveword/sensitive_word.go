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

package sensitiveword

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/OpenIMSDK/tools/log"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/redis/go-redis/v9"
)

const (
	sensitiveWordCacheKey = "openim:sensitive_words:cache"
	sensitiveWordVersionKey = "openim:sensitive_words:version"
	sensitiveWordReloadChannel = "sensitive_word_reload" // Redis 频道名称
)

// SensitiveWordChecker 敏感词检查器
type SensitiveWordChecker struct {
	db             relation.SensitiveWordModelInterface
	rdb            redis.UniversalClient // Redis 客户端
	words          map[string]bool // 敏感词集合
	mu             sync.RWMutex
	lastUpdate     time.Time
	updateInterval time.Duration
	cacheVersion   string // 缓存版本号
}

// NewSensitiveWordChecker 创建敏感词检查器
func NewSensitiveWordChecker(db relation.SensitiveWordModelInterface, rdb redis.UniversalClient) *SensitiveWordChecker {
	checker := &SensitiveWordChecker{
		db:             db,
		rdb:            rdb,
		words:          make(map[string]bool),
		updateInterval: 5 * time.Minute, // 每5分钟更新一次
	}
	
	// 初始加载
	ctx := context.Background()
	if err := checker.reload(ctx); err != nil {
		log.ZError(ctx, "初始加载敏感词失败", err)
	}
	
	// 启动定时更新
	go checker.autoReload()
	
	// 订阅 Redis 刷新通知
	if rdb != nil {
		go checker.subscribeReloadNotification()
	}
	
	return checker
}

// reload 重新加载敏感词
func (c *SensitiveWordChecker) reload(ctx context.Context) error {
	words, err := c.db.FindAllEnabled(ctx)
	if err != nil {
		return err
	}
	
	newWords := make(map[string]bool, len(words))
	for _, word := range words {
		// 转换为小写以支持不区分大小写的匹配
		newWords[strings.ToLower(word.Word)] = true
	}
	
	c.mu.Lock()
	c.words = newWords
	c.lastUpdate = time.Now()
	c.mu.Unlock()
	
	log.ZInfo(ctx, "敏感词已更新", "count", len(newWords))
	return nil
}

// autoReload 自动重新加载敏感词
func (c *SensitiveWordChecker) autoReload() {
	ticker := time.NewTicker(c.updateInterval)
	defer ticker.Stop()
	
	for range ticker.C {
		ctx := context.Background()
		if err := c.reload(ctx); err != nil {
			log.ZError(ctx, "自动更新敏感词失败", err)
		}
	}
}

// Check 检查文本是否包含敏感词
func (c *SensitiveWordChecker) Check(text string) (bool, []string) {
	if text == "" {
		return false, nil
	}
	
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	lowerText := strings.ToLower(text)
	foundWords := make([]string, 0)
	
	for word := range c.words {
		if strings.Contains(lowerText, word) {
			foundWords = append(foundWords, word)
		}
	}
	
	return len(foundWords) > 0, foundWords
}

// ForceReload 强制重新加载敏感词，并通知所有服务刷新
func (c *SensitiveWordChecker) ForceReload(ctx context.Context) error {
	// 先刷新本地缓存
	if err := c.reload(ctx); err != nil {
		return err
	}
	
	// 通过 Redis 发布通知，让所有服务刷新
	if c.rdb != nil {
		if err := c.rdb.Publish(ctx, sensitiveWordReloadChannel, "reload").Err(); err != nil {
			log.ZWarn(ctx, "发布敏感词刷新通知失败", err)
		} else {
			log.ZInfo(ctx, "已发布敏感词刷新通知")
		}
	}
	
	return nil
}

// subscribeReloadNotification 订阅 Redis 刷新通知
func (c *SensitiveWordChecker) subscribeReloadNotification() {
	ctx := context.Background()
	pubsub := c.rdb.Subscribe(ctx, sensitiveWordReloadChannel)
	defer pubsub.Close()
	
	log.ZInfo(ctx, "开始监听敏感词刷新通知")
	
	ch := pubsub.Channel()
	for msg := range ch {
		log.ZInfo(ctx, "收到敏感词刷新通知", "payload", msg.Payload)
		if err := c.reload(ctx); err != nil {
			log.ZError(ctx, "响应刷新通知失败", err)
		}
	}
}
