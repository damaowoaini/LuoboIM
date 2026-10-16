package admin

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/admin"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/proto/admin"
	"github.com/OpenIMSDK/protocol/wrapperspb"
)

type adminDBStub struct {
	database.AdminDatabaseInterface
	account          *table.Admin
	update           map[string]any
	changed, revoked bool
}

func (d *adminDBStub) GetAdminUserID(context.Context, string) (*table.Admin, error) {
	return d.account, nil
}
func (d *adminDBStub) UpdateAdmin(_ context.Context, _ string, update map[string]any) error {
	d.update = update
	return nil
}
func (d *adminDBStub) ChangePassword(_ context.Context, _ string, password string) error {
	d.changed = true
	d.account.Password = password
	return nil
}
func (d *adminDBStub) DeleteUserToken(context.Context, string) error { d.revoked = true; return nil }

func TestAdminProfileCannotPromoteItself(t *testing.T) {
	db := &adminDBStub{account: &table.Admin{UserID: "a1", Level: 80, Nickname: "before", FaceURL: "avatar"}}
	s := &adminServer{Database: db}
	ctx := mctx.WithOpUserID(context.Background(), "a1", constant.AdminUser)
	_, err := s.AdminUpdateInfo(ctx, &admin.AdminUpdateInfoReq{Level: wrapperspb.Int32(100), Nickname: wrapperspb.String("after")})
	if err == nil || db.update != nil {
		t.Fatal("normal admin could promote itself")
	}
	resp, err := s.AdminUpdateInfo(ctx, &admin.AdminUpdateInfoReq{Level: wrapperspb.Int32(80), Nickname: wrapperspb.String("after")})
	if err != nil || db.update["nickname"] != "after" {
		t.Fatalf("normal profile edit failed: %v", err)
	}
	if _, ok := db.update["level"]; ok {
		t.Fatal("profile update wrote privilege level")
	}
	if resp.FaceURL != "avatar" {
		t.Fatal("unchanged avatar was lost")
	}
}

func TestAdminProfileCannotChangePassword(t *testing.T) {
	if _, err := ToDBAdminUpdate(&admin.AdminUpdateInfoReq{Password: wrapperspb.String("unverified")}); err == nil {
		t.Fatal("profile endpoint bypassed current password validation")
	}
}

func TestAdminPasswordChangeIsSelfOnlyAndRevokesTokens(t *testing.T) {
	oldPassword, newPassword := "0123456789abcdef0123456789abcdef", "abcdef0123456789abcdef0123456789"
	db := &adminDBStub{account: &table.Admin{UserID: "a1", Password: oldPassword}}
	s := &adminServer{Database: db}
	ctx := mctx.WithOpUserID(context.Background(), "a1", constant.AdminUser)
	if _, err := s.ChangeAdminPassword(ctx, &admin.ChangeAdminPasswordReq{UserID: "a2", CurrentPassword: oldPassword, NewPassword: newPassword}); err == nil {
		t.Fatal("admin changed another admin password")
	}
	if _, err := s.ChangeAdminPassword(ctx, &admin.ChangeAdminPasswordReq{CurrentPassword: "wrong", NewPassword: newPassword}); err == nil {
		t.Fatal("wrong current password accepted")
	}
	if _, err := s.ChangeAdminPassword(ctx, &admin.ChangeAdminPasswordReq{CurrentPassword: oldPassword, NewPassword: newPassword}); err != nil {
		t.Fatal(err)
	}
	if !db.changed || !db.revoked || db.account.Password != newPassword {
		t.Fatal("password protocol or token revocation incorrect")
	}
}

func TestAdminInfoDoesNotReturnPasswordHash(t *testing.T) {
	s := &adminServer{Database: &adminDBStub{account: &table.Admin{UserID: "a1", Password: "secret-digest"}}}
	resp, err := s.GetAdminInfo(mctx.WithOpUserID(context.Background(), "a1", constant.AdminUser), &admin.GetAdminInfoReq{})
	if err != nil || resp.Password != "" {
		t.Fatal("admin password digest disclosed")
	}
}
