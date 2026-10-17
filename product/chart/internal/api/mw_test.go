package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/proto/admin"
	imconstant "github.com/OpenIMSDK/protocol/constant"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

type tokenAdminStub struct {
	admin.AdminClient
	response *admin.GetUserTokenResp
	err      error
}

func (s tokenAdminStub) ParseToken(context.Context, *admin.ParseTokenReq, ...grpc.CallOption) (*admin.ParseTokenResp, error) {
	return &admin.ParseTokenResp{UserID: "admin-1", UserType: constant.AdminUser}, nil
}
func (s tokenAdminStub) GetUserToken(context.Context, *admin.GetUserTokenReq, ...grpc.CallOption) (*admin.GetUserTokenResp, error) {
	return s.response, s.err
}

func TestTokenCacheFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stub    tokenAdminStub
		allowed bool
	}{
		{"RPC failure", tokenAdminStub{err: errors.New("redis unavailable")}, false},
		{"nil response", tokenAdminStub{}, false},
		{"missing token", tokenAdminStub{response: &admin.GetUserTokenResp{}}, false},
		{"kicked token", tokenAdminStub{response: &admin.GetUserTokenResp{TokensMap: map[string]int32{"token": imconstant.KickedToken}}}, false},
		{"unknown state", tokenAdminStub{response: &admin.GetUserTokenResp{TokensMap: map[string]int32{"token": 999}}}, false},
		{"active token", tokenAdminStub{response: &admin.GetUserTokenResp{TokensMap: map[string]int32{"token": imconstant.NormalToken}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			err := (&MW{client: tc.stub}).isValidToken(c, "admin-1", "token")
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed = %v, err = %v", tc.allowed, err)
			}
		})
	}
}

func TestOptionalAdminRejectsRevokedToken(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/account/register", nil)
	c.Request.Header.Set("token", "revoked")
	mw := &MW{client: tokenAdminStub{response: &admin.GetUserTokenResp{}}}
	mw.CheckAdminOrNil(c)
	if !c.IsAborted() {
		t.Fatal("a supplied revoked token must abort")
	}
	if _, ok := c.Get(constant.RpcOpUserID); ok {
		t.Fatal("revoked admin received identity")
	}
}

func TestOptionalAdminAllowsAnonymousRegistration(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/account/register", nil)
	(&MW{}).CheckAdminOrNil(c)
	if c.IsAborted() {
		t.Fatal("anonymous registration should retain existing behavior")
	}
}
