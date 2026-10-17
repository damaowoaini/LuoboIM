package cache

import (
	"context"
	"errors"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"github.com/redis/go-redis/v9"
	"net"
	"strings"
	"sync/atomic"
	"time"
)

var ErrOwnedRedis = errors.New("original owned Redis source is unconfirmed")

// Never records command args, key, payload, endpoint, reply or error text.
// A physical Write begun during a failed command is uncertain. Concurrent pool
// traffic can conservatively add uncertainty; it can never erase uncertainty.
// Local refusal/dial failure with no physical write does not claim a lost ACK.
type ownedRedisSource struct {
	owner      *ownedsource.Observer
	lifetime   context.Context
	writes     atomic.Uint64
	cancelPool context.CancelFunc
}
type ownedRedisConn struct {
	net.Conn
	source *ownedRedisSource
}

func (c *ownedRedisConn) Write(body []byte) (int, error) {
	if c.source.writes.Add(1) == 0 {
		c.source.owner.Attempt(ownedsource.RedisMutation)(true)
	}
	return c.Conn.Write(body)
}
func (h *ownedRedisSource) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if h.lifetime.Err() != nil {
			return nil, ErrOwnedRedis
		}
		current, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(h.lifetime, cancel)
		defer stop()
		conn, err := next(current, network, addr)
		if err != nil || conn == nil {
			return conn, err
		}
		return &ownedRedisConn{conn, h}, nil
	}
}
func ownedRedisReadOnly(name string) bool {
	switch strings.ToLower(name) {
	case "get", "mget", "hget", "hmget", "hgetall", "hkeys", "hlen", "exists", "ttl", "pttl", "llen", "lrange", "scard", "smembers", "sismember", "zrange", "zrevrange", "zrangebyscore", "zcard", "zscore", "scan", "sscan", "hscan", "zscan", "ping", "hello", "auth", "select", "readonly":
		return true
	}
	return false
}
func (h *ownedRedisSource) run(ctx context.Context, write bool, next func(context.Context) error) error {
	if ctx == nil || ctx.Err() != nil || h.lifetime.Err() != nil {
		return ErrOwnedRedis
	}
	current, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(h.lifetime, cancel)
	defer stop()
	if !write {
		return next(current)
	}
	before := h.writes.Load()
	finish := h.owner.Attempt(ownedsource.RedisMutation)
	err := next(current)
	// Even a Redis error can follow a partially applied Lua/transaction pipeline.
	// A reply alone does not prove rollback. No automatic replay or fresh ACK
	// can clear an original failed write whose actual outcome needs inspection.
	unknown := err != nil && h.writes.Load() != before
	finish(unknown)
	if unknown {
		if h.cancelPool != nil {
			h.cancelPool()
		}
		return ErrOwnedRedis
	}
	return err
}
func (h *ownedRedisSource) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		return h.run(ctx, !ownedRedisReadOnly(cmd.Name()), func(ctx context.Context) error { return next(ctx, cmd) })
	}
}
func (h *ownedRedisSource) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		write := false
		for _, cmd := range cmds {
			write = write || !ownedRedisReadOnly(cmd.Name())
		}
		return h.run(ctx, write, func(ctx context.Context) error {
			err := next(ctx, cmds)
			for _, cmd := range cmds {
				if issue := cmd.Err(); issue != nil {
					var reply redis.Error
					if !errors.As(issue, &reply) && !errors.Is(issue, redis.Nil) {
						return ErrOwnedRedis
					}
				}
			}
			return err
		})
	}
}

// Independent source-owned pool. Retry/redirect after a possibly committed INCR
// or pipeline is disabled. The package singleton/default path remains intact.
func NewOwnedSourceRedis(parent context.Context) (redis.UniversalClient, error) {
	owner := ownedsource.FromContext(parent)
	if parent == nil || parent.Done() == nil || parent.Err() != nil || owner == nil {
		return nil, ErrOwnedRedis
	}
	overrideConfigFromEnv()
	if len(config.Config.Redis.Address) == 0 {
		return nil, ErrOwnedRedis
	}
	poolCtx, cancelPool := context.WithCancel(parent)
	hook := &ownedRedisSource{owner: owner, lifetime: poolCtx, cancelPool: cancelPool}
	var client redis.UniversalClient
	if len(config.Config.Redis.Address) > 1 || config.Config.Redis.ClusterMode {
		cluster := redis.NewClusterClient(&redis.ClusterOptions{Addrs: append([]string{}, config.Config.Redis.Address...), Username: config.Config.Redis.Username, Password: config.Config.Redis.Password, PoolSize: 50, MaxRetries: -1, MaxRedirects: -1, ContextTimeoutEnabled: true, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolTimeout: time.Second, DialerRetries: 1})
		// Cluster node sockets do not pass the outer client's DialHook. Install only
		// the physical write hook on each node, not a second command observer.
		cluster.OnNewNode(func(node *redis.Client) { node.AddHook(ownedRedisDialOnly{hook}) })
		client = cluster
	} else {
		client = redis.NewClient(&redis.Options{Addr: config.Config.Redis.Address[0], Username: config.Config.Redis.Username, Password: config.Config.Redis.Password, DB: 0, PoolSize: 100, MaxRetries: -1, ContextTimeoutEnabled: true, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolTimeout: time.Second, DialerRetries: 1})
	}
	client.AddHook(hook)
	current, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	if err := client.Ping(current).Err(); err != nil {
		cancelPool()
		return nil, errors.Join(ErrOwnedRedis, client.Close())
	}
	return &ownedRedisPool{UniversalClient: client, cancel: cancelPool}, nil
}

type ownedRedisDialOnly struct{ source *ownedRedisSource }

func (h ownedRedisDialOnly) DialHook(n redis.DialHook) redis.DialHook          { return h.source.DialHook(n) }
func (h ownedRedisDialOnly) ProcessHook(n redis.ProcessHook) redis.ProcessHook { return n }
func (h ownedRedisDialOnly) ProcessPipelineHook(n redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return n
}

// Only the actual owned pool is retired. Acknowledged state is not replayed,
// nor is any shared/default singleton or another owner closed/canceled.
type ownedRedisPool struct {
	redis.UniversalClient
	cancel context.CancelFunc
}

func (p *ownedRedisPool) Close() error { p.cancel(); return p.UniversalClient.Close() }
