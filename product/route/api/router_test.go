package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"openim-route-service/model"
	"openim-route-service/service"
)

func TestRegistrationPendingAndRecoveredUseStructuredOptionalFlags(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		err                error
		pending, recovered bool
	}{
		{fmt.Errorf("wrapped: %w", service.ErrRegistrationPending), true, false},
		{fmt.Errorf("wrapped: %w", service.ErrRegistrationRecovered), false, true},
		{errors.New(service.ErrRegistrationRecovered.Error()), false, false},
	} {
		r := gin.New()
		r.POST("/api/auth/register", func(c *gin.Context) { respond(c, nil, tc.err) })
		w := request(r, "/api/auth/register", `{}`, "")
		var result struct {
			ErrCode int `json:"errCode"`
			Data    struct {
				Pending       bool `json:"registrationPending"`
				Recovered     bool `json:"registrationRecovered"`
				RequiresLogin bool `json:"requiresLogin"`
			} `json:"data"`
		}
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.ErrCode != 500 || result.Data.Pending != tc.pending || result.Data.Recovered != tc.recovered || result.Data.RequiresLogin != tc.recovered {
			t.Fatal("registration sentinel flags or legacy500 envelope changed")
		}
	}
}

type testServices struct {
	loginIP string
	verify  *service.VerifyRequest
	binds   int
}

func (s *testServices) Login(_ context.Context, _ *service.LoginRequest, ip, _ string) (map[string]interface{}, error) {
	s.loginIP = ip
	return map[string]interface{}{"errCode": 0}, nil
}
func (s *testServices) Register(context.Context, *service.RegisterRequest, string, string) (map[string]interface{}, error) {
	return map[string]interface{}{"errCode": 0}, nil
}
func (s *testServices) ResetPassword(context.Context, *service.ResetPasswordRequest, string, string) (map[string]interface{}, error) {
	return map[string]interface{}{"errCode": 0}, nil
}
func (s *testServices) Verify(_ context.Context, req *service.VerifyRequest, _ bool, _, _ string) (map[string]interface{}, error) {
	s.verify = req
	return map[string]interface{}{"errCode": 0}, nil
}
func (s *testServices) GetServerByInviteCode(string) (*model.RouteResponse, error) {
	return &model.RouteResponse{ServerID: "a", Domains: []string{"tenant.example"}}, nil
}
func (s *testServices) GetServerByUserID(string) (*model.RouteResponse, error) {
	return nil, errors.New("unbound")
}
func (s *testServices) BindUserToServer(string, string, string) error { s.binds++; return nil }
func (s *testServices) GetAllServers() ([]model.ServerConfig, error) {
	return []model.ServerConfig{}, nil
}
func (s *testServices) Post(context.Context, *model.RouteResponse, string, any, string, string) (map[string]interface{}, error) {
	return map[string]interface{}{"errCode": 0, "data": map[string]interface{}{"config": map[string]interface{}{"needVerificationCodeRegister": "0"}}}, nil
}
func testRouter(config RouterConfig) (*gin.Engine, *testServices) {
	gin.SetMode(gin.TestMode)
	s := &testServices{}
	return SetupRouterWithDependencies(config, Dependencies{s, s, s, s, s, s}), s
}
func request(r http.Handler, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Route-Admin-Token", token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
func TestPublicBindIsRejectedAndAdminBindStillWorks(t *testing.T) {
	token := strings.Repeat("a", 40)
	r, s := testRouter(RouterConfig{AdminToken: token})
	body := `{"user_id":"13812345678","server_id":"tenant"}`
	w := request(r, "/api/route/bind", body, "")
	if w.Code != 401 || s.binds != 0 {
		t.Fatal("anonymous routing mutation permitted")
	}
	w = request(r, "/api/route/bind", body, token)
	if w.Code != 200 || s.binds != 1 {
		t.Fatal("authenticated management compatibility broken")
	}
}
func TestAdminRoutesFailClosedWithoutConfiguredSecret(t *testing.T) {
	r, s := testRouter(RouterConfig{})
	w := request(r, "/api/route/bind", `{"user_id":"u","server_id":"t"}`, "anything")
	if w.Code != 401 || s.binds != 0 {
		t.Fatal("unconfigured admin API accepted request")
	}
}
func TestUntrustedIPHeadersAndBodyAreIgnored(t *testing.T) {
	r, s := testRouter(RouterConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"phoneNumber":"13812345678","password":"d","platform":2,"ip":"1.1.1.1"}`))
	req.RemoteAddr = "203.0.113.9:2222"
	req.Header.Set("X-Real-IP", "2.2.2.2")
	req.Header.Set("X-Forwarded-For", "3.3.3.3")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(httptest.NewRecorder(), req)
	if s.loginIP != "203.0.113.9" {
		t.Fatalf("spoofed source IP trusted: %s", s.loginIP)
	}
}
func TestVerifyForwardsPurposeEmailAndInvitation(t *testing.T) {
	r, s := testRouter(RouterConfig{})
	request(r, "/api/verify/check", `{"email":"a@example.com","verifyCode":"123456","usedFor":1,"invitationCode":"tenant-a"}`, "")
	if s.verify == nil || s.verify.Email != "a@example.com" || s.verify.UsedFor != 1 || s.verify.InvitationCode != "tenant-a" {
		t.Fatal("verification routing context dropped")
	}
}
func TestAnonymousClientConfigUsesTenantConfiguration(t *testing.T) {
	r, _ := testRouter(RouterConfig{})
	w := request(r, "/api/client_config/get", `{}`, "")
	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	config := result["data"].(map[string]interface{})["config"].(map[string]interface{})
	if config["needVerificationCodeRegister"] != "0" {
		t.Fatal("tenant config replaced by route defaults")
	}
}
func TestVerifyIPLimitCannotBeBypassedByForgedForwardingHeader(t *testing.T) {
	r, _ := testRouter(RouterConfig{VerifyRequestsPerMinute: 2})
	for i := 0; i < 3; i++ {
		w := request(r, "/api/verify/send", `{"phoneNumber":"13812345678","usedFor":3}`, "")
		if i == 2 && w.Code != 429 {
			t.Fatal("verification request rate limit not enforced")
		}
	}
}
func TestRequestBodyIsBounded(t *testing.T) {
	r, _ := testRouter(RouterConfig{})
	w := request(r, "/api/auth/login", `{"password":"`+strings.Repeat("a", 65<<10)+`"}`, "")
	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	if result["errCode"] != float64(400) {
		t.Fatal("oversized body accepted")
	}
}
func TestCORSAllowsOnlyConfiguredOrigin(t *testing.T) {
	r, _ := testRouter(RouterConfig{AllowedOrigins: []string{"https://app.example"}})
	for _, origin := range []string{"https://app.example", "https://evil.example"} {
		req := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if origin == "https://evil.example" {
			if w.Code != 403 || w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("untrusted CORS origin accepted")
			}
		} else if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatal("configured Web origin rejected")
		}
	}
}
