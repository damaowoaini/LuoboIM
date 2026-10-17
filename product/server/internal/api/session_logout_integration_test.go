package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/OpenIMSDK/tools/mw"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/sessionlogout"
	"github.com/openimsdk/open-im-server/v3/internal/admincapability"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestRealIMLogoutHTTPRejectsForeignOwnerAndProtectsDigestServiceBoundary(t *testing.T) {
	uri, socket := os.Getenv("LUOBO_INTEGRATION_MONGO_URI"), os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET")
	if uri == "" || socket == "" {
		t.Skip("private Mongo/Redis required")
	}
	if !strings.HasPrefix(uri, "mongodb://127.0.0.1:37017/") || !strings.HasSuffix(socket, "/references/integration/redis8.2/redis.sock") {
		t.Fatal("private fixtures only")
	}
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database("luobo_logout_http_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	t.Cleanup(func() { db.Drop(ctx); client.Disconnect(ctx) })
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 15})
	t.Cleanup(func() { rdb.Close() })
	tokens := cache.NewMsgCacheModel(rdb)
	oldSecret, oldAdmins := config.Config.Secret, config.Config.IMAdmin.UserID
	oldIssuer := config.Config.AdminDelegation.Issuer
	config.Config.Secret = strings.Repeat("synthetic-im-logout-", 3)
	config.Config.AdminDelegation.Issuer = "synthetic-logout-integration"
	admin := "logout-admin-" + uuid.NewString()
	config.Config.IMAdmin.UserID = []string{admin}
	t.Cleanup(func() { config.Config.Secret = oldSecret; config.Config.IMAdmin.UserID = oldAdmins })
	t.Cleanup(func() { config.Config.AdminDelegation.Issuer = oldIssuer })
	owner := "logout-owner-" + uuid.NewString()
	mint := func(user string) string {
		claims := tokenverify.BuildClaims(user, 2, 1)
		claims.ID = uuid.NewString()
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(config.Config.Secret))
		if err != nil {
			t.Fatal(err)
		}
		if user == admin {
			token = integrationServiceToken(t, user, 2)
		}
		if err := tokens.AddTokenFlag(ctx, user, 2, token, constant.NormalToken); err != nil {
			t.Fatal(err)
		}
		return token
	}
	current, newer, adminToken := mint(owner), mint(owner), mint(admin)
	t.Cleanup(func() { tokens.DeleteAllTokensByUidPid(ctx, owner, 2); tokens.DeleteAllTokensByUidPid(ctx, admin, 2) })
	service, err := sessionlogout.New(ctx, db, rdb, func(_ context.Context, user string, platform int, digest string) error {
		if user != owner || platform != 2 || digest != sessionlogout.Digest(current) {
			t.Fatal("gateway logout scope changed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(mw.GinParseOperationID(), admincapability.Guard(rdb, nil))
	router.POST("/auth/logout", sessionLogoutHandler(service))
	router.POST("/auth/logout_session", GinParseToken(rdb), sessionDigestLogoutHandler(service))
	post := func(path, token, body string) map[string]any {
		t.Helper()
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("token", token)
		if token != "" {
			signIntegrationServiceRequest(t, rdb, req, token, []byte(body))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var result map[string]any
		if json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("bad envelope")
		}
		return result
	}
	digestBody, _ := json.Marshal(map[string]any{"userID": owner, "platform": 2, "tokenHash": sessionlogout.Digest(current)})
	legacyAdmin, err := jwt.NewWithClaims(jwt.SigningMethodHS256, tokenverify.BuildClaims(admin, 2, 1)).SignedString([]byte(config.Config.Secret))
	if err != nil {
		t.Fatal("legacy fixture signing failed")
	}
	if err := tokens.AddTokenFlag(ctx, admin, 2, legacyAdmin, constant.NormalToken); err != nil {
		t.Fatal("legacy fixture status setup failed")
	}
	if result := post("/auth/logout_session", legacyAdmin, string(digestBody)); result["errCode"] == float64(0) {
		t.Fatal("legacy shared manager JWT reached digest logout")
	}
	if result := post("/auth/logout_session", current, string(digestBody)); result["errCode"] == float64(0) {
		t.Fatal("ordinary actor used service digest endpoint")
	}
	for _, row := range []struct{ token, body string }{{current, `{"userID":"victim"}`}, {"forged", `{}`}, {current, `{} {}`}, {current, `{"platform":5}`}} {
		if result := post("/auth/logout", row.token, row.body); result["errCode"] == float64(0) {
			t.Fatal("foreign/forged/malformed logout accepted")
		}
	}
	if n, err := db.Collection("session_logout_jobs").CountDocuments(ctx, bson.M{}); err != nil || n != 0 {
		t.Fatal("denied request queued job", err)
	}
	if result := post("/auth/logout_session", adminToken, string(digestBody)); result["errCode"] != float64(0) {
		t.Fatal("authorized service digest logout failed")
	}
	remaining, err := tokens.GetTokensWithoutError(ctx, owner, 2)
	if err != nil || len(remaining) != 1 {
		t.Fatal("incorrect current-session revoke", err)
	}
	if _, ok := remaining[newer]; !ok {
		t.Fatal("newer same-platform token revoked")
	}
	if result := post("/auth/logout", current, `{}`); result["errCode"] != float64(0) {
		t.Fatal("revoked own-token idempotent retry rejected")
	}
	t.Log("LUOBO_IM_EXACT_LOGOUT_HTTP_AUTHORIZATION_PASS")
}
