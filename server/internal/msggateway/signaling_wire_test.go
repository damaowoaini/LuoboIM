package msggateway

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/protocol/rtc"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type signalingRPCFixture struct {
	rtc.RtcServiceClient
	response *rtc.SignalMessageAssembleResp
}

func (f signalingRPCFixture) SignalMessageAssemble(context.Context, *rtc.SignalMessageAssembleReq, ...grpc.CallOption) (*rtc.SignalMessageAssembleResp, error) {
	return f.response, nil
}

func TestWebSocketSignalingPreservesAssemblyEnvelopeAndRejectsMissingResponse(t *testing.T) {
	expected := &rtc.SignalResp{Payload: &rtc.SignalResp_Invite{Invite: &rtc.SignalInviteResp{Token: "synthetic-fixture-token", RoomID: "synthetic-fixture-room", LiveURL: "wss://media.example.test"}}}
	body, err := proto.Marshal(&rtc.SignalReq{})
	if err != nil {
		t.Fatal(err)
	}
	handler := GrpcHandler{signalClient: &rpcclient.SignalRpcClient{Client: signalingRPCFixture{response: &rtc.SignalMessageAssembleResp{SignalResp: expected}}}}
	data, err := handler.SendSignalMessage(context.Background(), &Req{Data: body})
	if err != nil {
		t.Fatal(err)
	}
	var decoded rtc.SignalMessageAssembleResp
	if err := proto.Unmarshal(data, &decoded); err != nil || !proto.Equal(expected, decoded.SignalResp) {
		t.Fatal("proven legacy assembly response changed", err)
	}
	for _, missing := range []*rtc.SignalMessageAssembleResp{nil, {}} {
		handler.signalClient.Client = signalingRPCFixture{response: missing}
		if _, err := handler.SendSignalMessage(context.Background(), &Req{Data: body}); err == nil {
			t.Fatal("missing RTC response became successful empty WebSocket payload")
		}
	}
}
