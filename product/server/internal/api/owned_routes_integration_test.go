package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/sessionlogout"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func ownedAPIDatabaseFixture(t *testing.T) (context.Context, *mongo.Database, *redis.Client, *ownedMongoSource) {
	t.Helper()
	uri, socket := os.Getenv("LUOBO_INTEGRATION_MONGO_URI"), os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET")
	if uri == "" || socket == "" {
		t.Skip("requires explicit isolated Mongo/Redis; not an integration pass")
	}
	if !strings.HasPrefix(uri, "mongodb://127.0.0.1:37017/") || !strings.HasSuffix(socket, "/references/integration/redis8.2/redis.sock") {
		t.Fatal("only existing task-owned fixtures are permitted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	t.Cleanup(cancel)
	source := &ownedMongoSource{}
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri).SetMonitor(source.monitor()).SetRetryWrites(false))
	if err != nil {
		t.Fatal("owned Mongo fixture failed")
	}
	database := client.Database("luobo_owned_api_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 15})
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := database.Drop(cleanup); err != nil {
			t.Error("exact owned test database cleanup failed")
		}
		if err := client.Disconnect(cleanup); err != nil {
			t.Error("exact owned Mongo close failed")
		}
		if err := rdb.Close(); err != nil {
			t.Error("exact owned Redis close failed")
		}
	})
	return ctx, database, rdb, source
}

