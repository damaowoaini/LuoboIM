package cache

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestRedisEncryptionVersionAllocationPreservesHistoryUnderConcurrency(t *testing.T) {
	socket := os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET")
	if socket == "" {
		t.Skip("requires isolated Redis socket")
	}
	if !strings.Contains(socket, "references/integration/redis8.2/redis.sock") {
		t.Fatal("only task-private Redis is allowed")
	}
	client := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 15})
	// Register Close first: test cleanups run LIFO, after function defers. The
	// exact fixture key must be removed while this client is still usable.
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error("isolated encryption fixture Redis close failed")
		}
	})
	ctx := context.Background()
	conversation := "luobo_encryption_test_" + uuid.NewString()
	key := EncryptionKeyPrefix + conversation
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := client.Del(cleanup, key).Err(); err != nil {
			t.Error("exact encryption fixture key deletion failed")
			return
		}
		if remaining, err := client.Exists(cleanup, key).Result(); err != nil || remaining != 0 {
			t.Error("exact encryption fixture key absence unconfirmed")
		}
	})
	old := "old-legacy-key-must-stay-unchanged"
	if err := client.HSet(ctx, key, "1", old, MaxVersionKeyField, "1").Err(); err != nil {
		t.Fatal(err)
	}
	store := EncryptionCacheRedis{rdb: client}
	generator := func() (string, error) {
		b := make([]byte, 24)
		_, err := rand.Read(b)
		return base64.RawURLEncoding.EncodeToString(b), err
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.GenConversationNextVersionKey(ctx, conversation, generator); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if value, _ := client.HGet(ctx, key, "1").Result(); value != old {
		t.Fatal("existing key/version was overwritten")
	}
	versions, err := store.GetConversationEncryptionAllKey(ctx, conversation)
	if err != nil || len(versions) != 33 {
		t.Fatal("concurrent versions lost", err)
	}
	for i, version := range versions {
		if version.Version != int32(i+1) {
			t.Fatal("version allocation has duplicate/gap")
		}
		if i > 0 {
			at, err := client.HGet(ctx, key, "ISSUED_AT:"+strconv.Itoa(i+1)).Int64()
			if err != nil || at <= 0 {
				t.Fatal("key version missing creation timestamp")
			}
		}
	}
	before, _ := client.HGetAll(ctx, key).Result()
	if err := store.GenConversationNextVersionKey(ctx, conversation, func() (string, error) { return "", errors.New("entropy unavailable") }); err == nil {
		t.Fatal("generator error swallowed")
	}
	after, _ := client.HGetAll(ctx, key).Result()
	if len(before) != len(after) {
		t.Fatal("entropy failure changed version history")
	}
	// A stale legacy MAX must never overwrite a higher historical version.
	if err := client.HSet(ctx, key, MaxVersionKeyField, "1").Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.GenConversationNextVersionKey(ctx, conversation, generator); err != nil {
		t.Fatal(err)
	}
	max, err := store.GetConversationMaxVersionKey(ctx, conversation)
	if err != nil || max.Version != 34 {
		t.Fatal("stale MAX overwrote old version", err)
	}
}
