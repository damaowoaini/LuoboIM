package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	"github.com/OpenIMSDK/chat/pkg/common/adminprovision"
	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	admintable "github.com/OpenIMSDK/chat/pkg/common/db/table/admin"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	padmin "github.com/OpenIMSDK/chat/pkg/proto/admin"
	"github.com/OpenIMSDK/chat/pkg/proto/chat"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"google.golang.org/grpc"
)

func TestAdminProvisioningHTTPRecoveryMFAOwnerAndNoBrowserIdentity(t *testing.T) {
	a, rdb, id, session := delegationFixture(t)
	db := a.delegation.DB
	if e := db.AutoMigrate(&table.Account{}, &table.Attribute{}, &accountstate.Cancellation{}, &adminprovision.Intent{}, &adminprovision.Job{}, &adminprovision.RecoveryAudit{}); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	target := "synthetic-target"
	if db.Create(&table.Account{UserID: target, OperatorUserID: "old-bootstrap", CreateTime: now, ChangeTime: now}).Error != nil || db.Create(&table.Attribute{UserID: target, Email: "canonical@fixture.invalid", CreateTime: now, ChangeTime: now, BirthTime: now}).Error != nil {
		t.Fatal("synthetic target failed")
	}
	finds := 0
	a.provisioning = &adminprovision.Service{DB: db, ServerID: "tenant-a", RouteEnabled: true, Route: &adminprovision.RouteClient{}, FindExisting: func(_ context.Context, user string) (*adminprovision.User, error) {
		finds++
		return &adminprovision.User{ID: user, CreateTime: now.UnixMilli()}, nil
	}}
	invoke := func(path, body, token string) (int, json.RawMessage) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/"+path, strings.NewReader(body))
		c.Request.Header.Set("token", token)
		c.Request.Header.Set("X-Admin-ID", "forged-super")
		SetToken(c, id, 2)
		if path == "recover" {
			a.RecoverAdminProvisioning(c)
		} else if path == "resolve" {
			a.ResolveAdminProvisioning(c)
		} else {
			a.AdminProvisioningStatus(c)
		}
		var out struct {
			ErrCode int             `json:"errCode"`
			Data    json.RawMessage `json:"data"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		return out.ErrCode, out.Data
	}
	body := `{"userID":"synthetic-target","reason":"verified original account ownership"}`
	if code, _ := invoke("recover", body, session); code == 0 || finds != 0 {
		t.Fatal("ordinary admin authorized recovery")
	}
	request := uuid.NewString()
	if code, _ := invoke("resolve", `{"adminRequestID":"`+request+`"}`, session); code == 0 {
		t.Fatal("implicit creation cancellation accepted")
	}
	if code, data := invoke("resolve", `{"adminRequestID":"`+request+`","closeIfNotCreated":true}`, session); code != 0 {
		t.Fatal("normal admin cannot stop own unobserved intent")
	} else {
		var closed adminprovision.Status
		json.Unmarshal(data, &closed)
		if !closed.DefinitiveNotCreated || closed.Created {
			t.Fatal("resolution did not persist absence proof")
		}
	}
	db.Model(&admintable.Admin{}).Where("user_id=?", id).Update("level", 100)
	if code, _ := invoke("recover", `{"userID":"synthetic-target","reason":"verified ownership","email":"forged@fixture.invalid"}`, session); code == 0 {
		t.Fatal("browser supplied recovery identity accepted")
	}
	code, data := invoke("recover", body, session)
	var status adminprovision.Status
	json.Unmarshal(data, &status)
	if code != 0 || !status.Created || !status.Retryable || status.State != "pending" {
		t.Fatal("super recovery was not durably accepted")
	}
	var job adminprovision.Job
	db.First(&job)
	var payload adminprovision.Payload
	json.Unmarshal([]byte(job.Payload), &payload)
	if payload.ActorUserID != id || payload.UserID != target || len(payload.Accounts) != 1 || payload.Accounts[0].Email != "canonical@fixture.invalid" || job.OriginalCreatorID != "" {
		t.Fatal("recovery used browser or bootstrap identity")
	}
	var audit adminprovision.RecoveryAudit
	db.First(&audit)
	if !audit.OriginalCreatorUnknown || audit.ActorID != id {
		t.Fatal("recovery audit lost actor/unknown creator")
	}
	db.Model(&admintable.Admin{}).Where("user_id=?", id).Update("google_auth_enabled", false)
	if code, _ := invoke("recover", body, session); code == 0 {
		t.Fatal("MFA disabled session accepted")
	}
	db.Model(&admintable.Admin{}).Where("user_id=?", id).Update("google_auth_enabled", true)
	rdb.HDel(context.Background(), "CHAT_UID_TOKEN_STATUS:"+id, session)
	if code, _ := invoke("status", `{"receiptID":"`+status.ReceiptID+`"}`, session); code == 0 {
		t.Fatal("revoked session read status")
	}
}

type adminCreationStub struct {
	chat.ChatClient
	calls     int
	requestID string
}

type batchCreationStub struct {
	chat.ChatClient
	calls    int
	requests []string
}

func (s *batchCreationStub) RegisterUser(_ context.Context, r *chat.RegisterUserReq, _ ...grpc.CallOption) (*chat.RegisterUserResp, error) {
	s.calls++
	s.requests = append(s.requests, r.AdminRequestID)
	if !r.AdminDefaultsAll {
		return nil, fmt.Errorf("bulk default workflow lost")
	}
	if s.calls%2 == 0 {
		return nil, fmt.Errorf("synthetic unknown ACK")
	}
	return &chat.RegisterUserResp{UserID: "first-owner", AdminRequestID: r.AdminRequestID, ProvisioningReceiptID: uuid.NewString(), ProvisioningState: "pending", ProvisioningPending: true}, nil
}
func TestBatchCreationKeepsStableRowIDsAndReportsPartialUnknown(t *testing.T) {
	stub := &batchCreationStub{}
	a := &AdminApi{chatClient: stub}
	batch := uuid.NewString()
	users := []*chat.RegisterUserInfo{{Email: "first@fixture.invalid"}, {Email: "second@fixture.invalid"}, {Email: "never-reached@fixture.invalid"}}
	for range 2 {
		report, e := a.registerChatUser(context.Background(), "127.0.0.1", users, batch)
		if e != nil || report.Complete || !report.ProvisioningPending || report.FailedIndex == nil || *report.FailedIndex != 1 || len(report.Results) != 2 || report.FailureState != "unconfirmed" || report.AdminRequestID != batch {
			t.Fatal("partial commit misreported", e)
		}
	}
	if len(stub.requests) != 4 || stub.requests[0] != stub.requests[2] || stub.requests[1] != stub.requests[3] || stub.requests[0] == stub.requests[1] {
		t.Fatal("batch retry changed creation intents")
	}
	for _, id := range stub.requests {
		if !adminprovision.ValidID(id) {
			t.Fatal("derived row ID violates route intent contract")
		}
	}
}

func (s *adminCreationStub) RegisterUser(_ context.Context, r *chat.RegisterUserReq, _ ...grpc.CallOption) (*chat.RegisterUserResp, error) {
	s.calls++
	s.requestID = r.AdminRequestID
	return &chat.RegisterUserResp{UserID: "new-owner", AdminRequestID: r.AdminRequestID, ProvisioningReceiptID: uuid.NewString(), ProvisioningState: "pending", ProvisioningPending: true}, nil
}
func TestAdminCreationHTTPPreservesPendingReceiptAndDoesNotCreateIMAgain(t *testing.T) {
	a, rdb, id, session := delegationFixture(t)
	stub := &adminCreationStub{}
	api := &ChatApi{adminSessions: a.delegation, chatClient: stub}
	request := uuid.NewString()
	invoke := func(token string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/account/register", strings.NewReader(`{"platform":10,"adminRequestID":"`+request+`","user":{"email":"synthetic@fixture.invalid","nickname":"fixture"}}`))
		c.Request.Header.Set("token", token)
		SetToken(c, id, 2)
		api.RegisterUser(c)
		var out struct {
			ErrCode int `json:"errCode"`
			Data    struct {
				UserID  string `json:"userID"`
				Pending bool   `json:"provisioningPending"`
				Receipt string `json:"provisioningReceiptID"`
			} `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		if out.ErrCode == 0 && (out.Data.UserID != "new-owner" || !out.Data.Pending || out.Data.Receipt == "") {
			t.Fatal("accepted pending account misreported")
		}
		return out.ErrCode
	}
	// imApiCaller deliberately nil: the authoritative RPC outbox owns this create.
	if invoke(session) != 0 || stub.calls != 1 || stub.requestID != request {
		t.Fatal("stable pending registration failed")
	}
	rdb.HDel(context.Background(), "CHAT_UID_TOKEN_STATUS:"+id, session)
	if invoke(session) == 0 || stub.calls != 1 {
		t.Fatal("stale admin session created another account")
	}
}

