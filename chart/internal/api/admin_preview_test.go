package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/proto/admin"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type previewAdminStub struct {
	admin.AdminClient
	identity string
	level    int32
}

func (s previewAdminStub) GetAdminInfo(context.Context, *admin.GetAdminInfoReq, ...grpc.CallOption) (*admin.GetAdminInfoResp, error) {
	return &admin.GetAdminInfoResp{UserID: s.identity, Level: s.level}, nil
}

type credentialAuditFixture struct {
	beginFail, finishFail bool
	began, finished       int
}

func (a *credentialAuditFixture) Begin(context.Context, string, string, int32) (string, error) {
	a.began++
	if a.beginFail {
		return "", errors.New("audit unavailable")
	}
	return "audit", nil
}
func (a *credentialAuditFixture) Finish(context.Context, string, string) error {
	a.finished++
	if a.finishFail {
		return errors.New("audit unavailable")
	}
	return nil
}
func previewContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/admin/message_preview/search", strings.NewReader(body))
	c.Request.Header.Set("token", "fixture-chart-session")
	SetToken(c, "a1", constant.AdminUser)
	return c, w
}
func responseCode(t *testing.T, w *httptest.ResponseRecorder) int {
	t.Helper()
	var response struct {
		ErrCode int `json:"errCode"`
	}
	if json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatal("invalid API envelope")
	}
	return response.ErrCode
}
func TestAdminReadonlyPreviewScopeAndDelegatedHTTPHeaders(t *testing.T) {
	caller := &tagCallerStub{}
	a := &AdminApi{adminClient: previewAdminStub{identity: "a1", level: 80}, imApiCaller: caller}
	t.Setenv("LUOBO_MESSAGE_PREVIEW_ADMIN_IDS", "[]")
	c, w := previewContext(`{"userID":"target","conversationID":"si_peer_target"}`)
	a.adminPreview("search")(c)
	if responseCode(t, w) != 1002 {
		t.Fatal("ordinary ungranted admin obtained read permission")
	}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/admin/message_preview/search" || r.Header.Get("token") != "admin-token" || r.Header.Get("X-Luobo-Chart-Admin-Token") != "fixture-chart-session" {
			t.Error("preview request lost service or actual administrator credential")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["userID"] != "target" {
			t.Error("preview target changed")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"errCode":0,"data":{"messages":[],"hasMore":false,"nextCursor":"","scannedCount":0,"unsearchableCount":0,"searchComplete":true}}`))
	}))
	defer upstream.Close()
	old := config.Config.OpenIMUrl
	config.Config.OpenIMUrl = upstream.URL
	defer func() { config.Config.OpenIMUrl = old }()
	t.Setenv("LUOBO_MESSAGE_PREVIEW_ADMIN_IDS", `["a1"]`)
	c, w = previewContext(`{"userID":"target","conversationID":"si_peer_target"}`)
	a.adminPreview("search")(c)
	if responseCode(t, w) != 0 || calls != 1 || !strings.Contains(w.Body.String(), `"messages":[]`) {
		t.Fatal("granted read-only HTTP preview failed")
	}
	c, w = previewContext(`{"userID":"target","adminUserID":"forged"}`)
	a.adminPreview("search")(c)
	if responseCode(t, w) != 1001 || calls != 1 {
		t.Fatal("caller overrode administrator identity")
	}
	a.adminClient = previewAdminStub{identity: "other", level: 100}
	c, w = previewContext(`{"userID":"target"}`)
	a.adminPreview("search")(c)
	if responseCode(t, w) != 1002 || calls != 1 {
		t.Fatal("RPC administrator mismatch accepted")
	}
}
func TestLegacyFullUserCredentialIssuanceIsExplicitAndAudited(t *testing.T) {
	caller := &adminIdentityCallerStub{}
	audit := &credentialAuditFixture{}
	a := &AdminApi{adminClient: previewAdminStub{identity: "a1", level: 100}, imApiCaller: caller, credentialAudit: audit}
	t.Setenv("LUOBO_ENABLE_LEGACY_USER_TOKEN_ISSUANCE", "")
	c, w := previewContext(`{"userID":"target"}`)
	a.GetIMUserToken(c)
	if responseCode(t, w) != 1002 || len(caller.mintedFor) != 0 {
		t.Fatal("legacy full-token issuance default enabled")
	}
	t.Setenv("LUOBO_ENABLE_LEGACY_USER_TOKEN_ISSUANCE", "true")
	a.adminClient = previewAdminStub{identity: "a1", level: 80}
	c, w = previewContext(`{"userID":"target"}`)
	a.GetIMUserToken(c)
	if responseCode(t, w) != 1002 || len(caller.mintedFor) != 0 {
		t.Fatal("normal admin gained legacy full credential")
	}
	a.adminClient = previewAdminStub{identity: "a1", level: 100}
	audit.beginFail = true
	c, w = previewContext(`{"userID":"target"}`)
	a.GetIMUserToken(c)
	if responseCode(t, w) == 0 || len(caller.mintedFor) != 0 {
		t.Fatal("issued full credential before durable audit")
	}
	audit.beginFail = false
	audit.finishFail = true
	c, w = previewContext(`{"userID":"target"}`)
	a.GetIMUserToken(c)
	if responseCode(t, w) == 0 || strings.Contains(w.Body.String(), "fixture-admin-im-token") {
		t.Fatal("returned full credential without audit completion")
	}
	audit.finishFail = false
	c, w = previewContext(`{"userID":"target"}`)
	a.GetIMUserToken(c)
	if responseCode(t, w) != 0 || !strings.Contains(w.Body.String(), "fixture-admin-im-token") {
		t.Fatal("explicit superadmin legacy workflow failed")
	}
}
