package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"openim-route-service/model"
)

type fakeRoutes struct {
	accounts map[string]*model.RouteResponse
	invites  map[string]*model.RouteResponse
	bindErr  error
	binds    int
}

func (f *fakeRoutes) GetServerByUserID(key string) (*model.RouteResponse, error) {
	v, ok := f.accounts[key]
	if !ok {
		return nil, ErrAccountNotBound
	}
	return v, nil
}
func (f *fakeRoutes) GetServerByInviteCode(code string) (*model.RouteResponse, error) {
	v, ok := f.invites[code]
	if !ok {
		return nil, errors.New("invalid invitation")
	}
	return v, nil
}
func (f *fakeRoutes) BindUserToServer(key, id, code string) error {
	f.binds++
	if f.bindErr != nil {
		return f.bindErr
	}
	f.accounts[key] = f.invites[code]
	return nil
}
func (f *fakeRoutes) GetAllServers() ([]model.ServerConfig, error) { return nil, nil }

type forwardCall struct {
	path    string
	payload map[string]interface{}
	ip      string
}
type fakeForward struct {
	calls []forwardCall
	reply map[string]interface{}
}

func (f *fakeForward) Post(_ context.Context, _ *model.RouteResponse, path string, payload any, ip, _ string) (map[string]interface{}, error) {
	b, _ := json.Marshal(payload)
	var data map[string]interface{}
	json.Unmarshal(b, &data)
	f.calls = append(f.calls, forwardCall{path, data, ip})
	if f.reply != nil {
		return f.reply, nil
	}
	return map[string]interface{}{"errCode": float64(0), "data": map[string]interface{}{"userID": "u", "imToken": "im", "chatToken": "chat"}}, nil
}
func fixture() (*fakeRoutes, *fakeForward) {
	tenant := &model.RouteResponse{ServerID: "a", ServerName: "tenant A", Domains: []string{"tenant.example"}}
	return &fakeRoutes{accounts: map[string]*model.RouteResponse{}, invites: map[string]*model.RouteResponse{"": tenant, "invite-a": tenant}}, &fakeForward{}
}
func TestEmailCodeLoginReachesTenantWithRealCode(t *testing.T) {
	routes, forward := fixture()
	var req LoginRequest
	if err := json.Unmarshal([]byte(`{"email":"A@example.com","verificationCode":"182736","password":null,"platform":2,"deviceID":"device","ip":"forged"}`), &req); err != nil {
		t.Fatal(err)
	}
	account := Account{Email: "a@example.com"}
	routes.accounts[account.Key()] = routes.invites[""]
	result, err := (&LoginService{routes, forward}).Login(context.Background(), &req, "203.0.113.7", "op")
	if err != nil {
		t.Fatal(err)
	}
	call := forward.calls[0]
	if call.path != "/chat/account/login" || call.payload["email"] != "a@example.com" || call.payload["verifyCode"] != "182736" || call.payload["ip"] != "203.0.113.7" {
		t.Fatalf("incorrect forwarding: %+v", call)
	}
	if _, exists := call.payload["verificationCode"]; exists {
		t.Fatal("alias leaked to tenant")
	}
	info := result["data"].(map[string]interface{})["serverInfo"].(map[string]interface{})
	if info["serverID"] != "a" {
		t.Fatal("missing tenant identity")
	}
}
func TestLoginRejectsConflictingCredentialsBeforeNetwork(t *testing.T) {
	for _, body := range []string{`{"phoneNumber":"13812345678","password":"p","verifyCode":"123456","platform":2}`, `{"email":"a@example.com","verifyCode":"123456","verificationCode":"999999","platform":2}`, `{"phoneNumber":"13812345678","email":"a@example.com","password":"p","platform":2}`} {
		routes, forward := fixture()
		var req LoginRequest
		json.Unmarshal([]byte(body), &req)
		if _, err := (&LoginService{routes, forward}).Login(context.Background(), &req, "1.2.3.4", "op"); err == nil {
			t.Fatal("ambiguous login accepted")
		}
		if len(forward.calls) > 0 {
			t.Fatal("invalid login reached tenant")
		}
	}
}
func TestVerificationPrecheckThenRegisterUsesSameTenantCode(t *testing.T) {
	routes, forward := fixture()
	verify := &VerifyService{routes, forward}
	req := VerifyRequest{Account: Account{PhoneNumber: "13812345678"}, UsedFor: 1, InvitationCode: "invite-a", VerifyCode: "731264"}
	if _, err := verify.Verify(context.Background(), &req, false, "1.2.3.4", "send"); err != nil {
		t.Fatal(err)
	}
	if _, err := verify.Verify(context.Background(), &req, true, "1.2.3.4", "check"); err != nil {
		t.Fatal(err)
	}
	register := RegisterRequest{VerifyCode: "731264", InvitationCode: "invite-a", Platform: 2, AutoLogin: true, User: map[string]interface{}{"phoneNumber": "13812345678", "nickname": "萝卜", "password": "digest"}}
	if _, err := (&RegisterService{routes, forward, &fakeRegistrationStore{routes: routes}}).Register(context.Background(), &register, "1.2.3.4", "register"); err != nil {
		t.Fatal(err)
	}
	want := []string{"/chat/account/code/send", "/chat/account/code/verify", "/chat/account/register"}
	for i, call := range forward.calls {
		if call.path != want[i] || call.payload["verifyCode"] != "731264" {
			t.Fatalf("code/target changed in operation %d", i)
		}
	}
	if routes.binds != 1 {
		t.Fatal("registration did not persist routing")
	}
}
func TestExistingAccountCannotUseInvitationToSwitchTenant(t *testing.T) {
	routes, forward := fixture()
	routes.accounts["13812345678"] = routes.invites[""]
	routes.invites["other"] = &model.RouteResponse{ServerID: "b", Domains: []string{"other.example"}}
	v := VerifyRequest{Account: Account{PhoneNumber: "13812345678"}, UsedFor: 1, InvitationCode: "other"}
	if result, err := (&VerifyService{routes, forward}).Verify(context.Background(), &v, false, "1.2.3.4", ""); err != nil || !successful(result) {
		t.Fatal("registration send disclosed existing binding")
	}
	req := RegisterRequest{InvitationCode: "other", Platform: 2, User: map[string]interface{}{"phoneNumber": "13812345678"}}
	if _, err := (&RegisterService{routes, forward, &fakeRegistrationStore{routes: routes}}).Register(context.Background(), &req, "1.2.3.4", ""); err == nil {
		t.Fatal("cross-tenant register accepted")
	}
	if len(forward.calls) != 0 {
		t.Fatal("cross-tenant operation reached upstream")
	}
}
func TestUnknownAccountSendDoesNotDiscloseBinding(t *testing.T) {
	for _, purpose := range []int32{2, 3} {
		routes, forward := fixture()
		req := VerifyRequest{Account: Account{PhoneNumber: "13812345678"}, UsedFor: purpose}
		result, err := (&VerifyService{routes, forward}).Verify(context.Background(), &req, false, "1.2.3.4", "")
		if err != nil || !successful(result) {
			t.Fatalf("unknown account disclosed: %v", err)
		}
		if len(forward.calls) != 0 {
			t.Fatal("unknown account sent to arbitrary tenant")
		}
	}
}
func TestRegistrationNoCodeDefersToTenantConfig(t *testing.T) {
	routes, forward := fixture()
	req := RegisterRequest{Platform: 2, User: map[string]interface{}{"email": "a@example.com", "nickname": "萝卜", "password": "digest"}}
	if _, err := (&RegisterService{routes, forward, &fakeRegistrationStore{routes: routes}}).Register(context.Background(), &req, "1.2.3.4", ""); err != nil {
		t.Fatal(err)
	}
	if forward.calls[0].payload["verifyCode"] != "" {
		t.Fatal("gateway substituted a universal code")
	}
}
func TestRegistrationBindingFailureIsNotReportedAsUsableAccount(t *testing.T) {
	routes, forward := fixture()
	routes.bindErr = errors.New("database down")
	req := RegisterRequest{Platform: 2, User: map[string]interface{}{"phoneNumber": "13812345678", "nickname": "萝卜"}}
	result, err := (&RegisterService{routes, forward, &fakeRegistrationStore{routes: routes}}).Register(context.Background(), &req, "1.2.3.4", "")
	if err == nil || result != nil || !strings.Contains(err.Error(), "注册") {
		t.Fatal("binding failure hidden")
	}
}
func TestEmailResetForwardsOriginalCodeWithoutBypass(t *testing.T) {
	routes, forward := fixture()
	routes.accounts[(Account{Email: "a@example.com"}).Key()] = routes.invites[""]
	req := ResetPasswordRequest{Account: Account{Email: "a@example.com"}, VerifyCode: "927364", Password: "new-digest", Platform: 2}
	if _, err := (&PasswordService{routes, forward}).ResetPassword(context.Background(), &req, "1.2.3.4", ""); err != nil {
		t.Fatal(err)
	}
	c := forward.calls[0]
	if c.path != "/chat/account/password/reset" || c.payload["verifyCode"] != "927364" || c.payload["email"] != "a@example.com" {
		t.Fatal("reset code or email lost")
	}
}
func TestAccountNormalizationPreservesLegacyAndIsolatesCountry(t *testing.T) {
	a := Account{PhoneNumber: "13812345678"}
	if err := a.Normalize(); err != nil {
		t.Fatal(err)
	}
	if a.Key() != "phone:+86:13812345678" {
		t.Fatal("new phone binding omits country")
	}
	b := Account{PhoneNumber: "13812345678", AreaCode: "852"}
	b.Normalize()
	if b.Key() == a.Key() {
		t.Fatal("country-code accounts collide")
	}
	mail := Account{PhoneNumber: "A@example.com"}
	if err := mail.Normalize(); err != nil {
		t.Fatal(err)
	}
	if mail.Email != "a@example.com" || len(mail.Key()) > 50 {
		t.Fatal("legacy email send/canonical key broken")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestForwarderUsesHTTPSAndNeverFollowsRedirect(t *testing.T) {
	f := NewHTTPForwarder()
	if f.Client.CheckRedirect == nil {
		t.Fatal("redirects could leak credentials")
	}
	f.Client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Scheme != "https" || req.URL.Host != "tenant.example" || req.URL.Path != "/chat/account/login" {
			t.Fatal("wrong upstream")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errCode":0,"data":{}}`)), Header: make(http.Header)}, nil
	})
	if _, err := f.Post(context.Background(), &model.RouteResponse{Domains: []string{"tenant.example"}}, "/chat/account/login", map[string]string{"password": "secret"}, "1.2.3.4", "op"); err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"user:pass@evil.example", "tenant.example/path", "tenant.example?x=1", "tenant.example:8443", "tenant.example\r\nx:y"} {
		if _, err := domainURL(domain, ""); err == nil {
			t.Fatalf("unsafe domain %q accepted", domain)
		}
	}
}
func TestForwarderRejectsMalformedStatusOrOversizeResponse(t *testing.T) {
	for _, body := range []string{`{"data":{}}`, strings.Repeat("x", (2<<20)+1)} {
		f := NewHTTPForwarder()
		f.Client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		if _, err := f.Post(context.Background(), &model.RouteResponse{Domains: []string{"tenant.example"}}, "/chat/account/login", nil, "", ""); err == nil {
			t.Fatal("invalid response accepted")
		}
	}
}
func TestUpstreamTransportErrorsDoNotLeakCredentialURLs(t *testing.T) {
	f := NewHTTPForwarder()
	f.Client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("https://example/?password=secret")
	})
	_, err := f.Post(context.Background(), &model.RouteResponse{Domains: []string{"tenant.example"}}, "/chat/account/login", nil, "", "")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("transport leaked credential material")
	}
}

