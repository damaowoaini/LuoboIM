package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/proto/admin"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

type unenrolledAdminStub struct{ admin.AdminClient }

func (unenrolledAdminStub) Login(context.Context, *admin.LoginReq, ...grpc.CallOption) (*admin.LoginResp, error) {
	return &admin.LoginResp{AdminUserID: "a1", GoogleAuthEnabled: false}, nil
}

func TestAdminLoginDoesNotIssueIMTokenBeforeTwoFactor(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/account/login", strings.NewReader(`{"account":"admin","password":"digest"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	// A nil IM caller deliberately fails if token issuance is attempted.
	(&AdminApi{adminClient: unenrolledAdminStub{}}).AdminLogin(c)
	var result struct {
		ErrCode int            `json:"errCode"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ErrCode != 0 {
		t.Fatal(w.Body.String())
	}
	if result.Data["imToken"] != nil && result.Data["imToken"] != "" {
		t.Fatal("privileged IM token issued without 2FA")
	}
}

type enrolledDynamicAdminStub struct{ admin.AdminClient }

func (enrolledDynamicAdminStub) Login(context.Context, *admin.LoginReq, ...grpc.CallOption) (*admin.LoginResp, error) {
	return &admin.LoginResp{AdminUserID: "dynamic-admin", AdminToken: "authenticated-chart-session", GoogleAuthEnabled: true}, nil
}

type adminIdentityCallerStub struct {
	apicall.CallerInterface
	mintedFor []string
}

func (s *adminIdentityCallerStub) UserToken(_ context.Context, user string, _ int32) (string, error) {
	s.mintedFor = append(s.mintedFor, user)
	return "fixture-admin-im-token", nil
}
func TestDynamicAdminUsesExplicitAuthorizedIMManagementIdentity(t *testing.T) {
	old := config.Config.AdminList
	config.Config.AdminList = []config.Admin{{AdminID: "bootstrap-admin", ImAdminID: "im-service-admin"}}
	defer func() { config.Config.AdminList = old }()
	caller := &adminIdentityCallerStub{}
	identity, token, err := adminIMToken(mctx.WithOpUserID(context.Background(), "dynamic-admin", constant.AdminUser), caller)
	if err != nil || identity != "im-service-admin" || token != "fixture-admin-im-token" || len(caller.mintedFor) != 1 || caller.mintedFor[0] != "im-service-admin" {
		t.Fatal("authorized internal admin workflow unavailable")
	}
	// A browser login must not fall back to that internal token if delegation is
	// unavailable, even when IM credentials could otherwise be minted.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/account/login", strings.NewReader(`{"account":"dynamic-admin","password":"digest","googleAuthCode":"123456"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	(&AdminApi{adminClient: enrolledDynamicAdminStub{}, imApiCaller: caller}).AdminLogin(c)
	var result struct {
		ErrCode int            `json:"errCode"`
		Data    map[string]any `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.ErrCode == 0 || len(caller.mintedFor) != 1 {
		t.Fatal("browser login fell back to internal service credential")
	}
	if config.GetIMAdmin("normal-user") == "im-service-admin" || config.GetIMAdmin("dynamic-admin") != "" {
		t.Fatal("strict cancellation mapping became a global fallback")
	}
	before := len(caller.mintedFor)
	if _, _, err := adminIMToken(mctx.WithOpUserID(context.Background(), "normal-user", constant.AdminUser-1), caller); err == nil || len(caller.mintedFor) != before {
		t.Fatal("ordinary user obtained management token")
	}
}
