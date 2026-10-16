package admincapability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/serviceauth"
	"github.com/redis/go-redis/v9"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeValidator struct {
	actor string
	fail  bool
	calls int
}

func (v *fakeValidator) Validate(context.Context, string, string, []byte) (string, int32, error) {
	v.calls++
	if v.fail {
		return "", 0, errors.New("synthetic unavailable")
	}
	return v.actor, 80, nil
}
func setup(t *testing.T) (redis.UniversalClient, string, string, string) {
	t.Helper()
	secret, issuer, manager := config.Config.Secret, config.Config.AdminDelegation.Issuer, config.Config.Manager.UserID
	config.Config.Secret = strings.Repeat("synthetic-key-", 4)
	config.Config.AdminDelegation.Issuer = "tenant-A"
	config.Config.Manager.UserID = []string{"service-manager"}
	t.Cleanup(func() {
		config.Config.Secret = secret
		config.Config.AdminDelegation.Issuer = issuer
		config.Config.Manager.UserID = manager
	})
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:36379", DB: 15, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	if rdb.Ping(context.Background()).Err() != nil {
		t.Skip("private Redis unavailable")
	}
	t.Cleanup(func() { rdb.Close() })
	mint := func(purpose string) string {
		c := authverify.ClassifiedClaims{Claims: tokenverify.Claims{UserID: "service-manager", PlatformID: 10, RegisteredClaims: jwt.RegisteredClaims{Issuer: "tenant-A", Subject: "actual-admin", Audience: jwt.ClaimStrings{authverify.AdminAudience}, ID: uuid.NewString(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}, Purpose: purpose}
		if purpose == authverify.DelegatedPurpose {
			c.Delegation = &authverify.Delegation{Version: 1, AdminID: "actual-admin", SessionHash: strings.Repeat("a", 64)}
		}
		raw, e := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(config.Config.Secret))
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	return rdb, mint(""), mint(authverify.ServicePurpose), mint(authverify.DelegatedPurpose)
}
func request(t *testing.T, rdb redis.UniversalClient, v Validator, token, method, path, body string, headers http.Header) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Guard(rdb, v))
	r.Any("/*path", func(c *gin.Context) {
		b, _ := io.ReadAll(c.Request.Body)
		c.JSON(200, gin.H{"errCode": 0, "actor": c.GetString(ContextAdmin), "owner": c.GetString(ContextUploadOwner), "body": string(b)})
	})
	q := httptest.NewRequest(method, path, strings.NewReader(body))
	if headers != nil {
		q.Header = headers.Clone()
	}
	q.Header.Set("token", token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, q)
	var result map[string]any
	if json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal("non JSON guard response")
	}
	return w.Code, result
}
func denied(m map[string]any) bool { return m["errCode"] != float64(0) }
func TestLegacyAndStolenServiceJWTDeniedEvenWhenCacheContainsToken(t *testing.T) {
	rdb, legacy, service, delegate := setup(t)
	key := "UID_PID_TOKEN_STATUS:service-manager:Admin"
	rdb.HSet(context.Background(), key, legacy, 0)
	t.Cleanup(func() { rdb.HDel(context.Background(), key, legacy) })
	for _, raw := range []string{legacy, service} {
		_, m := request(t, rdb, nil, raw, "POST", "/msg/search_msg", `{}`, nil)
		if !denied(m) {
			t.Fatal("shared browser credential bypassed proof/classification")
		}
	}
	if authverify.WsVerifyToken(delegate, "service-manager", 10) == nil || authverify.WsVerifyToken(service, "service-manager", 10) == nil || authverify.WsVerifyToken(legacy, "service-manager", 10) == nil {
		t.Fatal("management credential opened user websocket")
	}
}
func TestInternalProofRequiresExactRequestAndSingleUseNonce(t *testing.T) {
	rdb, _, service, _ := setup(t)
	path, body := "/msg/search_msg", `{"sendID":"synthetic"}`
	h, e := serviceauth.Sign([]byte(config.Config.Secret), serviceauth.Internal, "POST", path, service, []byte(body), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	_, m := request(t, rdb, nil, service, "POST", path, body, h)
	if denied(m) {
		t.Fatal("legitimate signed internal request rejected")
	}
	nonceKey, _ := serviceauth.Verify([]byte(config.Config.Secret), serviceauth.Internal, "POST", path, service, []byte(body), h, time.Now())
	t.Cleanup(func() { rdb.Del(context.Background(), nonceKey) })
	_, m = request(t, rdb, nil, service, "POST", path, body, h)
	if !denied(m) {
		t.Fatal("nonce replay accepted")
	}
	for _, change := range []struct{ p, b string }{{path, `{"sendID":"victim"}`}, {"/auth/user_token", body}} {
		_, m = request(t, rdb, nil, service, "POST", change.p, change.b, h)
		if !denied(m) {
			t.Fatal("request substitution accepted")
		}
	}
	old, _ := serviceauth.Sign([]byte(config.Config.Secret), serviceauth.Internal, "POST", path, service, []byte(body), time.Now().Add(-301*time.Second))
	_, m = request(t, rdb, nil, service, "POST", path, body, old)
	if !denied(m) {
		t.Fatal("expired proof accepted")
	}
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: time.Millisecond * 20})
	defer dead.Close()
	fresh, _ := serviceauth.Sign([]byte(config.Config.Secret), serviceauth.Internal, "POST", path, service, []byte(body), time.Now())
	_, m = request(t, dead, nil, service, "POST", path, body, fresh)
	if !denied(m) {
		t.Fatal("Redis failure opened proof replay gate")
	}
}
func TestDelegationWhitelistRoleAndSenderCannotBeBypassed(t *testing.T) {
	rdb, _, _, raw := setup(t)
	v := &fakeValidator{actor: "actual-admin"}
	_, m := request(t, rdb, v, raw, "POST", "/user/get_users", `{"pagination":{"pageNumber":1,"showNumber":20}}`, nil)
	if denied(m) || m["actor"] != "actual-admin" {
		t.Fatal("normal admin management capability lost")
	}
	for _, path := range []string{"/msg/search_msg", "/msg/pull_msg_by_seq", "/encryption/get_encryption_key", "/auth/user_token", "/favorite/list", "/conversation/get_all_conversations", "/third/tencent_cloud_storage_credential", "/%75ser/get_users", "/user//get_users", "/user/get_users?ignored=true"} {
		_, m = request(t, rdb, v, raw, "POST", path, `{}`, nil)
		if !denied(m) {
			t.Fatalf("unauthorized path accepted %s", path)
		}
	}
	for _, bad := range []string{`{"sendID":"victim","recvIDs":["peer"]}`, `{"sendID":"service-manager","SendID":"victim"}`, `{"pagination":{"pageNumber":1,"showNumber":1001}}`} {
		p := "/msg/batch_send_msg"
		if strings.Contains(bad, "pagination") {
			p = "/user/get_users"
		}
		_, m = request(t, rdb, v, raw, "POST", p, bad, nil)
		if !denied(m) {
			t.Fatal("forged sender/bounds accepted")
		}
	}
	_, m = request(t, rdb, v, raw, "GET", "/user/get_users", `{}`, nil)
	if !denied(m) {
		t.Fatal("method substitution accepted")
	}
	v.actor = "other-admin"
	_, m = request(t, rdb, v, raw, "POST", "/user/get_users", `{}`, nil)
	if !denied(m) {
		t.Fatal("validator actor mismatch accepted")
	}
	v.actor = "actual-admin"
	v.fail = true
	_, m = request(t, rdb, v, raw, "POST", "/user/get_users", `{}`, nil)
	if !denied(m) {
		t.Fatal("validator unavailable opened authorization")
	}
}
func TestDelegatedUploadHasRealActorNamespace(t *testing.T) {
	rdb, _, _, raw := setup(t)
	_, m := request(t, rdb, &fakeValidator{actor: "actual-admin"}, raw, "POST", "/object/complete_multipart_upload", `{"name":"service-manager/file.png","uploadID":"opaque"}`, nil)
	if denied(m) || !strings.HasPrefix(m["owner"].(string), "luobo-admin-") {
		t.Fatal("upload not owned by actual administrator")
	}
	var body map[string]any
	json.Unmarshal([]byte(m["body"].(string)), &body)
	if body["name"] != m["owner"].(string)+"/file.png" {
		t.Fatal("shared alias namespace retained")
	}
	for _, name := range []string{"../other/file.png", "service-manager/../other/file.png", "/absolute/file", "folder//file", "."} {
		body, _ := json.Marshal(map[string]any{"name": name, "uploadID": "opaque"})
		_, bad := request(t, rdb, &fakeValidator{actor: "actual-admin"}, raw, "POST", "/object/complete_multipart_upload", string(body), nil)
		if !denied(bad) {
			t.Fatal("noncanonical object alias accepted")
		}
	}
}
func TestHTTPValidatorProofAndBoundedFailure(t *testing.T) {
	rdb, _, _, raw := setup(t)
	_ = rdb
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("operationID") == "" {
			t.Error("private validator omitted production-required operationID")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if _, e := serviceauth.Verify([]byte(config.Config.Secret), serviceauth.Validation, r.Method, r.URL.Path, raw, body, r.Header, time.Now()); e != nil {
			t.Error("validator proof invalid")
		}
		if !bytes.Contains(body, []byte(`"path":"/user/get_users"`)) {
			t.Error("operation not audit bound")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"errCode":0,"data":{"adminID":"actual-admin","level":80}}`)
	}))
	defer server.Close()
	v := &HTTPValidator{URL: server.URL + "/internal/admin_delegation/validate", Secret: []byte(config.Config.Secret), AllowLoopbackHTTP: true}
	actor, level, e := v.Validate(context.Background(), raw, "/user/get_users", []byte(`{}`))
	if e != nil || actor != "actual-admin" || level != 80 || calls != 1 {
		t.Fatal("private validator contract failed", e)
	}
	v.AllowLoopbackHTTP = false
	if _, _, e = v.Validate(context.Background(), raw, "/user/get_users", nil); e == nil {
		t.Fatal("HTTP production validator allowed")
	}
}

func TestOrdinaryCredentialFailureKeepsRouteAuthenticationContract(t *testing.T) {
	rdb, _, _, _ := setup(t)
	expired, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, tokenverify.Claims{UserID: "ordinary-user", PlatformID: 2, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute))}}).SignedString([]byte(config.Config.Secret))
	for _, raw := range []string{"malformed-business-credential", expired} {
		r := gin.New()
		r.Use(Guard(rdb, nil))
		r.POST("/favorite/list", func(c *gin.Context) { c.JSON(http.StatusUnauthorized, gin.H{"errCode": 1001}) })
		req := httptest.NewRequest("POST", "/favorite/list", strings.NewReader(`{}`))
		req.Header.Set("token", raw)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), `"errCode":1001`) {
			t.Fatal("management boundary replaced ordinary route's authentication failure contract")
		}
	}
}
