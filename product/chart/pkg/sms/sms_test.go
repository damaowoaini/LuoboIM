package sms

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/chat/pkg/common/config"
)

func TestEmailConfigurationStartsWithoutPretendingToSendSMS(t *testing.T) {
	previous := config.Config.VerifyCode.Use
	defer func() { config.Config.VerifyCode.Use = previous }()
	config.Config.VerifyCode.Use = "mail"
	sender, err := New()
	if err != nil {
		t.Fatalf("email service cannot start: %v", err)
	}
	if err := sender.SendCode(context.Background(), "+86", "13800000000", "123456"); err == nil {
		t.Fatal("mail-only service falsely acknowledged an unsent SMS")
	}
	config.Config.VerifyCode.Use = ""
	sender, err = New()
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.SendCode(context.Background(), "+86", "13800000000", "123456"); err == nil {
		t.Fatal("disabled service falsely acknowledged an unsent SMS")
	}
}
