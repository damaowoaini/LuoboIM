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
		var original error
		observed := h.run(ctx, ownedRedisBusinessWrite(cmd), func(current context.Context) error {
			original = next(current, cmd)
			// A direct driver Null reply is a complete acknowledged result. In
			// particular rockscache luaSet deliberately returns Lua nil after writes.
			// Preserve that exact sentinel for its original caller; joined/wrapped Nil
			// and all other errors remain subject to the original unknown-write fence.
			if original == redis.Nil {
				return nil
			}
			return original
		})
		if observed != nil {
			return observed
		}
		return original
	}
}
func (h *ownedRedisSource) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		write := false
		for _, cmd := range cmds {
			write = write || ownedRedisBusinessWrite(cmd)
		}
		var original error
		observed := h.run(ctx, write, func(current context.Context) error {
			original = next(current, cmds)
			onlyAcknowledgedNull := original == redis.Nil && len(cmds) > 0
			sawNull := false
			for _, cmd := range cmds {
				issue := cmd.Err()
				if issue == redis.Nil {
					sawNull = true
					continue
				}
				if issue != nil {
					onlyAcknowledgedNull = false
					var reply redis.Error
					if !errors.As(issue, &reply) {
						return ErrOwnedRedis
					}
				}
			}
			// The pinned driver reads/stamps every result before returning a reply
			// error. An outer first-Null cannot hide a later Lua/Redis/transport error.
			if onlyAcknowledgedNull && sawNull {
				return nil
			}
			return original
		})
		if observed != nil {
			return observed
		}
		return original
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
	var rebuild func() redis.UniversalClient
	if len(config.Config.Redis.Address) > 1 || config.Config.Redis.ClusterMode {
		snapshot := redis.ClusterOptions{Addrs: append([]string(nil), config.Config.Redis.Address...), Username: config.Config.Redis.Username, Password: config.Config.Redis.Password, PoolSize: 50, MaxRetries: -1, MaxRedirects: -1, ContextTimeoutEnabled: true, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolTimeout: time.Second, DialerRetries: 1}
		rebuild = func() redis.UniversalClient {
			opts := snapshot
			opts.Addrs = append([]string(nil), snapshot.Addrs...)
			return redis.NewClusterClient(&opts)
		}
	} else {
		snapshot := redis.Options{Addr: config.Config.Redis.Address[0], Username: config.Config.Redis.Username, Password: config.Config.Redis.Password, DB: 0, PoolSize: 100, MaxRetries: -1, ContextTimeoutEnabled: true, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolTimeout: time.Second, DialerRetries: 1}
		rebuild = func() redis.UniversalClient { opts := snapshot; return redis.NewClient(&opts) }
	}
	client := rebuild()
	pool, err := newOwnedRedisPool(parent, client, rebuild)
	if err != nil {
		return nil, errors.Join(err, client.Close())
	}
	current, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	if err := client.Ping(current).Err(); err != nil {
		return nil, errors.Join(ErrOwnedRedis, pool.Close())
	}
	return pool, nil
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
	cancel  context.CancelFunc
	parent  context.Context
	source  *ownedRedisSource
	rebuild func() redis.UniversalClient
}

func (p *ownedRedisPool) Close() error { p.cancel(); return p.UniversalClient.Close() }

// Only freshly constructed concrete driver clients enter this factory. The
// original parent and observer remain private; a later request cannot rebind
// ownership. Every pool receives its own cancellation and physical-write hook.
func newOwnedRedisPool(parent context.Context, client redis.UniversalClient, rebuild func() redis.UniversalClient) (*ownedRedisPool, error) {
	owner := ownedsource.FromContext(parent)
	if parent == nil || parent.Done() == nil || parent.Err() != nil || owner == nil || rebuild == nil {
		return nil, ErrOwnedRedis
	}
	switch actual := client.(type) {
	case *redis.Client:
		if actual == nil {
			return nil, ErrOwnedRedis
		}
	case *redis.ClusterClient:
		if actual == nil {
			return nil, ErrOwnedRedis
		}
	default:
		return nil, ErrOwnedRedis
	}
	poolCtx, cancelPool := context.WithCancel(parent)
	hook := &ownedRedisSource{owner: owner, lifetime: poolCtx, cancelPool: cancelPool}
	if cluster, ok := client.(*redis.ClusterClient); ok {
		// Cluster node sockets bypass the outer DialHook. Observe physical writes
		// on each node without adding another business-command observer.
		cluster.OnNewNode(func(node *redis.Client) { node.AddHook(ownedRedisDialOnly{hook}) })
	}
	client.AddHook(hook)
	return &ownedRedisPool{UniversalClient: client, cancel: cancelPool, parent: parent, source: hook, rebuild: rebuild}, nil
}

// Rebuild only the package's actual owned pool. Recovery is a sibling of the
// shared pool, with the same original source owner and exact driver options;
// closing or retiring either pool never cancels the other or its parent.
func (p *ownedRedisPool) newGroupInvalidationPool() (*ownedRedisPool, error) {
	if p == nil || p.parent == nil || p.parent.Done() == nil || p.parent.Err() != nil || p.cancel == nil || p.rebuild == nil || p.source == nil || p.source.owner == nil || p.source.owner != ownedsource.FromContext(p.parent) || p.source.lifetime == nil || p.source.lifetime.Err() != nil {
		return nil, ErrOwnedRedis
	}
	// This factory captured the original source configuration before driver
	// initialization. It cannot inherit a Dialer closed over the old pool or
	// connect to a later environment-selected source.
	switch original := p.UniversalClient.(type) {
	case *redis.Client:
		if original == nil {
			return nil, ErrOwnedRedis
		}
	case *redis.ClusterClient:
		if original == nil {
			return nil, ErrOwnedRedis
		}
	default:
		return nil, ErrOwnedRedis
	}
	client := p.rebuild()
	valid := false
	switch original := p.UniversalClient.(type) {
	case *redis.Client:
		fresh, ok := client.(*redis.Client)
		valid = ok && original != nil && fresh != nil && fresh != original
	case *redis.ClusterClient:
		fresh, ok := client.(*redis.ClusterClient)
		valid = ok && original != nil && fresh != nil && fresh != original
	}
	if !valid {
		// Never close an aliased original or an unknown wrapper supplied by a
		// malformed private factory. A distinct concrete driver client is ours.
		switch fresh := client.(type) {
		case *redis.Client:
			if fresh != nil && client != p.UniversalClient {
				return nil, errors.Join(ErrOwnedRedis, fresh.Close())
			}
		case *redis.ClusterClient:
			if fresh != nil && client != p.UniversalClient {
				return nil, errors.Join(ErrOwnedRedis, fresh.Close())
			}
		}
		return nil, ErrOwnedRedis
	}
	pool, err := newOwnedRedisPool(p.parent, client, p.rebuild)
	if err != nil {
		return nil, errors.Join(err, client.Close())
	}
	if p.source.lifetime.Err() != nil || p.parent.Err() != nil {
		return nil, errors.Join(ErrOwnedRedis, pool.Close())
	}
	return pool, nil
}