func TestLegacyInternationalPhoneBindingUsesExactTenantCredentials(t *testing.T) {
	routes, forward := fixture()
	routes.accounts["12345678"] = routes.invites[""]
	req := LoginRequest{Account: Account{AreaCode: "+852", PhoneNumber: "12345678"}, Password: "digest", Platform: 2}
	if _, err := (&LoginService{routes, forward}).Login(context.Background(), &req, "1.2.3.4", ""); err != nil {
		t.Fatal(err)
	}
	if forward.calls[0].payload["areaCode"] != "+852" || routes.binds != 0 {
		t.Fatal("legacy ownership was silently reassigned")
	}
}

// This fake exercises orchestration only. Transaction and capacity behavior is
// covered separately with SQL expectations and the opt-in isolated MySQL test.
type fakeRegistrationStore struct {
	routes      *fakeRoutes
	attempt     *RegistrationAttempt
	confirmErr  error
	completeErr error
	confirmed   string
	completed   int
	aborted     int
}

func (f *fakeRegistrationStore) Prepare(_ context.Context, key, code string) (*RegistrationAttempt, error) {
	if f.attempt != nil {
		return f.attempt, nil
	}
	server, err := f.routes.GetServerByInviteCode(code)
	if err != nil {
		return nil, err
	}
	return &RegistrationAttempt{Intent: model.RegistrationIntent{RegistrationID: "82ea3843-1abf-4d39-887c-0791408b7af8", AccountKey: key, ServerID: server.ServerID, InviteCode: code, CreatedAt: time.Now()}, Server: server, Fresh: true}, nil
}
func (f *fakeRegistrationStore) Confirm(_ context.Context, id, userID string) error {
	f.confirmed = userID
	return f.confirmErr
}
func (f *fakeRegistrationStore) Complete(_ context.Context, id string) error {
	f.completed++
	if f.completeErr != nil {
		return f.completeErr
	}
	if f.routes != nil {
		return f.routes.BindUserToServer("phone:+86:13812345678", "a", "")
	}
	return nil
}
func (f *fakeRegistrationStore) Abort(context.Context, string) error { f.aborted++; return nil }
