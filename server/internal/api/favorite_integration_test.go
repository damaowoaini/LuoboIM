package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/favorite"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestFavoriteHTTPUsesRealIMTokenAndRejectsRevokedOrForeignOwner(t *testing.T) {
	uri, socket := os.Getenv("LUOBO_INTEGRATION_MONGO_URI"), os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET")
	if uri == "" || socket == "" {
		t.Skip("requires isolated Mongo and Redis")
	}
	if !strings.HasPrefix(uri, "mongodb://127.0.0.1:37017/") || !strings.Contains(socket, "references/integration/redis8.2/redis.sock") {
		t.Fatal("only task-private fixtures permitted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	db := mongoClient.Database("luobo_favorite_http_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	defer mongoClient.Disconnect(context.Background())
	defer db.Drop(context.Background())
	store, err := favorite.NewMongoStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 15})
	defer rdb.Close()
	oldSecret := config.Config.Secret
	config.Config.Secret = strings.Repeat("s", 32)
	defer func() { config.Config.Secret = oldSecret }()
	owner := "luobo_test_" + uuid.NewString()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, tokenverify.BuildClaims(owner, 5, 1)).SignedString([]byte(config.Config.Secret))
	if err != nil {
		t.Fatal(err)
	}
	tokenCache := cache.NewMsgCacheModel(rdb)
	if err := tokenCache.SetTokenMapByUidPid(ctx, owner, 5, map[string]int{token: constant.NormalToken}); err != nil {
		t.Fatal(err)
	}
	defer tokenCache.DeleteAllTokensByUidPid(context.Background(), owner, 5)
	defer rdb.Del(context.Background(), "ACCOUNT_DISABLED:"+owner)
	api := &favoriteAPI{service: &favorite.Service{Store: store}}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.POST("/favorite/add", GinParseToken(rdb), api.handle("add"))
	invoke := func(body, credential string) map[string]json.RawMessage {
		req := httptest.NewRequest("POST", "/favorite/add", strings.NewReader(body))
		if credential != "" {
			req.Header.Set("token", credential)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		var result map[string]json.RawMessage
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatal("invalid API envelope", err)
		}
		return result
	}
	// This test isolates the real authentication/owner boundary using a legacy
	// standalone file. Chat-message provenance has its own source-policy tests.
	body := `{"userID":"` + owner + `","favoriteType":2,"sourceID":"standalone-file-1"}`
	succeeded := func(result map[string]json.RawMessage) bool {
		var code int
		json.Unmarshal(result["errCode"], &code)
		return code == 0
	}
	if succeeded(invoke(body, "")) {
		t.Fatal("missing IM token accepted")
	}
	if !succeeded(invoke(body, token)) {
		t.Fatal("valid IM-token favorite failed")
	}
	if succeeded(invoke(`{"userID":"victim","favoriteType":1,"sourceID":"stolen"}`, token)) {
		t.Fatal("body overrode token identity")
	}
	forged, err := jwt.NewWithClaims(jwt.SigningMethodHS256, tokenverify.BuildClaims(owner, 5, 1)).SignedString([]byte("different-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if succeeded(invoke(body, forged)) {
		t.Fatal("forged token accepted")
	}
	if err := tokenCache.DeleteAllTokensByUidPid(ctx, owner, 5); err != nil {
		t.Fatal(err)
	}
	if succeeded(invoke(body, token)) {
		t.Fatal("revoked IM token accepted")
	}
	if err := tokenCache.SetTokenMapByUidPid(ctx, owner, 5, map[string]int{token: constant.NormalToken}); err != nil {
		t.Fatal(err)
	}
	if err := rdb.Set(ctx, "ACCOUNT_DISABLED:"+owner, "cancelled", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if succeeded(invoke(body, token)) {
		t.Fatal("cancelled account accessed favorite API")
	}
}
