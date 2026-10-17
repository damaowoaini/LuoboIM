package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"math"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestVisibilityFloorRejectsNegativeBatchBeforeAnyRedisDispatch(t *testing.T) {
	c := &msgCache{} // nil Redis makes an accidental dispatch fail the test.
	for _, call := range []func() error{
		func() error { return c.SetMinSeq(context.Background(), "cid", -1) },
		func() error { return c.SetConversationUserMinSeq(context.Background(), "cid", "owner", -1) },
		func() error {
			return c.SetMinSeqs(context.Background(), map[string]int64{"positive": 3, "invalid": -1})
		},
		func() error {
			return c.SetUserConversationsMinSeqs(context.Background(), "owner", map[string]int64{"positive": 3, "invalid": -1})
		},
	} {
		if call() == nil {
			t.Fatal("negative visibility floor was accepted")
		}
	}
}

const retentionRedisFixtureURI = "redis://127.0.0.1:36379/15"

func allowedRedisFixtureURI(uri string) bool { return uri == retentionRedisFixtureURI }

func TestRetentionRedisFixtureRejectsAlternateDatabaseOrAuthorityWithoutConnecting(t *testing.T) {
	for _, uri := range []string{"redis://localhost:36379/15", "redis://127.0.0.1:6379/15", "redis://127.0.0.1:36379/0", "rediss://127.0.0.1:36379/15", "redis://fixture:synthetic@127.0.0.1:36379/15", retentionRedisFixtureURI + "?db=0", retentionRedisFixtureURI + "#fragment"} {
		if allowedRedisFixtureURI(uri) {
			t.Fatal("nonexact task Redis URI allowed")
		}
	}
	if !allowedRedisFixtureURI(retentionRedisFixtureURI) {
		t.Fatal("exact task Redis URI rejected")
	}
}

func TestVisibilityFloorRedisNeverLowersForOldOutboxOrInt64Precision(t *testing.T) {
	uri := os.Getenv("LUOBO_RETENTION_TEST_REDIS")
	if uri == "" {
		t.Skip("explicit task Redis fixture not authorized/configured")
	}
	if !allowedRedisFixtureURI(uri) {
		t.Fatal("exact task Redis fixture URI required")
	}
	config, err := redis.ParseURL(uri)
	if err != nil || config.Addr != "127.0.0.1:36379" || config.DB != 15 || config.Username != "" || config.Password != "" || config.TLSConfig != nil {
		t.Fatal("exact task Redis configuration required")
	}
	config.DialTimeout = 2 * time.Second
	client := redis.NewClient(config)
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("task Redis close unconfirmed: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var nonce [24]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal("fixture nonce failed")
	}
	cid := "luobo_retention_cache_test_" + hex.EncodeToString(nonce[:12])
	owner := "fixture_owner"
	claim := hex.EncodeToString(nonce[12:])
	cache := NewMsgCacheModel(client).(*msgCache)
	keys := []string{cache.getMinSeqKey(cid), cache.getConversationUserMinSeqKey(cid, owner)}
	ownedKeys := []string{"luobo_retention_fixture_claim_" + cid, keys[0], keys[1]}
	// Reserve only these three nonce-qualified keys atomically. A collision
	// produces no write, and therefore grants no cleanup authority.
	reserved, err := client.Eval(ctx, `if redis.call('EXISTS',KEYS[1],KEYS[2],KEYS[3])~=0 then return 0 end
redis.call('SET',KEYS[1],ARGV[1]);redis.call('SET',KEYS[2],'0');redis.call('SET',KEYS[3],'0');return 1`, ownedKeys, claim).Int64()
	if err != nil || reserved != 1 {
		t.Fatal("new task Redis keys ownership unconfirmed; no cleanup authorized")
	}
	t.Cleanup(func() {
		end, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		removed, err := client.Eval(end, `if redis.call('GET',KEYS[1])~=ARGV[1] then return -1 end
return redis.call('DEL',KEYS[1],KEYS[2],KEYS[3])`, ownedKeys, claim).Int64()
		if err != nil || removed != 3 {
			t.Errorf("exact claimed task Redis keys cleanup unconfirmed: removed=%d error=%v", removed, err)
			return
		}
		remaining, err := client.Exists(end, ownedKeys...).Result()
		if err != nil || remaining != 0 {
			t.Errorf("task Redis keys absence after cleanup unconfirmed: remaining=%d error=%v", remaining, err)
		}
	})

	for _, tc := range []struct {
		name               string
		before, next, want int64
	}{
		{"legacy_restriction500_old_event101", 500, 101, 500},
		{"later_higher_event", 500, 700, 700},
		{"above_double_precision", 9007199254740993, 9007199254740992, 9007199254740993},
		{"adjacent_large_increase", 9007199254740992, 9007199254740993, 9007199254740993},
		{"maximum_int64", math.MaxInt64, math.MaxInt64 - 1, math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range keys {
				if err := client.Set(ctx, key, tc.before, 0).Err(); err != nil {
					t.Fatal("fixture key setup failed")
				}
			}
			if err := cache.SetMinSeq(ctx, cid, tc.next); err != nil {
				t.Fatal("global atomic max failed")
			}
			if err := cache.SetConversationUserMinSeq(ctx, cid, owner, tc.next); err != nil {
				t.Fatal("owner atomic max failed")
			}
			for _, key := range keys {
				v, err := client.Get(ctx, key).Int64()
				if err != nil || v != tc.want {
					t.Fatalf("floor=%d want=%d", v, tc.want)
				}
			}
		})
	}
	if err := client.Set(ctx, keys[1], "unattributed_value", 0).Err(); err != nil {
		t.Fatal("fixture key setup failed")
	}
	if err := cache.SetConversationUserMinSeq(ctx, cid, owner, 1); err == nil {
		t.Fatal("malformed old restriction overwritten")
	}
	if v, err := client.Get(ctx, keys[1]).Result(); err != nil || v != "unattributed_value" {
		t.Fatal("unknown cache data changed")
	}
}
