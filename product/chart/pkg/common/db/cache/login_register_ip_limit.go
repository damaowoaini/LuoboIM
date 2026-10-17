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

const hourlyLoginRegisterIPKeyPrefix = "CHAT:HOURLY_LOGIN_REGISTER_IP:"

// LoginRegisterIPLimitInterface 同一 IP 每小时登录/注册相关操作次数限制。
type LoginRegisterIPLimitInterface interface {
	// CheckAndIncr 计数 +1；若已超过 limit 则 limited=true 且不增加计数。
	CheckAndIncr(ctx context.Context, ip string, limit int64) (limited bool, err error)
	// Incr 无条件 +1（登录失败等场景）。
	Incr(ctx context.Context, ip string) error
	// GetCount 获取当前小时已用次数。
	GetCount(ctx context.Context, ip string) (int64, error)
	// Decr 操作失败时回滚当前小时计数。
	Decr(ctx context.Context, ip string) error
}

type LoginRegisterIPLimitRedis struct {
	rdb redis.UniversalClient
}

func NewLoginRegisterIPLimitInterface(rdb redis.UniversalClient) *LoginRegisterIPLimitRedis {
	return &LoginRegisterIPLimitRedis{rdb: rdb}
}

func hourlyLoginRegisterIPKey(ip string) string {
	return fmt.Sprintf("%s%s:%s", hourlyLoginRegisterIPKeyPrefix, ip, time.Now().Format("2006-01-02-15"))
}

func secondsUntilEndOfHour() time.Duration {
	now := time.Now()
	end := time.Date(now.Year(), now.Month(), now.Day(), now.Hour()+1, 0, 0, 0, now.Location())
	return end.Sub(now)
}

func (v *LoginRegisterIPLimitRedis) CheckAndIncr(ctx context.Context, ip string, limit int64) (bool, error) {
	key := hourlyLoginRegisterIPKey(ip)
	count, err := v.rdb.Incr(ctx, key).Result()
	if err != nil {
		return false, errs.Wrap(err)
	}
	if count == 1 {
		if err := v.rdb.Expire(ctx, key, secondsUntilEndOfHour()).Err(); err != nil {
			return false, errs.Wrap(err)
		}
	}
	if count > limit {
		if _, decrErr := v.rdb.Decr(ctx, key).Result(); decrErr != nil {
			return true, errs.Wrap(decrErr)
		}
		return true, nil
	}
	return false, nil
}

func (v *LoginRegisterIPLimitRedis) Incr(ctx context.Context, ip string) error {
	key := hourlyLoginRegisterIPKey(ip)
	count, err := v.rdb.Incr(ctx, key).Result()
	if err != nil {
		return errs.Wrap(err)
	}
	if count == 1 {
		if err := v.rdb.Expire(ctx, key, secondsUntilEndOfHour()).Err(); err != nil {
			return errs.Wrap(err)
		}
	}
	return nil
}

func (v *LoginRegisterIPLimitRedis) GetCount(ctx context.Context, ip string) (int64, error) {
	count, err := v.rdb.Get(ctx, hourlyLoginRegisterIPKey(ip)).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, errs.Wrap(err)
	}
	return count, nil
}

func (v *LoginRegisterIPLimitRedis) Decr(ctx context.Context, ip string) error {
	if err := v.rdb.Decr(ctx, hourlyLoginRegisterIPKey(ip)).Err(); err != nil {
		return errs.Wrap(err)
	}
	return nil
}
