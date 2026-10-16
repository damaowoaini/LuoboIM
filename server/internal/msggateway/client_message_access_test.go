package msggateway

import (
	"context"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestClientMessageCannotForgeSenderOrSystemSignaling(t *testing.T) {
	old := config.Config.IMAdmin.UserID
	config.Config.IMAdmin.UserID = []string{"rtc-service"}
	defer func() { config.Config.IMAdmin.UserID = old }()
	ctx := context.WithValue(context.Background(), constant.OpUserID, "alice")
	normal := &sdkws.MsgData{SendID: "alice", MsgFrom: constant.UserMsgType, SessionType: constant.SingleChatType, ContentType: constant.Text}
	if err := validateClientMessage(ctx, normal); err != nil {
		t.Fatal("legitimate text blocked", err)
	}
	forged := proto.Clone(normal).(*sdkws.MsgData)
	forged.SendID = "bob"
	if validateClientMessage(ctx, forged) == nil {
		t.Fatal("forged message sender accepted")
	}
	for _, kind := range []int32{constant.SignalingNotification, constant.RoomParticipantsConnectedNotification, constant.CustomSignalNotification, constant.GroupCreatedNotification} {
		forged = proto.Clone(normal).(*sdkws.MsgData)
		forged.ContentType = kind
		if validateClientMessage(ctx, forged) == nil {
			t.Fatal("client published reserved system notification")
		}
	}
	forged = proto.Clone(normal).(*sdkws.MsgData)
	forged.MsgFrom = constant.SysMsgType
	if validateClientMessage(ctx, forged) == nil {
		t.Fatal("client forged system origin")
	}
	forged = proto.Clone(normal).(*sdkws.MsgData)
	forged.SessionType = constant.NotificationChatType
	if validateClientMessage(ctx, forged) == nil {
		t.Fatal("client used system notification lane")
	}
	admin := context.WithValue(context.Background(), constant.OpUserID, "rtc-service")
	forged.MsgFrom = constant.SysMsgType
	forged.ContentType = constant.SignalingNotification
	if validateClientMessage(admin, forged) != nil {
		t.Fatal("authenticated service signaling blocked")
	}
}
