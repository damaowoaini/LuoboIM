// Copyright © 2023 OpenIM open source community. All rights reserved.
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

package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/redis/go-redis/v9"
)

const dailyFriendApplyKeyPrefix = "CHAT:DAILY_FRIEND_APPLY:"

type FriendAddLimitInterface interface {
	// AddDailyApplyTarget 记录今日向 toUserID 发起的加好友申请。
	// 同一目标用户重复申请不计入新增；若超出 limit 则 limited=true。
	AddDailyApplyTarget(ctx context.Context, fromUserID, toUserID string, limit int64) (limited bool, err error)
}

type FriendAddLimitRedis struct {
	rdb redis.UniversalClient
}

func NewFriendAddLimitInterface(rdb redis.UniversalClient) *FriendAddLimitRedis {
	return &FriendAddLimitRedis{rdb: rdb}
}

func dailyFriendApplyKey(userID string) string {
	return fmt.Sprintf("%s%s:%s", dailyFriendApplyKeyPrefix, userID, time.Now().Format("2006-01-02"))
}

func secondsUntilEndOfDay() time.Duration {
	now := time.Now()
	end := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	return end.Sub(now)
}

func (f *FriendAddLimitRedis) AddDailyApplyTarget(ctx context.Context, fromUserID, toUserID string, limit int64) (bool, error) {
	key := dailyFriendApplyKey(fromUserID)
	added, err := f.rdb.SAdd(ctx, key, toUserID).Result()
	if err != nil {
		return false, errs.Wrap(err)
	}
	if added == 0 {
		return false, nil
	}
	if err := f.rdb.Expire(ctx, key, secondsUntilEndOfDay()).Err(); err != nil {
		return false, errs.Wrap(err)
	}
	count, err := f.rdb.SCard(ctx, key).Result()
	if err != nil {
		return false, errs.Wrap(err)
	}
	if count > limit {
		if remErr := f.rdb.SRem(ctx, key, toUserID).Err(); remErr != nil {
			return true, errs.Wrap(remErr)
		}
		return true, nil
	}
	return false, nil
}
