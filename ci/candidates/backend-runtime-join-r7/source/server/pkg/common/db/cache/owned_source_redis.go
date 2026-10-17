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

// go-redis v9.23.0 reuses the original hooks for initConn. These exact
// commands only configure metadata/notification state on that connection;
// they do not mutate application keys. In particular, Redis8.2 rejects the
// optional maintenance subcommand and the driver must retain its own fallback.
// Do NOT exempt the CLIENT namespace: KILL/PAUSE/unknown shapes remain observed.
func ownedRedisConnectionSetup(cmd redis.Cmder) bool {
	args := cmd.Args()
	word := func(index int, expected string) bool {
		if index >= len(args) {
			return false
		}
		value, ok := args[index].(string)
		return ok && strings.EqualFold(value, expected)
	}
	if !word(0, "client") {
		return false
	}
	if len(args) == 4 && word(1, "setinfo") && (word(2, "lib-name") || word(2, "lib-ver")) {
		value, ok := args[3].(string)
		return ok && len(value) > 0 && len(value) <= 1024 && strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == 127 }) < 0
	}
	if len(args) == 5 && word(1, "maint_notifications") && word(2, "on") && word(3, "moving-endpoint-type") {
		for _, endpoint := range []string{"auto", "internal-ip", "internal-fqdn", "external-ip", "external-fqdn", "none"} {
			if word(4, endpoint) {
				return true
			}
		}
	}
	return false
}
func ownedRedisBusinessWrite(cmd redis.Cmder) bool {
	return !ownedRedisReadOnly(cmd.Name()) && !ownedRedisConnectionSetup(cmd)
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
		return h.run(ctx, ownedRedisBusinessWrite(cmd), func(ctx context.Context) error { return next(ctx, cmd) })
	}
}
func (h *ownedRedisSource) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		write := false
		for _, cmd := range cmds {
			write = write || ownedRedisBusinessWrite(cmd)
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
