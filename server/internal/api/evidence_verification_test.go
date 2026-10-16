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
	"github.com/OpenIMSDK/tools/mw"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/admincapability"
	"github.com/openimsdk/open-im-server/v3/internal/objectverify"
	"github.com/openimsdk/open-im-server/v3/internal/objectverify/pb"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/serviceauth"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

type evidenceRPCFixture struct{ calls int }

func (f *evidenceRPCFixture) Verify(context.Context, *pb.VerifyRequest, ...grpc.CallOption) (*pb.VerifyResponse, error) {
	f.calls++
	return &pb.VerifyResponse{Valid: true}, nil
}

func TestEvidenceHTTPRequiresServiceClassMACLiveSessionAndBoundedJSON(t *testing.T) {
	oldSecret, oldIssuer, oldManagers := config.Config.Secret, config.Config.AdminDelegation.Issuer, config.Config.Manager.UserID
	config.Config.Secret = strings.Repeat("synthetic-evidence-http-", 3)
	config.Config.AdminDelegation.Issuer = "evidence-http-A"
	manager := "evidence-service-" + uuid.NewString()
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
	store := cache.NewMsgCacheModel(rdb)
	mint := func(purpose, issuer string) string {
		c := authverify.ClassifiedClaims{Claims: tokenverify.Claims{UserID: manager, PlatformID: 10, RegisteredClaims: jwt.RegisteredClaims{Issuer: issuer, ID: uuid.NewString(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}, Purpose: purpose}
		if purpose == authverify.DelegatedPurpose {
			c.Subject = "actor"
			c.Audience = jwt.ClaimStrings{authverify.AdminAudience}
			c.Delegation = &authverify.Delegation{Version: 1, AdminID: "actor", SessionHash: strings.Repeat("b", 64)}
		}
		raw, e := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(config.Config.Secret))
		if e != nil {
			t.Fatal(e)
		}
		if e = store.AddTokenFlag(context.Background(), manager, 10, raw, constant.NormalToken); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			rdb.HDel(context.Background(), "UID_PID_TOKEN_STATUS:"+manager+":"+constant.PlatformIDToName(10), raw)
		})
		return raw
	}
	raw := mint(authverify.ServicePurpose, config.Config.AdminDelegation.Issuer)
	rpc := &evidenceRPCFixture{}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(mw.GinParseOperationID(), admincapability.Guard(rdb, nil))
	router.POST(objectverify.HTTPPath, GinParseToken(rdb), evidenceVerificationHandler(rpc))
	request := func(token, body string, h http.Header) int {
		t.Helper()
		q := httptest.NewRequest(http.MethodPost, objectverify.HTTPPath, strings.NewReader(body))
		q.Header = h.Clone()
		q.Header.Set("token", token)
		q.Header.Set("operationID", "evidence-http-"+uuid.NewString())
		q.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, q)
		var out struct {
			ErrCode int `json:"errCode"`
			Data    struct {
				Valid bool `json:"valid"`
			} `json:"data"`
		}
		if json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal("non JSON verification response")
		}
		if out.ErrCode == 0 && !out.Data.Valid {
			t.Fatal("success without affirmative batch result")
		}
		return out.ErrCode
	}
	body := `{"ownerUserID":"owner","urls":["https://fixture.invalid/api/object/owner/evidence.png"]}`
	sign := func(token, payload string) http.Header {
		t.Helper()
		h, e := serviceauth.Sign([]byte(config.Config.Secret), serviceauth.Internal, "POST", objectverify.HTTPPath, token, []byte(payload), time.Now())
		if e != nil {
			t.Fatal(e)
		}
		key, e := serviceauth.Verify([]byte(config.Config.Secret), serviceauth.Internal, "POST", objectverify.HTTPPath, token, []byte(payload), h, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { rdb.Del(context.Background(), key) })
		return h
	}
	proof := sign(raw, body)
	if request(raw, body, proof) != 0 || rpc.calls != 1 {
		t.Fatal("valid signed service HTTP rejected")
	}
	if request(raw, body, proof) == 0 || rpc.calls != 1 {
		t.Fatal("HTTP replay reached private RPC")
	}
	if request(raw, body, http.Header{}) == 0 || rpc.calls != 1 {
		t.Fatal("stolen service JWT without MAC reached private RPC")
	}
	for _, token := range []string{mint("", config.Config.AdminDelegation.Issuer), mint(authverify.DelegatedPurpose, config.Config.AdminDelegation.Issuer), mint(authverify.ServicePurpose, "evidence-http-B")} {
		if request(token, body, sign(token, body)) == 0 || rpc.calls != 1 {
			t.Fatal("wrong class/issuer reached private RPC")
		}
	}
	for _, bad := range []string{`{}`, `{"ownerUserID":"owner","urls":[]}`, `{"ownerUserID":"owner","urls":["x"],"forged":true}`, body + `{}`, `{"ownerUserID":"owner","urls":["` + strings.Repeat("x", 4097) + `"]}`} {
		if request(raw, bad, sign(raw, bad)) == 0 || rpc.calls != 1 {
			t.Fatal("invalid JSON/bounds reached private RPC")
		}
	}
	if e := store.AddTokenFlag(context.Background(), manager, 10, raw, constant.KickedToken); e != nil {
		t.Fatal(e)
	}
	if request(raw, body, sign(raw, body)) == 0 || rpc.calls != 1 {
		t.Fatal("revoked service credential reached private RPC")
	}
	t.Log("LUOBO_EVIDENCE_HTTP_SERVICE_PROOF_LIVE_REDIS_PASS")
}
