package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestGroupAuthorityInvalidationUsesActualRockscacheTagAndExactOwnedCleanup(t *testing.T) {
	raw := os.Getenv("LUOBO_RETENTION_TEST_REDIS")
	if raw == "" {
		t.Skip("explicit task Redis fixture not selected")
	}
	if raw != "redis://127.0.0.1:36379/15" {
		t.Fatal("exact task Redis fixture required")
	}
	opts, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatal("task Redis URI invalid")
	}
	opts.ContextTimeoutEnabled = true
	opts.DialTimeout = time.Second
	opts.ReadTimeout = time.Second
	opts.WriteTimeout = time.Second
	opts.MaxRetries = -1
	client := redis.NewClient(opts)
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error("task Redis close failed", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal("task nonce failed")
	}
	claim := hex.EncodeToString(random[:])
	gid := "luobo_group_invalidation_" + hex.EncodeToString(random[:12])
	keys := []string{"luobo_group_invalidation_claim_" + gid, "GROUP_INFO:" + gid}
	ack, err := client.Eval(ctx, `if redis.call('EXISTS',KEYS[1],KEYS[2])~=0 then return 0 end
redis.call('SET',KEYS[1],ARGV[1]);redis.call('HSET',KEYS[2],'value',ARGV[1],'lockOwner',ARGV[1]);return 1`, keys, claim).Int64()
	if err != nil || ack != 1 {
		t.Fatal("exact task keys ownership unconfirmed; no cleanup authorized")
	}
	certain := true
	t.Cleanup(func() {
		if !certain {
			t.Error("task Redis mutation ACK unknown; retain owned keys for inspection")
			return
		}
		end, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		n, err := client.Eval(end, `if redis.call('TYPE',KEYS[1]).ok~='string' or redis.call('GET',KEYS[1])~=ARGV[1] then return -1 end
if redis.call('EXISTS',KEYS[2])==1 then
 if redis.call('TYPE',KEYS[2]).ok~='hash' or redis.call('HLEN',KEYS[2])~=2 or redis.call('HGET',KEYS[2],'value')~=ARGV[1] then return -2 end
 local before=redis.call('HGET',KEYS[2],'lockOwner')==ARGV[1] and not redis.call('HGET',KEYS[2],'lockUntil')
 local after=redis.call('HGET',KEYS[2],'lockUntil')=='0' and not redis.call('HGET',KEYS[2],'lockOwner')
 if not before and not after then return -3 end
end
return redis.call('DEL',KEYS[1],KEYS[2])`, keys, claim).Int64()
		if err != nil || n < 1 || n > 2 {
			t.Error("exact claimed cache keys cleanup unconfirmed", err)
			return
		}
		left, err := client.Exists(end, keys...).Result()
		if err != nil || left != 0 {
			t.Error("owned keys absence unconfirmed", err)
		}
	})
	invalidator, err := NewGroupAuthorityInvalidator(client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := invalidator.Close(); err != nil {
			t.Error("owned invalidator close failed", err)
		}
	})
	if err := invalidator.CheckReady(ctx); err != nil {
		t.Fatal("bounded recovery Redis is not ready", err)
	}
	if err := invalidator.Apply(ctx, keys[1]); err != nil {
		certain = false
		t.Fatal("cache invalidation ACK unknown", err)
	}
	fields, err := client.HGetAll(ctx, keys[1]).Result()
	if err != nil {
		t.Fatal("cache readback failed", err)
	}
	if len(fields) != 2 || fields["value"] != claim || fields["lockUntil"] != "0" {
		t.Fatal("actual rockscache tag-delete contract changed")
	}
	ttl, err := client.TTL(ctx, keys[1]).Result()
	if err != nil || ttl <= 0 {
		t.Fatal("tagged cache did not get finite expiry", err)
	}
	// Idempotent tag-delete leaves the exact claimed payload and no lock owner.
	if err := invalidator.Apply(ctx, keys[1]); err != nil {
		certain = false
		t.Fatal("repeat invalidation ACK unknown", err)
	}
	if v, err := client.HGet(ctx, keys[1], "value").Result(); err != nil || v != claim {
		t.Fatal("repeat invalidation replaced cached payload", err)
	}
}
