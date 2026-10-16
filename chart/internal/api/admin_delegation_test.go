package api

import (
	"context"
	"encoding/json"
	"github.com/OpenIMSDK/chat/pkg/common/admindelegation"
	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	admintable "github.com/OpenIMSDK/chat/pkg/common/db/table/admin"
	"github.com/OpenIMSDK/chat/pkg/common/serviceauth"
	"github.com/OpenIMSDK/chat/pkg/proto/admin"
	toolmw "github.com/OpenIMSDK/tools/mw"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type delegatedLoginStub struct {
	admin.AdminClient
	id, session string
}

func (s delegatedLoginStub) Login(context.Context, *admin.LoginReq, ...grpc.CallOption) (*admin.LoginResp, error) {
	return &admin.LoginResp{AdminUserID: s.id, AdminToken: s.session, GoogleAuthEnabled: true, Level: 80}, nil
}

type internalPreviewCaller struct {
	apicall.CallerInterface
	token string
}

func (s internalPreviewCaller) ImAdminTokenWithDefaultAdmin(context.Context) (string, error) {
	return s.token, nil
}
func delegationFixture(t *testing.T) (*AdminApi, *redis.Client, string, string) {
	t.Helper()
	secret, issuer, list, url := config.Config.Secret, config.Config.AdminDelegation.Issuer, config.Config.AdminList, config.Config.OpenIMUrl
	synthetic := strings.Repeat("synthetic-http-key-", 3)
	config.Config.Secret = &synthetic
	config.Config.AdminDelegation.Issuer = "tenant-A"
	config.Config.AdminList = []config.Admin{{AdminID: "bootstrap", ImAdminID: "configured-service"}}
	t.Cleanup(func() {
		config.Config.Secret = secret
		config.Config.AdminDelegation.Issuer = issuer
		config.Config.AdminList = list
		config.Config.OpenIMUrl = url
	})
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	socket := "/Users/chuzu/Documents/projects/LuoboIM/references/integration/mysql8.4/mysql.sock"
	root, e := gorm.Open(mysql.Open("root@unix("+socket+")/luobo_integration?parseTime=true"), cfg)
	if e != nil {
		t.Skip("private MySQL unavailable")
	}
	name := "luobo_admin_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if root.Exec("CREATE DATABASE `"+name+"`").Error != nil {
		t.Fatal("fixture schema failed")
	}
	db, e := gorm.Open(mysql.Open("root@unix("+socket+")/"+name+"?parseTime=true"), cfg)
	if e != nil {
		t.Fatal(e)
	}
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:36379", DB: 15})
	if rdb.Ping(context.Background()).Err() != nil {
		t.Skip("private Redis unavailable")
	}
	id := "admin-" + uuid.NewString()
	t.Cleanup(func() {
		rdb.Del(context.Background(), "CHAT_UID_TOKEN_STATUS:"+id)
		rdb.Close()
		r, _ := db.DB()
		r.Close()
		root.Exec("DROP DATABASE `" + name + "`")
		r, _ = root.DB()
		r.Close()
	})
	if e := db.AutoMigrate(&admintable.Admin{}, &admindelegation.Record{}, &admindelegation.RequestAudit{}, &adminMessageSearchAudit{}); e != nil {
		t.Fatal("schema failed")
	}
	if db.Create(&admintable.Admin{Account: "normal", UserID: id, Level: 80, GoogleAuthEnabled: true, GoogleAuthSecret: "synthetic-totp", CreateTime: time.Now()}).Error != nil {
		t.Fatal("admin fixture failed")
	}
	session, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"UserID": id, "UserType": 2, "PlatformID": 0, "exp": time.Now().Add(time.Hour).Unix(), "jti": uuid.NewString()}).SignedString([]byte(synthetic))
	rdb.HSet(context.Background(), "CHAT_UID_TOKEN_STATUS:"+id, session, 0)
	return &AdminApi{delegation: &admindelegation.Service{DB: db, Redis: rdb, Secret: []byte(synthetic), Issuer: "tenant-A"}}, rdb, id, session
}
func TestAdminLoginIssuesBoundCapabilityWithoutInternalCredentialCall(t *testing.T) {
	a, rdb, id, session := delegationFixture(t)
	a.adminClient = delegatedLoginStub{id: id, session: session}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/account/login", strings.NewReader(`{"account":"normal","password":"digest","googleAuthCode":"123456"}`))
	a.AdminLogin(c)
	var response struct {
		ErrCode int `json:"errCode"`
		Data    struct {
			ImToken  string `json:"imToken"`
			ImUserID string `json:"imUserID"`
		} `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || response.ErrCode != 0 || response.Data.ImUserID != "configured-service" {
		t.Fatal("legitimate browser login failed")
	}
	raw := response.Data.ImToken
	actor, level, e := a.delegation.Validate(context.Background(), raw)
	if e != nil || actor != id || level != 80 {
		t.Fatal("browser capability lacks live actor")
	}
	// Exercise the private HTTP boundary with a valid signature, replay, forged
	// external admin header and live session revocation.
	path := "/internal/admin_delegation/validate"
	body := `{"method":"POST","path":"/user/get_users","bodyHash":"` + strings.Repeat("a", 64) + `"}`
	router := gin.New()
	router.Use(toolmw.GinParseOperationID())
	router.POST(path, a.validateDelegation(rdb))
	h, _ := serviceauth.Sign([]byte(*config.Config.Secret), serviceauth.Validation, "POST", path, raw, []byte(body), time.Now())
	req := func(header http.Header) int {
		q := httptest.NewRequest("POST", path, strings.NewReader(body))
		q.Header = header.Clone()
		q.Header.Set("token", raw)
		q.Header.Set("operationID", "synthetic-admin-delegation-http-test")
		q.Header.Set("X-Admin-ID", "foreign")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, q)
		var m struct {
			ErrCode int `json:"errCode"`
			Data    struct {
				AdminID string `json:"adminID"`
			} `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &m)
		if m.ErrCode == 0 && m.Data.AdminID != id {
			t.Fatal("external actor header trusted")
		}
		return m.ErrCode
	}
	if req(h) != 0 || req(h) == 0 {
		t.Fatal("private signature/replay boundary failed")
	}
	nonce, _ := serviceauth.Verify([]byte(*config.Config.Secret), serviceauth.Validation, "POST", path, raw, []byte(body), h, time.Now())
	defer rdb.Del(context.Background(), nonce)
	if req(http.Header{}) == 0 {
		t.Fatal("unsigned validation accepted")
	}
	rdb.HDel(context.Background(), "CHAT_UID_TOKEN_STATUS:"+id, session)
	fresh, _ := serviceauth.Sign([]byte(*config.Config.Secret), serviceauth.Validation, "POST", path, raw, []byte(body), time.Now())
	if req(fresh) == 0 {
		t.Fatal("revoked administrator session passed HTTP validator")
	}
	key, _ := serviceauth.Verify([]byte(*config.Config.Secret), serviceauth.Validation, "POST", path, raw, []byte(body), fresh, time.Now())
	defer rdb.Del(context.Background(), key)
}
func TestScopedMessageSearchAuditsAndDoesNotFallBackForNormalAdmin(t *testing.T) {
	a, _, id, session := delegationFixture(t)
	a.adminClient = previewAdminStub{identity: id, level: 80}
	// The caller is nil: unauthorized reads must end before any upstream call.
	invoke := func(body string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/admin/message_search", strings.NewReader(body))
		c.Request.Header.Set("token", session)
		SetToken(c, id, constant.AdminUser)
		a.adminMessageSearch(c)
		var r struct {
			ErrCode int `json:"errCode"`
		}
		json.Unmarshal(w.Body.Bytes(), &r)
		return r.ErrCode
	}
	body := `{"sendID":"synthetic-owner","recvID":"","msgType":101,"sendTime":"","sessionType":1,"pagination":{"pageNumber":1,"showNumber":10}}`
	if invoke(body) != 1002 {
		t.Fatal("ordinary admin history bypassed preview grant")
	}
	a.adminClient = previewAdminStub{identity: id, level: 100}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		if _, e := serviceauth.Verify([]byte(*config.Config.Secret), serviceauth.Internal, r.Method, r.URL.Path, r.Header.Get("token"), b, r.Header, time.Now()); e != nil {
			t.Error("search lacked private proof")
		}
		io.WriteString(w, `{"errCode":0,"data":{"chatLogs":[{"clientMsgID":"synthetic"}],"chatLogsNum":1}}`)
	}))
	defer upstream.Close()
	config.Config.OpenIMUrl = upstream.URL
	raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"purpose": "luobo-internal-service-v1", "iss": "tenant-A", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte(*config.Config.Secret))
	a.imApiCaller = internalPreviewCaller{token: raw}
	if invoke(body) != 0 || calls != 1 {
		t.Fatal("superadmin legacy message search unavailable")
	}
	var audits []adminMessageSearchAudit
	if a.delegation.DB.Find(&audits).Error != nil || len(audits) != 1 || audits[0].Actor != id || audits[0].State != "completed" {
		t.Fatal("message search audit missing")
	}
	a.delegation.DB.Migrator().DropTable(&adminMessageSearchAudit{})
	if invoke(body) == 0 || calls != 1 {
		t.Fatal("audit failure did not block history read")
	}
}