type publicCreationClient struct{ chat.ChatClient }

func (publicCreationClient) RegisterUser(context.Context, *chat.RegisterUserReq, ...grpc.CallOption) (*chat.RegisterUserResp, error) {
	return &chat.RegisterUserResp{UserID: "public-owner"}, nil
}

type postIMTokenIssuer struct {
	padmin.AdminClient
	confirmed *bool
	t         *testing.T
}

func (s postIMTokenIssuer) CreateToken(context.Context, *padmin.CreateTokenReq, ...grpc.CallOption) (*padmin.CreateTokenResp, error) {
	if !*s.confirmed {
		s.t.Fatal("chat token issued before IM ownership confirmation")
	}
	return &padmin.CreateTokenResp{Token: "synthetic-chat"}, nil
}
func (postIMTokenIssuer) FindDefaultFriend(context.Context, *padmin.FindDefaultFriendReq, ...grpc.CallOption) (*padmin.FindDefaultFriendResp, error) {
	return &padmin.FindDefaultFriendResp{}, nil
}
func (postIMTokenIssuer) FindDefaultGroup(context.Context, *padmin.FindDefaultGroupReq, ...grpc.CallOption) (*padmin.FindDefaultGroupResp, error) {
	return &padmin.FindDefaultGroupResp{}, nil
}

