package controller

import (
	"context"
	"sync"
	"testing"

	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/golang-jwt/jwt/v4"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
)

type sessionCache struct {
	cache.MsgModel
	mu     sync.Mutex
	tokens map[string]bool
}

func (*sessionCache) GetTokensWithoutError(context.Context, string, int) (map[string]int, error) {
	return map[string]int{}, nil
}
func (c *sessionCache) AddTokenFlag(_ context.Context, _ string, _ int, token string, _ int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens[token] = true
	return nil
}
func TestIMSessionTokensHaveIndependentCryptographicJTI(t *testing.T) {
	secret := "synthetic-im-session-test-secret"
	c := &sessionCache{tokens: map[string]bool{}}
	db := NewAuthDatabase(c, secret, 1)
	var wg sync.WaitGroup
	failures := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := db.CreateToken(context.Background(), "same-user", 2)
			failures <- err
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(c.tokens) != 64 {
		t.Fatal("same-user/platform token sessions collided")
	}
	ids := map[string]bool{}
	for token := range c.tokens {
		var claims tokenverify.Claims
		if _, err := jwt.ParseWithClaims(token, &claims, func(*jwt.Token) (interface{}, error) { return []byte(secret), nil }); err != nil || claims.ID == "" || ids[claims.ID] {
			t.Fatal("missing/duplicate cryptographic jti", err)
		}
		ids[claims.ID] = true
	}
}
