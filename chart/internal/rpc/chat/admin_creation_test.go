package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	"github.com/OpenIMSDK/chat/pkg/common/adminprovision"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/common/registration"
	"github.com/OpenIMSDK/chat/pkg/proto/chat"
	chatClient "github.com/OpenIMSDK/chat/pkg/rpclient/chat"
	"github.com/OpenIMSDK/protocol/wrapperspb"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/google/uuid"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type directProvisionIM struct {
	users   map[string]adminprovision.User
	creates int
}

func (i *directProvisionIM) Find(_ context.Context, id string) (*adminprovision.User, error) {
	u, ok := i.users[id]
	if !ok {
		return nil, nil
	}
	return &u, nil
}
func (i *directProvisionIM) Create(_ context.Context, u adminprovision.User) error {
	i.creates++
	i.users[u.ID] = u
	return nil
}
func (i *directProvisionIM) PlanDefaults(context.Context, bool) (adminprovision.Defaults, error) {
	return adminprovision.Defaults{}, nil
}
func (i *directProvisionIM) ApplyDefaults(context.Context, string, adminprovision.Defaults) error {
	return nil
}

func TestAdminCreationRPCStableIntentRealActorAndNoCredentialReissue(t *testing.T) {
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	socket := "/Users/chuzu/Documents/projects/LuoboIM/references/integration/mysql8.4/mysql.sock"
	root, e := gorm.Open(mysql.Open("root@unix("+socket+")/luobo_integration?parseTime=true"), cfg)
	if e != nil {
		t.Skip("private MySQL unavailable")
	}
	name := "luobo_admin_rpc_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if e = root.Exec("CREATE DATABASE `" + name + "`").Error; e != nil {
		t.Fatal(e)
	}
	db, e := gorm.Open(mysql.Open("root@unix("+socket+")/"+name+"?parseTime=true"), cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		sql, _ := db.DB()
		sql.Close()
		root.Exec("DROP DATABASE `" + name + "`")
		sql, _ = root.DB()
		sql.Close()
	})
	if e = db.AutoMigrate(&table.Account{}, &table.Attribute{}, &table.Register{}, &table.VerifyCode{}, &accountstate.Cancellation{}, &registration.Receipt{}, &adminprovision.Intent{}, &adminprovision.Job{}, &adminprovision.RecoveryAudit{}); e != nil {
		t.Fatal(e)
	}
	original := config.Config.AdminList
	config.Config.AdminList = []config.Admin{{AdminID: "bootstrap-user"}}
	t.Cleanup(func() { config.Config.AdminList = original })
	im := &directProvisionIM{users: map[string]adminprovision.User{}}
	s := &chatSvr{Admin: &chatClient.AdminClient{}, Database: database.NewChatDatabase(db), Provisioning: &adminprovision.Service{DB: db, IM: im}}
	actor := mctx.WithOpUserID(context.Background(), "actual-creator", constant.AdminUser)
	id := uuid.NewString()
	request := func(password string) *chat.RegisterUserReq {
		return &chat.RegisterUserReq{Platform: 10, AutoLogin: true, AdminRequestID: id, User: &chat.RegisterUserInfo{UserID: "explicit-synthetic", Email: " Canonical@Fixture.Invalid ", Password: password, Nickname: "fixture", Birth: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()}}
	}
	first, e := s.RegisterUser(actor, request(""))
	if e != nil || first.UserID != "explicit-synthetic" || first.ProvisioningState != "direct_only" || first.ProvisioningPending || first.ChatToken != "" {
		t.Fatal(first, e)
	}
	second, e := s.RegisterUser(actor, request("changed-wire-is-not-reapplied"))
	if e != nil || !second.Recovered || second.ProvisioningReceiptID != first.ProvisioningReceiptID || second.ChatToken != "" || im.creates != 1 {
		t.Fatal("retry recreated/recredentialed account", e)
	}
	var account table.Account
	db.First(&account)
	var register table.Register
	db.First(&register)
	if account.OperatorUserID != "actual-creator" || register.Mode != "admin" || account.Password != "" {
		t.Fatal("bootstrap actor or retry password applied")
	}
	if _, e = s.AddUserAccount(mctx.WithOpUserID(context.Background(), "ordinary", constant.NormalUser), &chat.AddUserAccountReq{User: &chat.RegisterUserInfo{}}); e == nil {
		t.Fatal("ordinary RPC actor created an account")
	}
	// A second explicit chart identity cannot claim an existing IM-only owner.
	im.users["foreign-im-owner"] = adminprovision.User{ID: "foreign-im-owner", ProvisioningID: "foreign-marker"}
	foreign := request("")
	foreign.AdminRequestID = uuid.NewString()
	foreign.User.UserID = "foreign-im-owner"
	foreign.User.Email = "foreign@fixture.invalid"
	if _, e = s.RegisterUser(actor, foreign); e == nil {
		t.Fatal("existing IM-only identity claimed")
	}
	var rows int64
	db.Model(&table.Account{}).Count(&rows)
	if rows != 1 {
		t.Fatal("IM ID conflict committed chart account")
	}
	// Even a queued IM collision cannot escape through alternate lifecycle APIs.
	db.Model(&adminprovision.Job{}).Where("receipt_id=?", first.ProvisioningReceiptID).Update("state", "conflict")
	blocked := func(name string, e error) {
		if !errs.ErrNoPermission.Is(e) {
			t.Fatal(name + " did not stop at canonical ownership gate")
		}
	}
	_, e = s.Login(context.Background(), &chat.LoginReq{Email: "canonical@fixture.invalid", Password: "synthetic", Platform: 10})
	blocked("login", e)
	_, e = s.ChangePassword(actor, &chat.ChangePasswordReq{UserID: first.UserID, NewPassword: "synthetic-new-wire"})
	blocked("change password", e)
	_, e = s.UpdateUserInfo(actor, &chat.UpdateUserInfoReq{UserID: first.UserID, Nickname: &wrapperspb.StringValue{Value: "must-not-project"}})
	blocked("profile", e)
	originalVerify := config.Config.VerifyCode
	config.Config.VerifyCode.Use = "synthetic-provider"
	config.Config.VerifyCode.MaxCount = 3
	t.Cleanup(func() { config.Config.VerifyCode = originalVerify })
	code := &table.VerifyCode{Account: "canonical@fixture.invalid", Code: "123456", UsedFor: constant.VerificationCodeForResetPassword, CreateTime: time.Now(), Duration: 120}
	if e = db.Create(code).Error; e != nil {
		t.Fatal(e)
	}
	_, e = s.ResetPassword(context.Background(), &chat.ResetPasswordReq{Email: "canonical@fixture.invalid", VerifyCode: "123456", Password: "synthetic-new-wire"})
	blocked("reset password", e)
	db.First(&account)
	db.First(code)
	if account.Password != "" || code.Used {
		t.Fatal("blocked reset changed password or consumed OTP")
	}
	db.Model(&adminprovision.Job{}).Where("receipt_id=?", first.ProvisioningReceiptID).Update("state", "direct_only")
	changed := request("")
	changed.User.Nickname = "changed-profile"
	if _, e = s.RegisterUser(actor, changed); e == nil {
		t.Fatal("same intent changed profile")
	}
}
