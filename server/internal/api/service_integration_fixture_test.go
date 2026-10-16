package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/serviceauth"
	"github.com/redis/go-redis/v9"
)

// Fixture credentials use the same purpose/issuer and per-request proof as the
// current production Guard. They are never supplied from runtime configuration.
func integrationServiceToken(t *testing.T, user string, platform int) string {
	t.Helper()
	claims := authverify.ClassifiedClaims{
		Claims:  tokenverify.BuildClaims(user, platform, 1),
		Purpose: authverify.ServicePurpose,
	}
	claims.Issuer = config.Config.AdminDelegation.Issuer
	claims.ID = uuid.NewString()
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(config.Config.Secret))
	if err != nil {
		t.Fatal("synthetic service fixture credential signing failed")
	}
	return raw
}

func signIntegrationServiceRequest(t *testing.T, rdb redis.UniversalClient, request *http.Request, raw string, body []byte) {
	t.Helper()
	now := time.Now()
	header, err := serviceauth.Sign([]byte(config.Config.Secret), serviceauth.Internal, request.Method, request.URL.Path, raw, body, now)
	if err != nil {
		t.Fatal("synthetic fixture request signing failed")
	}
	key, err := serviceauth.Verify([]byte(config.Config.Secret), serviceauth.Internal, request.Method, request.URL.Path, raw, body, header, now)
	if err != nil {
		t.Fatal("synthetic fixture request proof not confirmed")
	}
	for name, values := range header {
		request.Header[name] = values
	}
	request.Header.Set("operationID", "synthetic-service-fixture-"+uuid.NewString())
	request.Header.Set("Content-Type", "application/json")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := rdb.Del(ctx, key).Err(); err != nil {
			t.Error("exact fixture request nonce cleanup failed")
		}
	})
}
