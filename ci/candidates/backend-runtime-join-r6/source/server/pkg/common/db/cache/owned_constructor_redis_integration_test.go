package cache

import (
	"context"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"github.com/redis/go-redis/v9"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActualOwnedRedisProductionConstructorsBindOriginalObserverAndRetireWrites(t *testing.T) {
	socket, address := os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET"), os.Getenv("LUOBO_BOOKING_TEST_REDIS_ADDR")
	if socket == "" || address == "" {
		t.Skip("requires explicitly prepared existing task-private Redis fixture")
	}
	if address != "127.0.0.1:36379" || !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || !strings.HasSuffix(socket, "/references/integration/redis8.2/redis.sock") {
		t.Fatal("only exact CI loopback Redis fixture allowed")
	}
	info, err := os.Lstat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatal("existing owned Redis socket required")
	}
	previous := config.Config.Redis
	t.Cleanup(func() { config.Config.Redis = previous })
	config.Config.Redis.Address = []string{address}
	config.Config.Redis.ClusterMode = false
	config.Config.Redis.Username = ""
	config.Config.Redis.Password = ""
	t.Setenv("REDIS_ADDRESS", address)
	t.Setenv("REDIS_PORT", "")
	t.Setenv("REDIS_USERNAME", "")
	t.Setenv("REDIS_PASSWORD", "")
	verifier := redis.NewClient(&redis.Options{Addr: address, DB: 0, MaxRetries: -1, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	keys := []string{}
	prefix := "luobo-owned-redis-constructor:" + uuid.NewString() + ":"
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if len(keys) > 0 {
			if err := verifier.Del(ctx, keys...).Err(); err != nil {
				t.Error("exact owned keys cleanup failed", err)
			}
		}
		_ = verifier.Close()
	})
	for _, route := range []string{"process-owned", "group-owned", "group-unbound-default"} {
		t.Run(route, func(t *testing.T) {
			key := prefix + route
			check, finishCheck := context.WithTimeout(context.Background(), 3*time.Second)
			defer finishCheck()
			if n, err := verifier.Exists(check, key).Result(); err != nil || n != 0 {
				t.Fatal("fresh exact synthetic key required", err)
			}
			keys = append(keys, key)
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := &ownedmongo.Observer{}
			current := parent
			if route != "group-unbound-default" {
				current, err = ownedmongo.Bind(parent, original)
				if err != nil {
					t.Fatal(err)
				}
			}
			var client redis.UniversalClient
			if route == "process-owned" {
				client, err = NewProcessRuntimeRedis(current)
			} else {
				client, err = NewGroupRuntimeRedis(current)
			}
			if err != nil || client == nil {
				t.Fatal("actual production constructor failed", err)
			}
			defer client.Close()
			_, wrapped := client.(*ownedRedisPool)
			if wrapped != (route != "group-unbound-default") {
				t.Fatal("actual constructor routed around ownership hook or changed default path")
			}
			if n, err := client.Incr(current, key).Result(); err != nil || n != 1 {
				t.Fatal("actual INCR failed", err)
			}
			if value, err := client.Get(current, key).Result(); err != nil || value != "1" {
				t.Fatal("actual GET failed", err)
			}
			before := original.Snapshot().External.RedisMutation
			if route == "group-unbound-default" {
				if before.Started != 0 {
					t.Fatal("default unbound pool unexpectedly adopted source")
				}
				return
			}
			if before.Started != 1 || before.Succeeded != 1 || before.Unknown || original.Snapshot().SourceOutcomeUnconfirmed {
				t.Fatal("actual constructor did not observe original INCR exactly")
			}
			cancel()
			if err := client.Incr(context.Background(), key).Err(); err == nil {
				t.Fatal("later caller wrote after original owner retirement")
			}
			after := original.Snapshot().External.RedisMutation
			if after.Started != before.Started || after.Unconfirmed != before.Unconfirmed {
				t.Fatal("pre-I/O canceled call invented new mutation/lost ACK")
			}
			read, done := context.WithTimeout(context.Background(), 3*time.Second)
			defer done()
			if value, err := verifier.Get(read, key).Result(); err != nil || value != "1" {
				t.Fatal("canceled original pool performed another real write", err)
			}
		})
	}
}
