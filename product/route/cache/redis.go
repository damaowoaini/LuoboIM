package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

var RedisClient *redis.Client

type RedisConfig struct {
	Address  string
	Password string
	DB       int
}

// InitRedis 初始化 Redis 连接
func InitRedis(config RedisConfig) error {
	RedisClient = redis.NewClient(&redis.Options{
		Addr:     config.Address,
		Password: config.Password,
		DB:       config.DB,
	})

	// 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := RedisClient.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("failed to connect redis: %w", err)
	}

	return nil
}

// Public OTPs belong exclusively to the tenant chart service.

// Close 关闭 Redis 连接
func Close() error {
	if RedisClient != nil {
		return RedisClient.Close()
	}
	return nil
}
