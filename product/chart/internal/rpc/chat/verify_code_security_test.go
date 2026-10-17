package chat

import (
	"context"
	"testing"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	pbchat "github.com/OpenIMSDK/chat/pkg/proto/chat"
)

type verifyDBStub struct {
	database.ChatDatabaseInterface
	code   *table.VerifyCode
	failed int
}

func (d *verifyDBStub) TakeLastVerifyCode(context.Context, string) (*table.VerifyCode, error) {
	return d.code, nil
}
func (d *verifyDBStub) UpdateVerifyCodeIncrCount(context.Context, uint) error { d.failed++; return nil }

func TestVerificationSecurityPurposeExpiryAndUse(t *testing.T) {
	old := config.Config.VerifyCode
	defer func() { config.Config.VerifyCode = old }()
	config.Config.VerifyCode.Use = "test-provider"
	config.Config.VerifyCode.MaxCount = 3
	for _, tc := range []struct {
		name    string
		purpose int32
		used    bool
		age     time.Duration
		count   int
		code    string
		allowed bool
	}{
		{"login", constant.VerificationCodeForLogin, false, 0, 0, "123456", true},
		{"password reset code cannot log in", constant.VerificationCodeForResetPassword, false, 0, 0, "123456", false},
		{"old code with unknown purpose", 0, false, 0, 0, "123456", false},
		{"used code", constant.VerificationCodeForLogin, true, 0, 0, "123456", false},
		{"expired code", constant.VerificationCodeForLogin, false, time.Hour, 0, "123456", false},
		{"guess limit", constant.VerificationCodeForLogin, false, 0, 3, "123456", false},
		{"wrong code", constant.VerificationCodeForLogin, false, 0, 0, "654321", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &verifyDBStub{code: &table.VerifyCode{ID: 1, Code: "123456", UsedFor: tc.purpose, Used: tc.used, Duration: 120, CreateTime: time.Now().Add(-tc.age), Count: tc.count}}
			_, err := (&chatSvr{Database: db}).verifyCodeStrict(context.Background(), "account", tc.code, constant.VerificationCodeForLogin)
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v err=%v", tc.allowed, err)
			}
			if tc.name == "wrong code" && db.failed != 1 {
				t.Fatal("wrong guess was not counted")
			}
		})
	}
}

func TestVerificationSecurityCheckDoesNotConsume(t *testing.T) {
	old := config.Config.VerifyCode
	defer func() { config.Config.VerifyCode = old }()
	config.Config.VerifyCode.Use = "test-provider"
	config.Config.VerifyCode.MaxCount = 3
	db := &verifyDBStub{code: &table.VerifyCode{ID: 1, Code: "123456", UsedFor: constant.VerificationCodeForRegister, Duration: 120, CreateTime: time.Now()}}
	s := &chatSvr{Database: db}
	for i := 0; i < 2; i++ {
		if _, err := s.VerifyCode(context.Background(), &pbchat.VerifyCodeReq{Email: "a@example.com", VerifyCode: "123456", UsedFor: constant.VerificationCodeForRegister}); err != nil {
			t.Fatal(err)
		}
	}
	if db.code.Used {
		t.Fatal("checking code consumed it before final registration")
	}
	if _, err := s.VerifyCode(context.Background(), &pbchat.VerifyCodeReq{Email: "a@example.com", VerifyCode: "123456", UsedFor: constant.VerificationCodeForResetPassword}); err == nil {
		t.Fatal("check accepted wrong purpose")
	}
}
