package chat

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	"github.com/OpenIMSDK/chat/pkg/common/adminprovision"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/registration"
	"github.com/OpenIMSDK/chat/pkg/proto/admin"
	"github.com/OpenIMSDK/chat/pkg/proto/chat"
	chatClient "github.com/OpenIMSDK/chat/pkg/rpclient/chat"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type publicRPCAdmin struct {
	admin.UnimplementedAdminServer
	tokens atomic.Int32
}

func (*publicRPCAdmin) CheckRegisterForbidden(context.Context, *admin.CheckRegisterForbiddenReq) (*admin.CheckRegisterForbiddenResp, error) {
	return &admin.CheckRegisterForbiddenResp{}, nil
}
func (*publicRPCAdmin) CheckLoginForbidden(context.Context, *admin.CheckLoginForbiddenReq) (*admin.CheckLoginForbiddenResp, error) {
	return &admin.CheckLoginForbiddenResp{}, nil
}
func (*publicRPCAdmin) GetClientConfig(context.Context, *admin.GetClientConfigReq) (*admin.GetClientConfigResp, error) {
	return &admin.GetClientConfigResp{Config: map[string]string{constant.NeedVerificationCodeRegisterConfigKey: "1", constant.NeedInvitationCodeRegisterConfigKey: "0"}}, nil
}
func (s *publicRPCAdmin) CreateToken(context.Context, *admin.CreateTokenReq) (*admin.CreateTokenResp, error) {
	s.tokens.Add(1)
	return &admin.CreateTokenResp{Token: "synthetic-chat-after-recovery"}, nil
}

type publicRPCDiscovery struct {
	discoveryregistry.SvcDiscoveryRegistry
	conn *grpc.ClientConn
}

func (s publicRPCDiscovery) GetConn(context.Context, string, ...grpc.DialOption) (*grpc.ClientConn, error) {
	return s.conn, nil
}

type publicRPCLostConfirmation struct {
	directProvisionIM
	unconfirmed bool
}

func (s *publicRPCLostConfirmation) Create(ctx context.Context, u adminprovision.User) error {
	e := s.directProvisionIM.Create(ctx, u)
	s.unconfirmed = true
	return e
}
func (s *publicRPCLostConfirmation) Find(ctx context.Context, id string) (*adminprovision.User, error) {
	if s.unconfirmed {
		s.unconfirmed = false
		return nil, errors.New("synthetic lost create confirmation")
	}
	return s.directProvisionIM.Find(ctx, id)
}

