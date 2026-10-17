package rtc

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	pb "github.com/OpenIMSDK/protocol/rtc"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/protocol/wrapperspb"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/rpcmiddleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// This traverses protobuf serialization and the actual production metadata and
// business-error middleware, with real Mongo persistence. MQ/gateway delivery is
// deliberately covered separately, not represented as a complete deployment.
func TestMongoRTCAll17RPCOverProductionMiddleware(t *testing.T) {
	service, _, _, _ := rtcMongo(t)
	old := config.Config.IMAdmin.UserID
	config.Config.IMAdmin.UserID = []string{"admin"}
	defer func() { config.Config.IMAdmin.UserID = old }()
	var mutex sync.Mutex
	methods := map[string]bool{}
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer(rpcmiddleware.GrpcServer(), grpc.ChainUnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		mutex.Lock()
		methods[info.FullMethod] = true
		mutex.Unlock()
		return next(ctx, req)
	}))
	pb.RegisterRtcServiceServer(server, service)
	go server.Serve(listener)
	defer server.Stop()
	connection, err := grpc.NewClient("passthrough:///rtc-fixture", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()), rpcmiddleware.GrpcClient())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client := pb.NewRtcServiceClient(connection)
	actor := func(user string, platform int) context.Context {
		return context.WithValue(rtcContext(user, platform), constant.OperationID, "rtc-transport-"+uuid.NewString())
	}
	host, guest, admin := actor("u1", 2), actor("u2", 1), actor("admin", 5)
	roomID := uuid.NewString()
	invited, err := client.SignalMessageAssemble(host, inviteRequest(roomID, "u1", "u2"))
	if err != nil || invited.GetSignalResp().GetInvite().RoomID != roomID {
		t.Fatal("wire invite failed", err)
	}
	if _, err := client.GetSignalInvitationInfoStartApp(guest, &pb.GetSignalInvitationInfoStartAppReq{UserID: "u2"}); err != nil {
		t.Fatal(err)
	}
	info, err := client.GetSignalInvitationInfo(guest, &pb.GetSignalInvitationInfoReq{RoomID: roomID})
	if err != nil || info.InvitationInfo == nil {
		t.Fatal("wire invitation lookup failed", err)
	}
	if _, err := client.SignalMessageAssemble(guest, acceptRequest(info.InvitationInfo, "u2")); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SignalGetTokenByRoomID(guest, &pb.SignalGetTokenByRoomIDReq{RoomID: roomID, UserID: "u2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SignalGetRooms(guest, &pb.SignalGetRoomsReq{RoomIDs: []string{roomID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SignalGetRoomByGroupID(guest, &pb.SignalGetRoomByGroupIDReq{GroupID: "group-without-call"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SignalMessageAssemble(host, &pb.SignalMessageAssembleReq{SignalReq: &pb.SignalReq{Payload: &pb.SignalReq_HungUp{HungUp: &pb.SignalHungUpReq{UserID: "u1", Invitation: info.InvitationInfo}}}}); err != nil {
		t.Fatal(err)
	}
	meeting, err := client.SignalCreateMeeting(host, &pb.SignalCreateMeetingReq{MeetingName: "Wire Meeting", InviteeUserIDList: []string{"u2"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SignalJoinMeeting(guest, &pb.SignalJoinMeetingReq{RoomID: meeting.RoomID, UserID: "u2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SignalUpdateMeetingInfo(host, &pb.SignalUpdateMeetingInfoReq{RoomID: meeting.RoomID, MeetingName: wrapperspb.String("Updated Wire Meeting")}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SignalGetMeetings(guest, &pb.SignalGetMeetingsReq{UserID: "u2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SignalOperateStream(guest, &pb.SignalOperateStreamReq{RoomID: meeting.RoomID, UserID: "u2", StreamType: "audio", Mute: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SignalSendCustomSignal(guest, &pb.SignalSendCustomSignalReq{RoomID: meeting.RoomID, CustomInfo: `{"type":"client-test"}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SignalCloseRoom(host, &pb.SignalCloseRoomReq{RoomID: meeting.RoomID}); err != nil {
		t.Fatal(err)
	}
	pagination := &sdkws.RequestPagination{PageNumber: 1, ShowNumber: 20}
	if _, err := client.GetSignalInvitationRecords(admin, &pb.GetSignalInvitationRecordsReq{Pagination: pagination}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DeleteSignalRecords(admin, &pb.DeleteSignalRecordsReq{SIDs: []string{roomID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetMeetingRecords(admin, &pb.GetMeetingRecordsReq{Pagination: pagination}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DeleteMeetingRecords(admin, &pb.DeleteMeetingRecordsReq{RoomIDs: []string{meeting.RoomID}}); err != nil {
		t.Fatal(err)
	}
	mutex.Lock()
	count := len(methods)
	mutex.Unlock()
	if count != 17 {
		t.Fatalf("only %d/17 existing RPC contracts traversed", count)
	}
	if _, err := client.SignalCreateMeeting(actor("attacker", 1), &pb.SignalCreateMeetingReq{MeetingName: "Forged", MeetingHostUserID: "victim"}); err == nil {
		t.Fatal("protobuf middleware lost actor ownership")
	}
	t.Log("LUOBO_RTC_17_RPC_WIRE_PASS")
}
