package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/gin-gonic/gin"
)

type cancelDBStub struct {
	database.ChatDatabaseInterface
	password string
}

func (d cancelDBStub) GetAccount(context.Context, string) (*table.Account, error) {
	return &table.Account{Password: d.password}, nil
}
func (d cancelDBStub) GetAttribute(context.Context, string) (*table.Attribute, error) {
	return &table.Attribute{}, nil
}

type cancelStoreStub struct{ requested string }

func (s *cancelStoreStub) Request(_ context.Context, id, _ string) (*accountstate.Cancellation, error) {
	s.requested = id
	return &accountstate.Cancellation{UserID: id, RequestedAt: time.Now()}, nil
}
func (*cancelStoreStub) Complete(context.Context, string, time.Time) error { return nil }
func (*cancelStoreStub) ListAfter(context.Context, string, int) ([]*accountstate.Cancellation, error) {
	return nil, nil
}

type cancelSessionsStub struct{}

func (cancelSessionsStub) Disable(context.Context, string) error { return nil }
func (cancelSessionsStub) Revoke(context.Context, string) error  { return nil }

func TestAccountCancellationAuthorizationAndResult(t *testing.T) {
	const password = "0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name, actor, target, password string
		role                          int32
		accepted                      bool
	}{
		{"owner", "u1", "", password, constant.NormalUser, true},
		{"other user", "u1", "u2", password, constant.NormalUser, false},
		{"wrong password", "u1", "", "abcdef0123456789abcdef0123456789", constant.NormalUser, false},
		{"admin token", "a1", "", password, constant.AdminUser, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &cancelStoreStub{}
			a := &AccountCancellationApi{db: cancelDBStub{password: password}, service: &accountstate.Service{Store: store, Sessions: cancelSessionsStub{}}}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			body, _ := json.Marshal(map[string]string{"userID": tc.target, "password": tc.password})
			c.Request = httptest.NewRequest("POST", "/account/cancel", strings.NewReader(string(body)))
			SetToken(c, tc.actor, tc.role)
			a.Cancel(c)
			var result struct {
				ErrCode int `json:"errCode"`
				Data    struct {
					Status     string `json:"status"`
					DataErased bool   `json:"dataErased"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if (result.ErrCode == 0) != tc.accepted || (store.requested != "") != tc.accepted {
				t.Fatalf("unexpected authorization result: %s", w.Body.String())
			}
			if tc.accepted && (result.Data.Status != "sessions_revoked" || result.Data.DataErased) {
				t.Fatal("incorrect cancellation state or false erasure claim")
			}
		})
	}
}
