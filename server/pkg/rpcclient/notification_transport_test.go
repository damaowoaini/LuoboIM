package rpcclient

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

func TestServerNotificationUsesServiceActorAndPreservesDomainSender(t *testing.T) {
	oldIM, oldManager := config.Config.IMAdmin.UserID, config.Config.Manager.UserID
	config.Config.IMAdmin.UserID = []string{"notification-service"}
	config.Config.Manager.UserID = nil
	defer func() { config.Config.IMAdmin.UserID = oldIM; config.Config.Manager.UserID = oldManager }()
	calls := 0
	sender := NewNotificationSender(WithLocalSendMsg(func(ctx context.Context, req *msg.SendMsgReq) (*msg.SendMsgResp, error) {
		calls++
		if authverify.CheckAdmin(ctx) != nil || authverify.CheckAccessV3(ctx, req.MsgData.SendID) != nil {
			t.Fatal("internal notification no longer has authorized transport")
		}
		if mcontext.GetOpUserID(ctx) != "notification-service" || req.MsgData.SendID != "bob" || req.MsgData.RecvID != "alice" || req.MsgData.ContentType != constant.FriendAddedNotification {
			t.Fatal("notification actor/message compatibility changed")
		}
		return &msg.SendMsgResp{}, nil
	}))
	original := context.WithValue(context.Background(), constant.OpUserID, "alice")
	if sender.NotificationWithSesstionType(original, "bob", "alice", constant.FriendAddedNotification, constant.NotificationChatType, &sdkws.FriendAddedTips{}) != nil || calls != 1 {
		t.Fatal("reverse friend notification failed")
	}
	if mcontext.GetOpUserID(original) != "alice" {
		t.Fatal("business actor context mutated")
	}
	config.Config.IMAdmin.UserID = nil
	if sender.NotificationWithSesstionType(original, "bob", "alice", constant.FriendAddedNotification, constant.NotificationChatType, &sdkws.FriendAddedTips{}) == nil || calls != 1 {
		t.Fatal("missing service identity did not fail closed")
	}
}
