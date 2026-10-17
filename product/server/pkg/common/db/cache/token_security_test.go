package cache

import (
	"context"
	"errors"
	"testing"

	"github.com/redis/go-redis/v9"
)

type tokenRedisStub struct {
	redis.UniversalClient
	disabled int64
	err      error
	read     bool
	deleted  string
}

func (r *tokenRedisStub) Exists(context.Context, ...string) *redis.IntCmd {
	return redis.NewIntResult(r.disabled, r.err)
}
func (r *tokenRedisStub) HGetAll(context.Context, string) *redis.MapStringStringCmd {
	r.read = true
	return redis.NewMapStringStringResult(map[string]string{"token": "0"}, nil)
}
func (r *tokenRedisStub) Del(_ context.Context, keys ...string) *redis.IntCmd {
	r.deleted = keys[0]
	return redis.NewIntResult(1, nil)
}

func TestTokenSecurityDisabledAccountsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		disabled int64
		err      error
		allowed  bool
	}{
		{"active account", 0, nil, true}, {"cancelled account", 1, nil, false},
		{"Redis unavailable", 0, errors.New("unavailable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rdb := &tokenRedisStub{disabled: tc.disabled, err: tc.err}
			_, err := (&msgCache{rdb: rdb}).GetTokensWithoutError(context.Background(), "u1", 2)
			if (err == nil) != tc.allowed || rdb.read != tc.allowed {
				t.Fatalf("allowed=%v read=%v err=%v", tc.allowed, rdb.read, err)
			}
		})
	}
}

func TestTokenSecurityRevocationWorksForDisabledAccount(t *testing.T) {
	rdb := &tokenRedisStub{disabled: 1}
	if err := (&msgCache{rdb: rdb}).DeleteAllTokensByUidPid(context.Background(), "u1", 2); err != nil {
		t.Fatal(err)
	}
	if rdb.deleted == "" || rdb.read {
		t.Fatal("revocation must delete tokens even when gated reads are denied")
	}
}
