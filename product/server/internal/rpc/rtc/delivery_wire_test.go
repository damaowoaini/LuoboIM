package rtc

import (
	"context"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/msg"
	pb "github.com/OpenIMSDK/protocol/rtc"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type captureDeliveryClient struct {
	msg.MsgClient
	request *msg.SendMsgReq
}

func (c *captureDeliveryClient) SendMsg(_ context.Context, request *msg.SendMsgReq, _ ...grpc.CallOption) (*msg.SendMsgResp, error) {
	c.request = proto.Clone(request).(*msg.SendMsgReq)
	return &msg.SendMsgResp{}, nil
}

func TestRTCDeliveryUsesNotificationMapWithoutDuplicatingChatHistory(t *testing.T) {
	client := &captureDeliveryClient{}
	delivery := &rpcDelivery{messages: &rpcclient.MessageRpcClient{Client: client}, serviceUser: "fixture-service"}
	for _, kind := range []int32{constant.SignalingNotification, constant.RoomParticipantsConnectedNotification, constant.RoomParticipantsDisconnectedNotification, constant.StreamChangedNotification, constant.CustomSignalNotification} {
		payload, err := proto.Marshal(&pb.SignalReq{Payload: &pb.SignalReq_Invite{Invite: &pb.SignalInviteReq{}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := delivery.Send(context.Background(), &Outbox{ID: "fixed-synthetic-event", Receiver: "peer", ContentType: kind, Payload: payload, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		data := client.request.MsgData
		options := msgprocessor.Options(data.Options)
		// Client.PushMessage uses precisely these production functions to select
		// PushMessages.NotificationMsgs. A sn_ chat event cannot trigger the
		// proven legacy native notification handler for 1601-1605.
		if !msgprocessor.IsNotification(msgprocessor.GetConversationIDByMsg(data)) || options.IsNotNotification() {
			t.Fatal("RTC routed into ordinary Msgs instead of NotificationMsgs")
		}
		if options.IsSendMsg() || options.IsHistory() || options.IsUnreadCount() || options.IsConversationUpdate() {
			t.Fatal("RTC notification duplicated normal chat/history/unread state")
		}
		if !proto.Equal(&pb.SignalReq{Payload: &pb.SignalReq_Invite{Invite: &pb.SignalInviteReq{}}}, decodeSignal(t, data.Content)) {
			t.Fatal("RTC raw protobuf changed")
		}
	}
}
func decodeSignal(t *testing.T, data []byte) *pb.SignalReq {
	t.Helper()
	var value pb.SignalReq
	if err := proto.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return &value
}
