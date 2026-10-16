package cache

import (
	"context"
	"errors"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/redis/go-redis/v9"
)

// NewGroupRuntimeRedis is owned by the managed group process, not the legacy
// package singleton. The same existing config/environment is used, but every
// socket/pool/retry and the initial ping has a real finite lifecycle budget.
func NewGroupRuntimeRedis(ctx context.Context) (redis.UniversalClient, error) {
	if ctx == nil || ctx.Done() == nil {
		return nil, errors.New("managed group cache context required")
	}
	overrideConfigFromEnv()
	if len(config.Config.Redis.Address) == 0 {
		return nil, errors.New("group cache address missing")
	}
	var client redis.UniversalClient
	if len(config.Config.Redis.Address) > 1 || config.Config.Redis.ClusterMode {
		client = redis.NewClusterClient(&redis.ClusterOptions{Addrs: append([]string(nil), config.Config.Redis.Address...), Username: config.Config.Redis.Username, Password: config.Config.Redis.Password, PoolSize: 50, MaxRetries: -1, MaxRedirects: -1, ContextTimeoutEnabled: true, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolTimeout: time.Second})
	} else {
		client = redis.NewClient(&redis.Options{Addr: config.Config.Redis.Address[0], Username: config.Config.Redis.Username, Password: config.Config.Redis.Password, DB: 0, PoolSize: 100, MaxRetries: -1, ContextTimeoutEnabled: true, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolTimeout: time.Second})
	}
	current, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := client.Ping(current).Err(); err != nil {
		_ = client.Close()
		return nil, errors.New("group cache initialization failed")
	}
	return client, nil
}