type publicCreationIM struct {
	apicall.CallerInterface
	confirmed *bool
	t         *testing.T
}

func (s publicCreationIM) RegisterUser(context.Context, []*sdkws.UserInfo) error {
	*s.confirmed = true
	return nil
}
func (publicCreationIM) ImAdminTokenWithDefaultAdmin(context.Context) (string, error) {
	return "synthetic-service", nil
}
func (publicCreationIM) InviteToGroup(context.Context, string, []string) error { return nil }
func (s publicCreationIM) UserToken(context.Context, string, int32) (string, error) {
	if !*s.confirmed {
		s.t.Fatal("IM credential issued before ownership")
	}
	return "synthetic-IM", nil
}
func TestPublicAutoLoginCompletesDualTokensOnlyAfterIMConfirmation(t *testing.T) {
	confirmed := false
	api := &ChatApi{chatClient: publicCreationClient{}, adminClient: postIMTokenIssuer{confirmed: &confirmed, t: t}, imApiCaller: publicCreationIM{confirmed: &confirmed, t: t}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/account/register", strings.NewReader(`{"platform":2,"autoLogin":true,"user":{"email":"public@fixture.invalid"}}`))
	api.RegisterUser(c)
	var out struct {
		ErrCode int `json:"errCode"`
		Data    struct {
			Chat string `json:"chatToken"`
			IM   string `json:"imToken"`
		} `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.ErrCode != 0 || out.Data.Chat != "synthetic-chat" || out.Data.IM != "synthetic-IM" {
		t.Fatal("public autoLogin dual-token compatibility failed")
	}
}
