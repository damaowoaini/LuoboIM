package cache

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"github.com/redis/go-redis/v9"
)

func TestGroupAuthorityInvalidatorOwnsFiniteClientWithoutChangingSharedOptions(t *testing.T) {
	base := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer func() {
		if err := base.Close(); err != nil {
			t.Error("base fixture client close", err)
		}
	}()
	invalidator, err := NewGroupAuthorityInvalidator(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := invalidator.Close(); err != nil {
			t.Error("bounded client close", err)
		}
	}()
	opts := invalidator.client.(*redis.Client).Options()
	if invalidator.client == base || !opts.ContextTimeoutEnabled || opts.DialTimeout != time.Second || opts.ReadTimeout != time.Second || opts.WriteTimeout != time.Second || opts.PoolTimeout != time.Second || opts.MaxRetries != 0 {
		t.Fatal("dedicated invalidator is not bounded")
	}
	if base.Options().ContextTimeoutEnabled {
		t.Fatal("shared client options mutated")
	}
	for _, key := range []string{"", "UID_PID_TOKEN_STATUS:synthetic", "GROUP_INFO:", "GROUP_INFO:g\x00bad"} {
		if err := invalidator.Apply(context.Background(), key); err == nil {
			t.Fatal("unrelated key was admitted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := invalidator.Apply(ctx, "GROUP_INFO:synthetic"); err == nil {
		t.Fatal("cancelled invalidation admitted")
	}
}

type foreignGroupRedisClient struct{ redis.UniversalClient }

func TestGroupAuthorityInvalidatorRebuildsOwnedPoolWithoutMovingSourceOrLifetime(t *testing.T) {
	newBase := func(t *testing.T, cluster bool) (*ownedRedisPool, context.Context, context.CancelFunc, *ownedsource.Observer) {
		t.Helper()
		parent, cancel := context.WithCancel(context.Background())
		owner := &ownedsource.Observer{}
		bound, err := ownedsource.Bind(parent, owner)
		if err != nil {
			t.Fatal(err)
		}
		var raw redis.UniversalClient
		var rebuild func() redis.UniversalClient
		if cluster {
			snapshot := redis.ClusterOptions{Addrs: []string{"127.0.0.1:1"}, MaxRetries: 3, MaxRedirects: 4}
			initial := snapshot
			initial.Addrs = append([]string(nil), snapshot.Addrs...)
			raw = redis.NewClusterClient(&initial)
			rebuild = func() redis.UniversalClient {
				opts := snapshot
				opts.Addrs = append([]string(nil), snapshot.Addrs...)
				opts.ContextTimeoutEnabled = true
				opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout, opts.PoolTimeout = time.Second, time.Second, time.Second, time.Second
				opts.MaxRetries, opts.MaxRedirects, opts.DialerRetries = -1, -1, 1
				return redis.NewClusterClient(&opts)
			}
		} else {
			snapshot := redis.Options{Addr: "127.0.0.1:1", MaxRetries: 3}
			initial := snapshot
			raw = redis.NewClient(&initial)
			rebuild = func() redis.UniversalClient {
				opts := snapshot
				opts.ContextTimeoutEnabled = true
				opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout, opts.PoolTimeout = time.Second, time.Second, time.Second, time.Second
				opts.MaxRetries, opts.DialerRetries = -1, 1
				return redis.NewClient(&opts)
			}
		}
		base, err := newOwnedRedisPool(bound, raw, rebuild)
		if err != nil {
			_ = raw.Close()
			cancel()
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = base.Close(); cancel() })
		return base, bound, cancel, owner
	}
	checkOwned := func(t *testing.T, base *ownedRedisPool) (*GroupAuthorityInvalidator, *ownedRedisPool) {
		t.Helper()
		invalidator, err := NewGroupAuthorityInvalidator(base)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := invalidator.Close(); err != nil {
				t.Error(err)
			}
		})
		recovery, ok := invalidator.client.(*ownedRedisPool)
		if !ok || recovery == base || recovery.UniversalClient == base.UniversalClient || recovery.source == base.source || recovery.source.owner != base.source.owner || recovery.parent != base.parent || recovery.source.lifetime == base.source.lifetime {
			t.Fatal("recovery did not keep an independent owned pool with the original observer")
		}
		return invalidator, recovery
	}
	t.Run("single-node-independent-options-and-close", func(t *testing.T) {
		base, parent, _, owner := newBase(t, false)
		invalidator, recovery := checkOwned(t, base)
		before := *base.UniversalClient.(*redis.Client).Options()
		opts := recovery.UniversalClient.(*redis.Client).Options()
		if !opts.ContextTimeoutEnabled || opts.DialTimeout != time.Second || opts.ReadTimeout != time.Second || opts.WriteTimeout != time.Second || opts.PoolTimeout != time.Second || opts.MaxRetries != 0 || opts.DialerRetries != 1 || opts.Addr != before.Addr || opts.DB != before.DB || opts.Username != before.Username || opts.Password != before.Password {
			t.Fatal("owned single-node recovery options differ")
		}
		if base.UniversalClient.(*redis.Client).Options().ContextTimeoutEnabled != before.ContextTimeoutEnabled || base.UniversalClient.(*redis.Client).Options().MaxRetries != before.MaxRetries {
			t.Fatal("shared options changed")
		}
		if err := invalidator.Close(); err != nil {
			t.Fatal(err)
		}
		if recovery.source.lifetime.Err() == nil || base.source.lifetime.Err() != nil || parent.Err() != nil || owner.Snapshot().Unconfirmed() {
			t.Fatal("recovery Close retired shared pool or invented unknown")
		}
	})
	t.Run("cluster-independent-options-and-close", func(t *testing.T) {
		base, parent, _, owner := newBase(t, true)
		invalidator, recovery := checkOwned(t, base)
		original := base.UniversalClient.(*redis.ClusterClient).Options()
		opts := recovery.UniversalClient.(*redis.ClusterClient).Options()
		if !opts.ContextTimeoutEnabled || opts.DialTimeout != time.Second || opts.ReadTimeout != time.Second || opts.WriteTimeout != time.Second || opts.PoolTimeout != time.Second || opts.MaxRetries != 0 || opts.MaxRedirects != 0 || opts.DialerRetries != 1 || len(opts.Addrs) != 1 || opts.Addrs[0] != original.Addrs[0] {
			t.Fatal("owned cluster recovery options differ")
		}
		opts.Addrs[0] = "synthetic-foreign:1"
		if original.Addrs[0] != "127.0.0.1:1" || original.MaxRetries != 3 || original.MaxRedirects != 4 {
			t.Fatal("shared cluster options aliased or changed")
		}
		if err := invalidator.Close(); err != nil {
			t.Fatal(err)
		}
		if recovery.source.lifetime.Err() == nil || base.source.lifetime.Err() != nil || parent.Err() != nil || owner.Snapshot().Unconfirmed() {
			t.Fatal("cluster Close moved source lifetime")
		}
	})
	t.Run("closing-base-keeps-existing-recovery", func(t *testing.T) {
		base, parent, _, owner := newBase(t, false)
		_, recovery := checkOwned(t, base)
		if err := base.Close(); err != nil {
			t.Fatal(err)
		}
		if base.source.lifetime.Err() == nil || recovery.source.lifetime.Err() != nil || parent.Err() != nil || owner.Snapshot().Unconfirmed() {
			t.Fatal("shared Close retired sibling recovery")
		}
		if next, err := NewGroupAuthorityInvalidator(base); !errors.Is(err, ErrOwnedRedis) || next != nil {
			t.Fatal("closed shared pool was rebuilt")
		}
	})
	for _, kind := range []string{"business-ack", "business-lost-ack"} {
		name := "actual-driver-ack-keeps-original-owner"
		if kind == "business-lost-ack" {
			name = "actual-driver-lost-ack-retires-only-recovery"
		}
		t.Run(name, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			original, later := &ownedsource.Observer{}, &ownedsource.Observer{}
			bound, err := ownedsource.Bind(parent, original)
			if err != nil {
				t.Fatal(err)
			}
			caller, err := ownedsource.Bind(parent, later)
			if err != nil {
				t.Fatal(err)
			}
			left, right := net.Pipe()
			wire := &ownedRedisSetupWire{parent: parent, left: left, right: right, done: make(chan error, 1)}
			rebuild := func() redis.UniversalClient {
				return redis.NewClient(&redis.Options{Addr: "127.0.0.1:36379", PoolSize: 1, MaxRetries: -1, DialerRetries: 1, ContextTimeoutEnabled: true, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolTimeout: time.Second, Dialer: func(ctx context.Context, _, _ string) (net.Conn, error) {
					if ctx.Err() != nil || wire.dials.Add(1) != 1 {
						return nil, errors.New("only original protocol connection allowed")
					}
					return left, nil
				}})
			}
			raw := rebuild()
			base, err := newOwnedRedisPool(bound, raw, rebuild)
			if err != nil {
				_ = raw.Close()
				_ = left.Close()
				_ = right.Close()
				t.Fatal(err)
			}
			defer base.Close()
			invalidator, recovery := checkOwned(t, base)
			go func() { wire.done <- wire.serve(kind) }()
			t.Cleanup(func() {
				_ = invalidator.Close()
				_ = left.Close()
				_ = right.Close()
				if err := <-wire.done; err != nil {
					t.Error("actual driver protocol peer failed", err)
				}
			})
			if kind == "business-ack" {
				if err := base.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := invalidator.CheckReady(caller); err != nil {
				t.Fatal("owned recovery setup failed", err)
			}
			if wire.maintenance.Load() != 1 || wire.identity.Load() != 2 || original.Snapshot().RedisMutation.Started != 0 {
				t.Fatal("driver setup became business mutation")
			}
			value, err := invalidator.client.Incr(caller, "synthetic-setup-sequence").Result()
			counts := original.Snapshot().RedisMutation
			if wire.increments.Load() != 1 || wire.dials.Load() != 1 || recovery.source.writes.Load() == 0 || base.source.writes.Load() != 0 || counts.Started != 1 || counts.InFlight != 0 || later.Snapshot().RedisMutation.Started != 0 || parent.Err() != nil || (base.source.lifetime.Err() != nil) != (kind == "business-ack") {
				t.Fatal("physical write or original source ownership moved")
			}
			if kind == "business-ack" {
				if err != nil || value != 1 || counts.Succeeded != 1 || original.Snapshot().Unconfirmed() || recovery.source.lifetime.Err() != nil {
					t.Fatal("original recovery ACK changed", err)
				}
			} else {
				if !errors.Is(err, ErrOwnedRedis) || !counts.Unknown || counts.Unconfirmed != 1 || recovery.source.lifetime.Err() == nil {
					t.Fatal("lost ACK did not retire its own recovery pool", err)
				}
				if err := invalidator.client.Incr(caller, "synthetic-setup-sequence").Err(); !errors.Is(err, ErrOwnedRedis) || wire.increments.Load() != 1 || wire.dials.Load() != 1 || original.Snapshot().RedisMutation.Started != 1 {
					t.Fatal("retired recovery replayed or reconnected")
				}
				original.Attempt(ownedsource.RedisMutation)(false)
				if !original.Snapshot().Unconfirmed() || later.Snapshot().RedisMutation.Started != 0 {
					t.Fatal("later ACK or caller cleared the original unknown")
				}
			}
		})
	}
	t.Run("parent-cancel-refuses-rebuild", func(t *testing.T) {
		base, parent, cancel, owner := newBase(t, false)
		invalidator, recovery := checkOwned(t, base)
		cancel()
		if parent.Err() == nil || base.source.lifetime.Err() == nil || recovery.source.lifetime.Err() == nil {
			t.Fatal("original parent did not cancel both pools")
		}
		if err := invalidator.CheckReady(context.Background()); !errors.Is(err, ErrOwnedRedis) {
			t.Fatal("cancelled parent entered driver I/O")
		}
		if next, err := NewGroupAuthorityInvalidator(base); !errors.Is(err, ErrOwnedRedis) || next != nil || owner.Snapshot().Unconfirmed() {
			t.Fatal("cancelled parent rebuilt or invented unknown")
		}
	})
	t.Run("foreign-and-malformed-owned-clients-refused", func(t *testing.T) {
		base, parent, _, _ := newBase(t, false)
		var nilOwned *ownedRedisPool
		var nilPlain *redis.Client
		var nilCluster *redis.ClusterClient
		badOwner := *base
		badOwner.source = &ownedRedisSource{owner: &ownedsource.Observer{}, lifetime: parent}
		badParent := *base
		badParent.parent = context.Background()
		badClient := *base
		badClient.UniversalClient = &foreignGroupRedisClient{base.UniversalClient}
		for _, client := range []redis.UniversalClient{nil, nilOwned, nilPlain, nilCluster, &foreignGroupRedisClient{base.UniversalClient}, &ownedRedisPool{}, &badOwner, &badParent, &badClient} {
			if got, err := NewGroupAuthorityInvalidator(client); err == nil || got != nil {
				t.Fatal("foreign or malformed client admitted")
			}
		}
		if got, err := newOwnedRedisPool(parent, &foreignGroupRedisClient{base.UniversalClient}, base.rebuild); !errors.Is(err, ErrOwnedRedis) || got != nil {
			t.Fatal("factory admitted arbitrary UniversalClient wrapper")
		}
	})
}
