package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/registration"
	"github.com/OpenIMSDK/chat/pkg/proto/admin"
	"github.com/OpenIMSDK/chat/pkg/proto/chat"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

type publicOutboxClient struct {
	chat.ChatClient
	pending      bool
	state        string
	failure      error
	missingOwner bool
}

func (s publicOutboxClient) RegisterUser(context.Context, *chat.RegisterUserReq, ...grpc.CallOption) (*chat.RegisterUserResp, error) {
	if s.failure != nil {
		return nil, s.failure
	}
	if s.missingOwner {
		return &chat.RegisterUserResp{ProvisioningReceiptID: "unconfirmed-response", ProvisioningState: "awaiting_im", ProvisioningPending: true}, nil
	}
	return &chat.RegisterUserResp{UserID: "public-owner", ProvisioningReceiptID: "internal-receipt", ProvisioningState: s.state, ProvisioningPending: s.pending}, nil
}

func TestPublicOutboxKeepsOldAutoLoginAndRefusesPendingOrUnknownState(t *testing.T) {
	for _, tc := range []struct {
		name, state                 string
		pending, autologin, success bool
	}{
		{"direct autoLogin", "direct_only", false, true, true},
		{"route autoLogin", "bound", false, true, true},
		{"legacy non autoLogin", "direct_only", false, false, true},
		{"pending must not issue credentials", "awaiting_im", true, true, false},
		{"conflict must not issue credentials", "conflict", true, true, false},
		{"inconsistent unknown response fails closed", "", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			confirmed := tc.success
			api := &ChatApi{chatClient: publicOutboxClient{state: tc.state, pending: tc.pending}}
			if tc.success && tc.autologin {
				api.adminClient = postIMTokenIssuer{confirmed: &confirmed, t: t}
				api.imApiCaller = publicCreationIM{confirmed: &confirmed, t: t}
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			body := `{"platform":2,"autoLogin":` + map[bool]string{true: "true", false: "false"}[tc.autologin] + `,"user":{"email":"public@fixture.invalid"}}`
			c.Request = httptest.NewRequest("POST", "/account/register", strings.NewReader(body))
			api.RegisterUser(c)
			var out struct {
				ErrCode int `json:"errCode"`
				Data    struct {
					UserID        string `json:"userID"`
					Chat          string `json:"chatToken"`
					IM            string `json:"imToken"`
					Pending       bool   `json:"registrationPending"`
					RequiresLogin bool   `json:"requiresLogin"`
				} `json:"data"`
			}
			if json.Unmarshal(w.Body.Bytes(), &out) != nil || (out.ErrCode == 0) != tc.success {
				t.Fatal("public result or pending boundary incorrect")
			}
			if tc.success && out.Data.UserID != "public-owner" {
				t.Fatal("owner response changed")
			}
			if tc.success && tc.autologin && (out.Data.Chat != "synthetic-chat" || out.Data.IM != "synthetic-IM") {
				t.Fatal("autoLogin lost original dual tokens")
			}
			if (!tc.success || !tc.autologin) && (out.Data.Chat != "" || out.Data.IM != "") {
				t.Fatal("unconfirmed public request returned credentials")
			}
			if !tc.success && (!out.Data.Pending || !out.Data.RequiresLogin || out.ErrCode != 1002) {
				t.Fatal("committed pending response lacks structured recovery state")
			}
		})
	}
}

type incompleteRegistrationTokenIssuer struct {
	admin.AdminClient
	failure error
	empty   bool
}

func (s incompleteRegistrationTokenIssuer) CreateToken(context.Context, *admin.CreateTokenReq, ...grpc.CallOption) (*admin.CreateTokenResp, error) {
	if s.empty {
		return nil, nil
	}
	return nil, s.failure
}

type incompleteRegistrationIMToken struct {
	apicall.CallerInterface
	failure error
	empty   bool
}

func (s incompleteRegistrationIMToken) UserToken(context.Context, string, int32) (string, error) {
	if s.empty {
		return "", nil
	}
	return "", s.failure
}
func TestCommittedRegistrationTokenFailuresKeepRecoveryStateWithoutPartialTokens(t *testing.T) {
	for _, kind := range []string{"chat error", "chat missing", "IM error", "IM missing"} {
		t.Run(kind, func(t *testing.T) {
			api := &ChatApi{}
			chatToken := ""
			expected := 500
			switch kind {
			case "chat error":
				api.adminClient = incompleteRegistrationTokenIssuer{failure: errs.ErrArgs.Wrap("private dependency detail")}
				expected = 1001
			case "chat missing":
				api.adminClient = incompleteRegistrationTokenIssuer{empty: true}
			case "IM error":
				chatToken = "synthetic-issued-chat"
				api.imApiCaller = incompleteRegistrationIMToken{failure: errs.ErrNoPermission.Wrap("private upstream failure")}
				expected = 1002
			case "IM missing":
				chatToken = "synthetic-issued-chat"
				api.imApiCaller = incompleteRegistrationIMToken{empty: true}
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/account/register", nil)
			api.completePublicRegistration(c, "committed-owner", chatToken, 2, true)
			var out struct {
				ErrCode int    `json:"errCode"`
				ErrMsg  string `json:"errMsg"`
				Data    struct {
					Pending       bool   `json:"registrationPending"`
					RequiresLogin bool   `json:"requiresLogin"`
					Chat          string `json:"chatToken"`
					IM            string `json:"imToken"`
				} `json:"data"`
			}
			if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.ErrCode != expected || out.ErrMsg != registrationPendingMessage || !out.Data.Pending || !out.Data.RequiresLogin || out.Data.Chat != "" || out.Data.IM != "" || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "synthetic-issued-chat") {
				t.Fatal("token completion error lost committed state or exposed a credential")
			}
		})
	}
	for _, code := range []error{errs.ErrArgs.Wrap("before-or-after-transaction unknown"), errs.ErrNoPermission.Wrap("before-or-after-transaction unknown")} {
		api := &ChatApi{chatClient: publicOutboxClient{failure: code}}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/account/register", strings.NewReader(`{"platform":2,"autoLogin":true,"user":{"email":"public@fixture.invalid"}}`))
		api.RegisterUser(c)
		if strings.Contains(w.Body.String(), "registrationPending") {
			t.Fatal("numeric error alone asserted durable commitment")
		}
	}
	api := &ChatApi{chatClient: publicOutboxClient{missingOwner: true}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/account/register", strings.NewReader(`{"platform":2,"autoLogin":true,"user":{"email":"public@fixture.invalid"}}`))
	api.RegisterUser(c)
	if strings.Contains(w.Body.String(), "registrationPending") {
		t.Fatal("unconfirmed owner asserted durable commitment")
	}
}

func TestSignedReceiptRetryCannotBypassDurablePublicWorker(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/internal/registration/receipt", nil)
	// Nil database/caller make any legacy synchronous create or write fail: the
	// managed receipt must immediately defer to the existing durable worker.
	receipt := &registration.Receipt{ManagedByOutbox: true, RegistrationID: "synthetic"}
	if e := (&registrationAPI{}).ensureIM(c, receipt); e == nil || receipt.IMProvisionedAt != nil {
		t.Fatal("route retry bypassed durable owner validation")
	}
}
