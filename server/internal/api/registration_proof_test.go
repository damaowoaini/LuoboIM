package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/user"
	"github.com/OpenIMSDK/tools/mw"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/admincapability"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/registrationproof"
	"github.com/openimsdk/open-im-server/v3/pkg/common/serviceauth"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type registrationHTTPNonces struct {
	client *redis.Client
	keys   []string
}

func (n *registrationHTTPNonces) Consume(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	n.keys = append(n.keys, key)
	return n.client.SetNX(ctx, key, "1", ttl).Result()
}

type registrationHTTPRPC struct {
	user.UserClient
	auth   *registrationproof.Authorizer
	calls  int
	marked int
}

func (f *registrationHTTPRPC) UserRegister(ctx context.Context, req *user.UserRegisterReq, _ ...grpc.CallOption) (*user.UserRegisterResp, error) {
	f.calls++
	snapshot, e := registrationproof.Snapshot(req.Users)
	if e != nil {
		return nil, e
	}
	if snapshot.HasMarkers() {
		f.marked++
		md := metadata.MD{}
		keys, _ := ctx.Value(constant.RpcCustomHeader).([]string)
		for _, key := range keys {
			md.Set(key, ctx.Value(key).([]string)...)
		}
		ctx = metadata.NewIncomingContext(context.Background(), md)
	}
	ctx, e = f.auth.Authorize(ctx, req, snapshot)
	if e != nil {
		return nil, e
	}
	for _, u := range req.Users {
		if e := registrationproof.CheckCreate(ctx, u.UserID, u.Ex); e != nil {
			return nil, e
		}
	}
	return &user.UserRegisterResp{}, nil
}
func TestUserRegisterHTTPPreservesLegacyAndRequiresPurposeBoundInitialMarkerProof(t *testing.T) {
	oldSecret, oldIssuer, oldManagers := config.Config.Secret, config.Config.AdminDelegation.Issuer, config.Config.Manager.UserID
	config.Config.Secret = strings.Repeat("synthetic-registration-http-", 3)
	config.Config.AdminDelegation.Issuer = "registration-http-A"
	manager := "fixture-registration-" + uuid.NewString()
	config.Config.Manager.UserID = []string{manager}
	t.Cleanup(func() {
		config.Config.Secret = oldSecret
		config.Config.AdminDelegation.Issuer = oldIssuer
		config.Config.Manager.UserID = oldManagers
	})
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:36379", DB: 15, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	if rdb.Ping(context.Background()).Err() != nil {
		rdb.Close()
		t.Skip("private task Redis unavailable")
	}
	t.Cleanup(func() { rdb.Close() })
	tokens := cache.NewMsgCacheModel(rdb)
	nonces := &registrationHTTPNonces{client: rdb}
	t.Cleanup(func() {
		for _, key := range nonces.keys {
			rdb.Del(context.Background(), key)
		}
	})
	mint := func(purpose, issuer, id string) string {
		c := authverify.ClassifiedClaims{Claims: tokenverify.Claims{UserID: id, PlatformID: 10, RegisteredClaims: jwt.RegisteredClaims{Issuer: issuer, ID: uuid.NewString(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}, Purpose: purpose}
		if purpose == authverify.DelegatedPurpose {
			c.Subject = "browser-admin"
			c.Audience = jwt.ClaimStrings{authverify.AdminAudience}
			c.Delegation = &authverify.Delegation{Version: 1, AdminID: "browser-admin", SessionHash: strings.Repeat("a", 64)}
		}
		raw, e := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(config.Config.Secret))
		if e != nil {
			t.Fatal(e)
		}
		if e := tokens.AddTokenFlag(context.Background(), id, 10, raw, constant.NormalToken); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			rdb.HDel(context.Background(), "UID_PID_TOKEN_STATUS:"+id+":"+constant.PlatformIDToName(10), raw)
		})
		return raw
	}
	raw := mint(authverify.ServicePurpose, config.Config.AdminDelegation.Issuer, manager)
	rpc := &registrationHTTPRPC{auth: &registrationproof.Authorizer{Tokens: tokens, Nonces: nonces, Secret: []byte(config.Config.Secret)}}
	api := NewUserApi(rpcclient.User{Client: rpc})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(mw.GinParseOperationID(), admincapability.Guard(rdb, nil))
	router.POST("/user/user_register", api.UserRegister)
	request := func(token, body string, h http.Header) int {
		q := httptest.NewRequest(http.MethodPost, "/user/user_register", strings.NewReader(body))
		q.Header = h.Clone()
		q.Header.Set("operationID", "synthetic-registration-"+uuid.NewString())
		q.Header.Set("Content-Type", "application/json")
		if token != "" {
			q.Header.Set("token", token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, q)
		var out struct {
			ErrCode int `json:"errCode"`
		}
		if json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal("registration response was not JSON")
		}
		return out.ErrCode
	}
	body := func(ex string) string {
		b, _ := json.Marshal(map[string]any{"secret": config.Config.Secret, "users": []map[string]any{{"userID": "synthetic-owner", "nickname": "synthetic profile", "ex": ex}}})
		return string(b)
	}
	sign := func(token, payload string) http.Header {
		h, e := serviceauth.Sign([]byte(config.Config.Secret), serviceauth.Internal, "POST", "/user/user_register", token, []byte(payload), time.Now())
		if e != nil {
			t.Fatal(e)
		}
		key, e := serviceauth.Verify([]byte(config.Config.Secret), serviceauth.Internal, "POST", "/user/user_register", token, []byte(payload), h, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		nonces.keys = append(nonces.keys, key)
		return h
	}
	if request("", body("legacy opaque"), http.Header{}) != 0 || rpc.calls != 1 || rpc.marked != 0 {
		t.Fatal("legacy secret-only marker-free contract changed")
	}
	for _, key := range []string{registrationproof.PublicKey, registrationproof.AdminKey} {
		payload := body(`{"` + key + `":"` + uuid.NewString() + `"}`)
		proof := sign(raw, payload)
		if request(raw, payload, proof) != 0 {
			t.Fatal("fresh authenticated initial proof rejected")
		}
		calls := rpc.calls
		if request(raw, payload, proof) == 0 || rpc.calls != calls {
			t.Fatal("HTTP nonce replay reached RPC")
		}
		if request(raw, payload, http.Header{}) == 0 || rpc.calls != calls {
			t.Fatal("stolen service token without MAC reached RPC")
		}
		if request("", payload, http.Header{}) == 0 || rpc.calls != calls {
			t.Fatal("legacy secret-only call forged marker")
		}
	}
	marked := body(`{"luoboRegistrationID":"` + uuid.NewString() + `"}`)
	calls := rpc.calls
	for _, token := range []string{mint("", config.Config.AdminDelegation.Issuer, "synthetic-business-"+uuid.NewString()), mint(authverify.DelegatedPurpose, config.Config.AdminDelegation.Issuer, manager), mint(authverify.ServicePurpose, "registration-http-B", manager)} {
		if request(token, marked, sign(token, marked)) == 0 || rpc.calls != calls {
			t.Fatal("ordinary/delegated/cross-issuer caller minted initial proof")
		}
	}
	if e := tokens.AddTokenFlag(context.Background(), manager, 10, raw, constant.KickedToken); e != nil {
		t.Fatal(e)
	}
	if request(raw, marked, sign(raw, marked)) == 0 {
		t.Fatal("revoked service JWT minted marker")
	}
	for _, bad := range []string{`{}`, `{"users":[null]}`, body(`{"luoboRegistrationID":"not-a-uuid"}`), body(`{"luoboRegistrationID":"` + uuid.NewString() + `","luoboRegistrationID":"` + uuid.NewString() + `"}`), body(`{"luoboRegiſtrationID":"` + uuid.NewString() + `"}`)} {
		if request("", bad, http.Header{}) == 0 {
			t.Fatal("invalid/ambiguous marker request accepted")
		}
	}
	t.Log("LUOBO_USER_REGISTER_HTTP_PURPOSE_PROOF_LIVE_REDIS_PASS")
}