func TestOwnedAPIFavoriteAndSensitiveWritersBorrowOriginalDatabaseAndKeepHTTPAuth(t *testing.T) {
	ctx, database, rdb, source := ownedAPIDatabaseFixture(t)
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_V1", "0")
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_EPOCH", "")
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_ACTIVATION_SHA256", "")
	scope, err := newOwnedRouteScope(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := scope.stopLogout(closeCtx); err != nil {
			t.Error(err)
		}
		if err := scope.stopSensitive(closeCtx); err != nil {
			t.Error(err)
		}
		if err := scope.closeValidationHTTP(closeCtx); err != nil {
			t.Error(err)
		}
	})
	before, err := rdb.PubSubNumSub(ctx, "sensitive_word_reload").Result()
	if err != nil {
		t.Fatal(err)
	}
	sensitive, err := scope.newSensitiveWordAPI(rdb)
	if err != nil {
		t.Fatal(err)
	}
	during, err := rdb.PubSubNumSub(ctx, "sensitive_word_reload").Result()
	if err != nil || during["sensitive_word_reload"] != before["sensitive_word_reload"]+1 {
		t.Fatal("actual owned subscription not established", err)
	}
	word := &relation.SensitiveWordModel{ID: uuid.NewString(), Word: "synthetic-owned-word", Status: 1, CreateTime: time.Now(), UpdateTime: time.Now()}
	if err := sensitive.db.Create(ctx, []*relation.SensitiveWordModel{word}); err != nil {
		t.Fatal(err)
	}
	if n, err := database.Collection(relation.SensitiveWordModelTableName).CountDocuments(ctx, bson.M{"_id": word.ID}); err != nil || n != 1 {
		t.Fatal("sensitive writer escaped the exact owned database", n, err)
	}
	oldSecret := config.Config.Secret
	config.Config.Secret = strings.Repeat("owned-api-synthetic-secret-", 2)
	t.Cleanup(func() { config.Config.Secret = oldSecret })
	user := "owned-favorite-" + uuid.NewString()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, tokenverify.BuildClaims(user, 2, 1)).SignedString([]byte(config.Config.Secret))
	if err != nil {
		t.Fatal(err)
	}
	tokens := cache.NewMsgCacheModel(rdb)
	if err := tokens.AddTokenFlag(ctx, user, 2, token, constant.NormalToken); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tokens.DeleteAllTokensByUidPid(context.Background(), user, 2) })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if err := registerFavoriteRoutesOnDatabase(ctx, router, GinParseToken(rdb), rdb, nil, database); err != nil {
		t.Fatal(err)
	}
	invoke := func(credential string) bool {
		request := httptest.NewRequest("POST", "/favorite/add", strings.NewReader(`{"userID":"`+user+`","favoriteType":2,"sourceID":"owned-standalone-file"}`))
		request.Header.Set("token", credential)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, request)
		var result struct {
			ErrCode int `json:"errCode"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.ErrCode == 0
	}
	if invoke("") || !invoke(token) {
		t.Fatal("owned favorite route weakened authentication or rejected valid standalone file")
	}
	if n, err := database.Collection("favorite_items").CountDocuments(ctx, bson.M{"userID": user}); err != nil || n != 1 {
		t.Fatal("favorite writer escaped the exact owned database", n, err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := scope.stopSensitive(closeCtx); err != nil {
		t.Fatal(err)
	}
	after, err := rdb.PubSubNumSub(ctx, "sensitive_word_reload").Result()
	if err != nil || after["sensitive_word_reload"] != before["sensitive_word_reload"] {
		t.Fatal("original owned subscription remained after join", err)
	}
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatal("worker close incorrectly closed borrowed Redis")
	}
	if err := database.Client().Ping(ctx, nil); err != nil {
		t.Fatal("worker close incorrectly closed borrowed Mongo")
	}
	if source.observation().SourceOutcomeUnconfirmed {
		t.Fatal("acknowledged fixture writes were mislabeled unknown")
	}
}

func TestOwnedAPIExactLogoutRecoveryJoinsAndKeepsOriginalPendingJobAndNewToken(t *testing.T) {
	ctx, database, rdb, _ := ownedAPIDatabaseFixture(t)
	scope, err := newOwnedRouteScope(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scope.newSensitiveWordAPI(rdb); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := scope.stopLogout(closeCtx); err != nil {
			t.Error(err)
		}
		if err := scope.stopSensitive(closeCtx); err != nil {
			t.Error(err)
		}
		if err := scope.closeValidationHTTP(closeCtx); err != nil {
			t.Error(err)
		}
	})
	user := "owned-logout-" + uuid.NewString()
	tokens := cache.NewMsgCacheModel(rdb)
	for _, token := range []string{"synthetic-old", "synthetic-new"} {
		if err := tokens.AddTokenFlag(ctx, user, 2, token, constant.NormalToken); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = tokens.DeleteAllTokensByUidPid(context.Background(), user, 2) })
	entered := make(chan struct{})
	var once sync.Once
	service, err := sessionlogout.New(ctx, database, rdb, func(call context.Context, uid string, platform int, digest string) error {
		if uid != user || platform != 2 || digest != sessionlogout.Digest("synthetic-old") {
			return errors.New("original exact-session binding changed")
		}
		once.Do(func() { close(entered) })
		<-call.Done()
		return call.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Begin(ctx, user, 2, "synthetic-old")
	if err != nil {
		t.Fatal(err)
	}
	scope.logout = service
	if err := scope.start(); err != nil {
		t.Fatal(err)
	}
	ownedAPIWait(t, entered)
	stopCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := scope.stopLogout(stopCtx); err != nil {
		t.Fatal("real logout worker did not cancel/join", err)
	}
	if err := scope.stopSensitive(stopCtx); err != nil {
		t.Fatal("real pubsub/ticker did not join", err)
	}
	var stored sessionlogout.Job
	if err := database.Collection("session_logout_jobs").FindOne(ctx, bson.M{"_id": job.ID}).Decode(&stored); err != nil || stored.CompletedAt != nil || stored.TokenHash != sessionlogout.Digest("synthetic-old") {
		t.Fatal("pending original durable obligation lost or falsely acknowledged", err)
	}
	actual, err := tokens.GetTokensWithoutError(ctx, user, 2)
	value, exists := actual["synthetic-new"]
	if err != nil || !exists || value != constant.NormalToken {
		t.Fatal("recovery revoked newer session", err)
	}
	if _, exists := actual["synthetic-old"]; exists {
		t.Fatal("old exact token was not processed before kick barrier")
	}
}