func TestPublicRPCOTPAndLostResponseRecoverOriginalPasswordWithoutClientReceipt(t *testing.T) {
	for _, signed := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "signed route"}[signed], func(t *testing.T) {
			cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
			socket := "/Users/chuzu/Documents/projects/LuoboIM/references/integration/mysql8.4/mysql.sock"
			root, e := gorm.Open(mysql.Open("root@unix("+socket+")/luobo_integration?parseTime=true"), cfg)
			if e != nil {
				t.Skip("private MySQL unavailable")
			}
			name := "luobo_public_rpc_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			if e = root.Exec("CREATE DATABASE `" + name + "`").Error; e != nil {
				t.Fatal(e)
			}
			db, e := gorm.Open(mysql.Open("root@unix("+socket+")/"+name+"?parseTime=true"), cfg)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() {
				x, _ := db.DB()
				x.Close()
				root.Exec("DROP DATABASE `" + name + "`")
				x, _ = root.DB()
				x.Close()
			})
			if e = db.AutoMigrate(&table.Account{}, &table.Attribute{}, &table.Register{}, &table.VerifyCode{}, &table.UserLoginRecord{}, &accountstate.Cancellation{}, &registration.Receipt{}, &adminprovision.Intent{}, &adminprovision.Job{}, &adminprovision.RecoveryAudit{}); e != nil {
				t.Fatal(e)
			}
			oldAdmins, oldVerify := config.Config.AdminList, config.Config.VerifyCode.Use
			config.Config.AdminList = []config.Admin{{AdminID: "synthetic-bootstrap"}}
			config.Config.VerifyCode.Use = "mail"
			t.Cleanup(func() { config.Config.AdminList = oldAdmins; config.Config.VerifyCode.Use = oldVerify })
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			server := grpc.NewServer()
			manager := &publicRPCAdmin{}
			admin.RegisterAdminServer(server, manager)
			go server.Serve(listener)
			t.Cleanup(server.Stop)
			conn, e := grpc.DialContext(context.Background(), listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { conn.Close() })
			im := &publicRPCLostConfirmation{directProvisionIM: directProvisionIM{users: map[string]adminprovision.User{}}}
			service := &adminprovision.Service{DB: db, IM: im, ServerID: "synthetic-public-tenant"}
			s := &chatSvr{Admin: chatClient.NewAdminClient(publicRPCDiscovery{conn: conn}), Database: database.NewChatDatabase(db), Provisioning: service}
			email := "public-rpc@fixture.invalid"
			code := &table.VerifyCode{Account: email, Code: "123456", UsedFor: constant.VerificationCodeForRegister, Duration: 300, CreateTime: time.Now()}
			if e = db.Create(code).Error; e != nil {
				t.Fatal(e)
			}
			const wire = "0123456789abcdef0123456789abcdef"
			request := func() *chat.RegisterUserReq {
				return &chat.RegisterUserReq{Platform: 2, AutoLogin: true, VerifyCode: code.Code, Ip: "127.0.0.1", User: &chat.RegisterUserInfo{Email: email, Password: wire, Nickname: "Synthetic public RPC"}}
			}
			ctx := context.Background()
			routeID := uuid.NewString()
			if signed {
				ctx = registration.WithID(ctx, routeID)
			}
			response, e := s.RegisterUser(ctx, request())
			if e != nil || response == nil || !response.ProvisioningPending || response.ChatToken != "" || manager.tokens.Load() != 0 {
				t.Fatal("public account bypassed pending receipt or issued early token", e)
			}
			var receipt registration.Receipt
			if e = db.First(&receipt).Error; e != nil {
				t.Fatal(e)
			}
			if !receipt.ManagedByOutbox || receipt.IMProvisionedAt != nil || receipt.UserID != response.UserID || (signed && receipt.RegistrationID != routeID) {
				t.Fatal("RPC omitted/misbound atomic public receipt")
			}
			login := &chat.LoginReq{Email: email, Password: wire, Platform: 2, Ip: "127.0.0.1"}
			if _, e = s.Login(context.Background(), login); e == nil || manager.tokens.Load() != 0 {
				t.Fatal("pending ownership accepted original password login")
			}
			// A new worker knows only persisted jobs;it receives no client request,
			// password,OTP or response receipt. The already-created IM owner is reused.
			resumed := *service
			var jobs []adminprovision.Job
			if e = db.Where("state <> ?", "direct_only").Find(&jobs).Error; e != nil {
				t.Fatal(e)
			}
			for _, job := range jobs {
				if e = resumed.Process(context.Background(), job.ReceiptID); e != nil {
					t.Fatal(e)
				}
			}
			logged, e := s.Login(context.Background(), login)
			if e != nil || logged.UserID != response.UserID || logged.ChatToken != "synthetic-chat-after-recovery" || manager.tokens.Load() != 1 || im.creates != 1 {
				t.Fatal("original password did not recover without re-registration", e)
			}
			if e = db.First(code, code.ID).Error; e != nil || !code.Used {
				t.Fatal("OTP consumption lost after recovery", e)
			}
			if _, e = s.RegisterUser(ctx, request()); e == nil {
				t.Fatal("consumed registration OTP was reusable")
			}
			var count int64
			db.Model(&table.Account{}).Count(&count)
			if count != 1 {
				t.Fatal("retry created another account")
			}
		})
	}
}
