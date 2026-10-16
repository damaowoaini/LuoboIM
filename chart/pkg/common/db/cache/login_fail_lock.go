package cache

import (
	"context"
	"time"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/redis/go-redis/v9"
)

const loginFailKeyPrefix = "CHAT:LOGIN_FAIL:"

// LoginFailLockInterface 登录失败计数（固定窗口，首次失败起算）。
type LoginFailLockInterface interface {
	Count(ctx context.Context, key string) (int64, error)
	Incr(ctx context.Context, key string, window time.Duration) (int64, error)
	Reset(ctx context.Context, keys ...string) error
}

type LoginFailLockRedis struct {
	rdb redis.UniversalClient
}

func NewLoginFailLock(rdb redis.UniversalClient) *LoginFailLockRedis {
	return &LoginFailLockRedis{rdb: rdb}
}

func (l *LoginFailLockRedis) Count(ctx context.Context, key string) (int64, error) {
	n, err := l.rdb.Get(ctx, loginFailKeyPrefix+key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, errs.Wrap(err)
	}
	return n, nil
}

func (l *LoginFailLockRedis) Incr(ctx context.Context, key string, window time.Duration) (int64, error) {
	k := loginFailKeyPrefix + key
	n, err := l.rdb.Incr(ctx, k).Result()
	if err != nil {
		return 0, errs.Wrap(err)
	}
	if n == 1 {
		if err := l.rdb.Expire(ctx, k, window).Err(); err != nil {
			return n, errs.Wrap(err)
		}
	}
	return n, nil
}

func (l *LoginFailLockRedis) Reset(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	full := make([]string, len(keys))
	for i, k := range keys {
		full[i] = loginFailKeyPrefix + k
	}
	return errs.Wrap(l.rdb.Del(ctx, full...).Err())
}
