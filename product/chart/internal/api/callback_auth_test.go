package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type nonceRedisStub struct {
	redis.UniversalClient
	seen map[string]bool
	err  error
}

func (r *nonceRedisStub) SetNX(_ context.Context, key string, _ interface{}, _ time.Duration) *redis.BoolCmd {
	if r.err != nil {
		return redis.NewBoolResult(false, r.err)
	}
	fresh := !r.seen[key]
	r.seen[key] = true
	return redis.NewBoolResult(fresh, nil)
}
func signedCallback(body, command, ts, nonce string) string {
	mac := hmac.New(sha256.New, []byte("callback-test-secret"))
	mac.Write([]byte(ts + "\n" + nonce + "\n" + command + "\n" + body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestCallbackSecurityRejectsForgeryExpiryReplayAndRedisFailure(t *testing.T) {
	old := config.Config.Secret
	defer func() { config.Config.Secret = old }()
	secret := "callback-test-secret"
	config.Config.Secret = &secret
	const body = `{"fromUserID":"u1","toUserID":"u2"}`
	const nonce = "0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name                                  string
		age                                   time.Duration
		tamper, missing, redisFailure, replay bool
		allowed                               bool
	}{
		{name: "signed callback", allowed: true}, {name: "forged body", tamper: true}, {name: "unsigned", missing: true},
		{name: "expired", age: 10 * time.Minute}, {name: "future", age: -10 * time.Minute}, {name: "replay", replay: true}, {name: "Redis unavailable", redisFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rdb := &nonceRedisStub{seen: map[string]bool{}}
			if tc.redisFailure {
				rdb.err = errors.New("Redis down")
			}
			if tc.replay {
				rdb.seen["CALLBACK_NONCE:"+nonce] = true
			}
			r := gin.New()
			reached := false
			r.POST("/callback/open_im/*command", callbackAuthentication(rdb), func(c *gin.Context) {
				reached = true
				b, _ := io.ReadAll(c.Request.Body)
				if string(b) != body {
					t.Fatal("callback body changed")
				}
				c.Status(200)
			})
			ts := strconv.FormatInt(time.Now().Add(-tc.age).Unix(), 10)
			wireBody := body
			if tc.tamper {
				wireBody = `{"fromUserID":"victim"}`
			}
			req := httptest.NewRequest("POST", "/callback/open_im/beforeAddFriend", strings.NewReader(wireBody))
			if !tc.missing {
				req.Header.Set("X-OpenIM-Callback-Timestamp", ts)
				req.Header.Set("X-OpenIM-Callback-Nonce", nonce)
				req.Header.Set("X-OpenIM-Callback-Signature", signedCallback(body, "beforeAddFriend", ts, nonce))
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if reached != tc.allowed || (w.Code == 200) != tc.allowed {
				t.Fatalf("reached=%v status=%d body=%s", reached, w.Code, w.Body.String())
			}
		})
	}
}
