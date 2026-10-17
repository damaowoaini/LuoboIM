package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/cache"
	"github.com/OpenIMSDK/chat/pkg/common/tokenverify"
	imconstant "github.com/OpenIMSDK/protocol/constant"
	imtoken "github.com/OpenIMSDK/tools/tokenverify"
	"github.com/gin-gonic/gin"
	driver "github.com/go-sql-driver/mysql"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestRealCurrentSessionLogoutSQLRedisPendingRestartAndOwnerIsolation(t *testing.T) {
	dsn, socket := os.Getenv("LUOBO_INTEGRATION_MYSQL_DSN"), os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET")
	if dsn == "" || socket == "" {
		t.Skip("private MySQL/Redis required")
	}
	cfg, err := driver.ParseDSN(dsn)
	if err != nil || cfg.Net != "unix" || !strings.Contains(cfg.Addr, "/references/integration/mysql8.4/") || !strings.HasSuffix(socket, "/references/integration/redis8.2/redis.sock") {
		t.Fatal("private fixtures only")
	}
	root, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	name := "luobo_session_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := root.Exec("CREATE DATABASE `" + name + "`").Error; err != nil {
		t.Fatal(err)
	}
	cfg.DBName = name
	db, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sql, _ := db.DB(); sql != nil {
			sql.Close()
		}
		root.Exec("DROP DATABASE `" + name + "`")
		if sql, _ := root.DB(); sql != nil {
			sql.Close()
		}
	})
	if err := db.AutoMigrate(&accountstate.SessionRevocation{}); err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 15})
	t.Cleanup(func() { rdb.Close() })
	tokens := cache.NewTokenInterface(rdb)
	ctx := context.Background()
	user := "logout-" + uuid.NewString()
	peer := user + "-peer"
	t.Cleanup(func() { tokens.DeleteAllToken(ctx, user); tokens.DeleteAllToken(ctx, peer) })
	secret := "synthetic-private-current-session-secret"
	oldSecret := config.Config.Secret
	config.Config.Secret = &secret
	t.Cleanup(func() { config.Config.Secret = oldSecret })
	oldChat, _ := tokenverify.CreateToken(user, constant.NormalUser, 3600)
	newChat, _ := tokenverify.CreateToken(user, constant.NormalUser, 3600)
	peerChat, _ := tokenverify.CreateToken(peer, constant.NormalUser, 3600)
	adminChat, _ := tokenverify.CreateToken(user, constant.AdminUser, 3600)
	for _, row := range []struct{ user, token string }{{user, oldChat}, {user, newChat}, {peer, peerChat}} {
		if err := tokens.AddTokenFlag(ctx, row.user, row.token, 0); err != nil {
			t.Fatal(err)
		}
	}
	imJWT := func(owner string) string {
		claims := imtoken.BuildClaims(owner, 2, 1)
		claims.ID = uuid.NewString()
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	imCurrent, imForeign := imJWT(user), imJWT(peer)
	var unavailable atomic.Bool
	unavailable.Store(true)
	var callbacks atomic.Int32
	callback := func(ctx context.Context, owner string, platform int, digest string) error {
		if owner != user || platform != 2 || digest != accountstate.SessionDigest(imCurrent) {
			return errors.New("wrong session scope")
		}
		if operation, _ := ctx.Value(imconstant.OperationID).(string); operation == "" {
			return errors.New("missing background operation ID")
		}
		callbacks.Add(1)
		if unavailable.Load() {
			return errors.New("fixture IM unavailable")
		}
		return nil
	}
	service := accountstate.NewSessionService(db, rdb, callback)
	handler := &currentSessionLogout{service: service}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/account/logout", handler.Logout)
	post := func(token string, body any) map[string]any {
		t.Helper()
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/account/logout", bytes.NewReader(data))
		req.Header.Set("token", token)
		req.Header.Set("content-type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var result map[string]any
		if json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("invalid envelope")
		}
		return result
	}
	for _, row := range []struct{ token, im string }{{newChat, imForeign}, {adminChat, imCurrent}, {"forged", imCurrent}} {
		if result := post(row.token, map[string]string{"imToken": row.im}); result["errCode"] == float64(0) {
			t.Fatal("foreign/admin/forged logout accepted")
		}
	}
	remaining, _ := tokens.GetTokensWithoutError(ctx, user)
	if len(remaining) != 2 {
		t.Fatal("denied request revoked legitimate chat sessions")
	}
	result := post(oldChat, map[string]string{"imToken": imCurrent})
	if result["errCode"] != float64(0) {
		t.Fatal("durable queued logout failed")
	}
	data := result["data"].(map[string]any)
	if data["revoked"] != true || data["pending"] != true || data["sessionClosed"] != false {
		t.Fatal("pending revocation falsely completed")
	}
	remaining, _ = tokens.GetTokensWithoutError(ctx, user)
	if len(remaining) != 1 {
		t.Fatal("logout revoked newer chat session")
	}
	if _, ok := remaining[newChat]; !ok {
		t.Fatal("newer chat token removed")
	}
	peerTokens, _ := tokens.GetTokensWithoutError(ctx, peer)
	if len(peerTokens) != 1 {
		t.Fatal("foreign session revoked")
	}
	var wg sync.WaitGroup
	errorsCh := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := service.Request(ctx, user, 2, oldChat, imCurrent); errorsCh <- err }()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	var rows []accountstate.SessionRevocation
	if err := db.Find(&rows).Error; err != nil || len(rows) != 1 {
		t.Fatal("retry duplicated SQL revocation", err)
	}
	if strings.Contains(rows[0].ChatTokenHash, oldChat) || strings.Contains(rows[0].IMTokenHash, imCurrent) {
		t.Fatal("SQL stored raw credential")
	}
	unavailable.Store(false)
	restarted := accountstate.NewSessionService(db, rdb, callback)
	if err := restarted.Reconcile(ctx); err != nil {
		t.Fatal("restart did not finish pending IM revocation", err)
	}
	var completed int64
	db.Model(&accountstate.SessionRevocation{}).Where("completed_at IS NOT NULL").Count(&completed)
	if completed != 1 || callbacks.Load() < 2 {
		t.Fatal("not durably completed")
	}
	result = post(oldChat, map[string]string{"imToken": imCurrent})
	if result["errCode"] != float64(0) || result["data"].(map[string]any)["pending"] != false {
		t.Fatal("revoked-token retry not idempotent")
	}
	if err := db.Migrator().DropTable(&accountstate.SessionRevocation{}); err != nil {
		t.Fatal(err)
	}
	result = post(newChat, map[string]string{"imToken": imCurrent})
	if result["errCode"] == float64(0) {
		t.Fatal("SQL outage became successful logout")
	}
	remaining, _ = tokens.GetTokensWithoutError(ctx, user)
	if _, ok := remaining[newChat]; !ok {
		t.Fatal("failed persistence partially revoked without outbox")
	}
	t.Log("LUOBO_CHART_EXACT_LOGOUT_SQL_REDIS_HTTP_PASS")
}
