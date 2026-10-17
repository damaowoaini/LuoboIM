package cache

import (
	"context"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAtomicTerminalKickCannotResurrectRevokedSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:36379", DB: 15, DialTimeout: time.Second})
	defer rdb.Close()
	if rdb.Ping(ctx).Err() != nil {
		t.Skip("isolated Redis unavailable")
	}
	user := "token-race-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	key := uidPidToken + user + ":" + constant.PlatformIDToName(2)
	defer rdb.Del(context.Background(), key)
	store := NewMsgCacheModel(rdb)
	for _, v := range []string{"old", "current", "other"} {
		if err := store.AddTokenFlag(ctx, user, 2, v, constant.NormalToken); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.GetTokensWithoutError(ctx, user, 2); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTokenByUidPid(ctx, user, 2, []string{"old"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.KickOtherTokensByUidPid(ctx, user, 2, "current"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	tokens, err := store.GetTokensWithoutError(ctx, user, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := tokens["old"]; exists {
		t.Fatal("revoked token resurrected")
	}
	if tokens["current"] != constant.NormalToken || tokens["other"] != constant.KickedToken {
		t.Fatal("wrong terminal token statuses")
	}
	if err = store.DeleteTokenByUidPid(ctx, user, 2, []string{"current"}); err != nil {
		t.Fatal(err)
	}
	if err = store.KickOtherTokensByUidPid(ctx, user, 2, "current"); err == nil {
		t.Fatal("removed current token accepted")
	}
	tokens, err = store.GetTokensWithoutError(ctx, user, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := tokens["current"]; exists {
		t.Fatal("missing current resurrected")
	}
	t.Log("LUOBO_ATOMIC_KICK_NO_REVOKED_TOKEN_RESURRECTION_PASS")
}
