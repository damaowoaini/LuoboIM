package tokenverify

import (
	"sync"
	"testing"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/golang-jwt/jwt/v4"
)

func TestNewSessionsHaveUniqueJTIAndLegacyTokensRemainValid(t *testing.T) {
	old := config.Config.Secret
	secret := "synthetic-session-regression-secret"
	config.Config.Secret = &secret
	t.Cleanup(func() { config.Config.Secret = old })
	var wg sync.WaitGroup
	tokens := make(chan string, 64)
	failures := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := CreateToken("same-user", TokenUser, 3600)
			tokens <- token
			failures <- err
		}()
	}
	wg.Wait()
	close(tokens)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for token := range tokens {
		var c claims
		if _, err := jwt.ParseWithClaims(token, &c, secretFunc(secret)); err != nil || c.ID == "" || seen[token] {
			t.Fatal("sessions collided or jti missing", err)
		}
		seen[token] = true
	}
	legacy := jwt.NewWithClaims(jwt.SigningMethodHS256, buildClaims("same-user", TokenUser, 3600))
	token, err := legacy.SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if user, kind, err := GetToken(token); err != nil || user != "same-user" || kind != TokenUser {
		t.Fatal("legacy no-jti token rejected", err)
	}
}
func secretFunc(value string) jwt.Keyfunc {
	return func(*jwt.Token) (interface{}, error) { return []byte(value), nil }
}
