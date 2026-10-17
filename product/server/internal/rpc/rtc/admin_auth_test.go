package rtc

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/redis/go-redis/v9"
)

func TestRTCAdminUsesRevocableChartCredential(t *testing.T) {
	socket := os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET")
	if socket == "" {
		t.Skip("requires private Redis fixture")
	}
	if !strings.Contains(socket, "/references/integration/redis8.2/") {
		t.Fatal("only task-private Redis permitted")
	}
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 15})
	defer rdb.Close()
	s := &Server{rdb: rdb}
	old := config.Config.Secret
	config.Config.Secret = strings.Repeat("test-secret-", 4)
	defer func() { config.Config.Secret = old }()
	user := "luobo-cms-" + uuid.NewString()
	ctx := rtcContext(user, 5)
	token, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, &chartClaims{UserID: user, UserType: 2, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}}).SignedString([]byte(config.Config.Secret))
	key := "CHAT_UID_TOKEN_STATUS:" + user
	disabled := "ACCOUNT_DISABLED:" + user
	defer rdb.Del(context.Background(), key, disabled)
	if s.admin(ctx) == nil {
		t.Fatal("dynamic admin accepted without chart credential")
	}
	signed := context.WithValue(ctx, adminTokenMetadata, []string{token})
	if s.admin(signed) == nil {
		t.Fatal("unregistered chart session accepted")
	}
	if err := rdb.HSet(context.Background(), key, token, 0).Err(); err != nil {
		t.Fatal("fixture Redis unavailable")
	}
	rdb.Expire(context.Background(), key, time.Minute)
	if s.admin(signed) != nil {
		t.Fatal("valid chart admin rejected")
	}
	mismatch := context.WithValue(rtcContext("other", 5), adminTokenMetadata, []string{token})
	if s.admin(mismatch) == nil {
		t.Fatal("chart admin credential actor mismatch accepted")
	}
	rdb.Set(context.Background(), disabled, "1", time.Minute)
	if s.admin(signed) == nil {
		t.Fatal("disabled chart admin accepted")
	}
	rdb.Del(context.Background(), disabled)
	rdb.HDel(context.Background(), key, token)
	if s.admin(signed) == nil {
		t.Fatal("revoked chart admin accepted")
	}
	rdb.Close()
	if s.admin(signed) == nil {
		t.Fatal("Redis unavailable did not fail closed")
	}
}
