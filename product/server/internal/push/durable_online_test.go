package push

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/msggateway"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type frozenGatewayFixture struct {
	msggateway.UnimplementedMsgGatewayServer
	mu       sync.Mutex
	calls    []*msggateway.OnlineBatchPushOneMsgReq
	response func(*msggateway.OnlineBatchPushOneMsgReq) (*msggateway.OnlineBatchPushOneMsgResp, error)
}

func (g *frozenGatewayFixture) SuperGroupOnlineBatchPushOneMsg(_ context.Context, req *msggateway.OnlineBatchPushOneMsgReq) (*msggateway.OnlineBatchPushOneMsgResp, error) {
	g.mu.Lock()
	g.calls = append(g.calls, proto.Clone(req).(*msggateway.OnlineBatchPushOneMsgReq))
	g.mu.Unlock()
	return g.response(req)
}
func (g *frozenGatewayFixture) snapshot() []*msggateway.OnlineBatchPushOneMsgReq {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]*msggateway.OnlineBatchPushOneMsgReq(nil), g.calls...)
}

type frozenDiscoveryFixture struct{ connections []*grpc.ClientConn }

func (d frozenDiscoveryFixture) GetConns(context.Context, string, ...grpc.DialOption) ([]*grpc.ClientConn, error) {
	return d.connections, nil
}
func (d frozenDiscoveryFixture) GetConn(context.Context, string, ...grpc.DialOption) (*grpc.ClientConn, error) {
	if len(d.connections) == 0 {
		return nil, errors.New("synthetic discovery empty")
	}
	return d.connections[0], nil
}
func (d frozenDiscoveryFixture) GetUserIdHashGatewayHost(context.Context, string) (string, error) {
	return "synthetic-owned-gateway", nil
}
func gatewayFixture(t *testing.T, g *frozenGatewayFixture) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	msggateway.RegisterMsgGatewayServer(server, g)
	exited := make(chan error, 1)
	go func() { exited <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error("owned gateway listener cleanup failed", err)
		}
		select {
		case err := <-exited:
			if err != nil && !errors.Is(err, grpc.ErrServerStopped) && !errors.Is(err, net.ErrClosed) {
				t.Error("owned gateway server failed", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("owned gateway drain unconfirmed")
		}
	})
	connection, err := grpc.NewClient("passthrough:///owned-bufconn-gateway", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDisableRetry(), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error("owned gateway client close failed", err)
		}
	})
	return connection
}
func disableLegacyPushCallbacks(t *testing.T) {
	t.Helper()
	online, before := config.Config.Callback.CallbackOnlinePush.Enable, config.Config.Callback.CallbackBeforeSuperGroupOnlinePush.Enable
	config.Config.Callback.CallbackOnlinePush.Enable = false
	config.Config.Callback.CallbackBeforeSuperGroupOnlinePush.Enable = false
	t.Cleanup(func() {
		config.Config.Callback.CallbackOnlinePush.Enable = online
		config.Config.Callback.CallbackBeforeSuperGroupOnlinePush.Enable = before
	})
}
func onlineCallFixture() messagedelivery.OnlineCall {
	now := time.Now().Add(-31 * time.Second).UnixMilli()
	return messagedelivery.OnlineCall{BranchID: "e607ca68-9fb3-4aa2-8da4-5f0d26a0f505", ConversationID: "n_synthetic-group", Recipients: []string{"synthetic-owner", "synthetic-peer"}, Message: &sdkws.MsgData{SendID: "synthetic-actor", RecvID: "synthetic-group", GroupID: "synthetic-group", SessionType: constant.SuperGroupChatType, Seq: 42, ClientMsgID: "stable-notification", ServerMsgID: "stable-server", SendTime: now, CreateTime: now, Content: []byte("synthetic opaque content"), Options: msgprocessor.NewOptions(msgprocessor.WithHistory(true))}}
}
func successfulRows(req *msggateway.OnlineBatchPushOneMsgReq) (*msggateway.OnlineBatchPushOneMsgResp, error) {
	result := &msggateway.OnlineBatchPushOneMsgResp{}
	for _, uid := range req.PushToUserIDs {
		result.SinglePushResult = append(result.SinglePushResult, &msggateway.SingleMsgToUserResults{UserID: uid, Resp: []*msggateway.SingleMsgToUserPlatform{{RecvID: uid, ResultCode: 0, RecvPlatFormID: 5}}, OnlinePush: true})
	}
	return result, nil
}
func sameDeadlineContext(ctx context.Context, _ proto.Message) (context.Context, error) {
	return context.WithValue(ctx, struct{ synthetic string }{"metadata-only"}, true), nil
}

// This uses the actual generated gRPC client/server and protobuf serializer,
// with an owned in-process gateway stub. It does not create real WS users,
// authorize service JWTs, or prove a device/vendor received the message.
func TestDurableGatewayWirePreservesLateMessageAndExactRecipientRoster(t *testing.T) {
	disableLegacyPushCallbacks(t)
	gateway := &frozenGatewayFixture{response: successfulRows}
	connection := gatewayFixture(t, gateway)
	transport, err := NewFrozenGatewayTransport(frozenDiscoveryFixture{[]*grpc.ClientConn{connection}}, "gateway", false, sameDeadlineContext)
	if err != nil {
		t.Fatal(err)
	}
	call := onlineCallFixture()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	report, err := transport.Deliver(ctx, call)
	if err != nil || !report.DispatchStarted || len(report.ACKs) != 2 {
		t.Fatal("matching gateway responses were not confirmed", err)
	}
	calls := gateway.snapshot()
	if len(calls) != 1 || !proto.Equal(calls[0].MsgData, call.Message) || len(calls[0].PushToUserIDs) != 2 || calls[0].PushToUserIDs[0] != "synthetic-owner" || time.Now().UnixMilli()-calls[0].MsgData.SendTime < 10000 {
		t.Fatal("late message was dropped, refreshed, or retargeted")
	}
}

func TestDurableGatewayWireRefusesMalformedOrUnknownACKs(t *testing.T) {
	disableLegacyPushCallbacks(t)
	for name, change := range map[string]func(*msggateway.OnlineBatchPushOneMsgResp){
		"foreign-row":             func(r *msggateway.OnlineBatchPushOneMsgResp) { r.SinglePushResult[0].UserID = "foreign" },
		"missing-row":             func(r *msggateway.OnlineBatchPushOneMsgResp) { r.SinglePushResult = r.SinglePushResult[:1] },
		"duplicate-row":           func(r *msggateway.OnlineBatchPushOneMsgResp) { r.SinglePushResult[1] = r.SinglePushResult[0] },
		"wrong-platform-owner":    func(r *msggateway.OnlineBatchPushOneMsgResp) { r.SinglePushResult[0].Resp[0].RecvID = "foreign" },
		"socket-write-failed":     func(r *msggateway.OnlineBatchPushOneMsgResp) { r.SinglePushResult[0].Resp[0].ResultCode = -2 },
		"unknown-platform-result": func(r *msggateway.OnlineBatchPushOneMsgResp) { r.SinglePushResult[0].Resp[0].ResultCode = 55 },
	} {
		t.Run(name, func(t *testing.T) {
			gateway := &frozenGatewayFixture{response: func(req *msggateway.OnlineBatchPushOneMsgReq) (*msggateway.OnlineBatchPushOneMsgResp, error) {
				r, _ := successfulRows(req)
				change(r)
				return r, nil
			}}
			connection := gatewayFixture(t, gateway)
			transport, err := NewFrozenGatewayTransport(frozenDiscoveryFixture{[]*grpc.ClientConn{connection}}, "gateway", false, sameDeadlineContext)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			report, err := transport.Deliver(ctx, onlineCallFixture())
			if err == nil || !report.DispatchStarted {
				t.Fatal("malformed gateway reply became complete")
			}
			for _, ack := range report.ACKs {
				if ack.UserID == "synthetic-owner" {
					t.Fatal("failed/foreign recipient was acknowledged")
				}
			}
		})
	}
}

func TestDurableGatewayWirePreservesPartialACKAndDoesNotRetryLostRPC(t *testing.T) {
	disableLegacyPushCallbacks(t)
	good, bad := &frozenGatewayFixture{response: successfulRows}, &frozenGatewayFixture{response: func(*msggateway.OnlineBatchPushOneMsgReq) (*msggateway.OnlineBatchPushOneMsgResp, error) {
		return nil, status.Error(codes.Unavailable, "synthetic unknown reply")
	}}
	left, right := gatewayFixture(t, good), gatewayFixture(t, bad)
	transport, err := NewFrozenGatewayTransport(frozenDiscoveryFixture{[]*grpc.ClientConn{left, right}}, "gateway", false, sameDeadlineContext)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	report, err := transport.Deliver(ctx, onlineCallFixture())
	if err == nil || !report.DispatchStarted || len(report.ACKs) != 0 || len(good.snapshot()) != 1 || len(bad.snapshot()) != 1 {
		t.Fatal("unknown gateway ACK was retried or called complete", err)
	}
}

func TestDurableGatewayWireNoOnlineConnectionIsDistinctFromDeviceDelivery(t *testing.T) {
	disableLegacyPushCallbacks(t)
	gateway := &frozenGatewayFixture{response: func(req *msggateway.OnlineBatchPushOneMsgReq) (*msggateway.OnlineBatchPushOneMsgResp, error) {
		r, _ := successfulRows(req)
		for _, row := range r.SinglePushResult {
			row.Resp = nil
			row.OnlinePush = false
		}
		return r, nil
	}}
	connection := gatewayFixture(t, gateway)
	transport, err := NewFrozenGatewayTransport(frozenDiscoveryFixture{[]*grpc.ClientConn{connection}}, "gateway", false, sameDeadlineContext)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	report, err := transport.Deliver(ctx, onlineCallFixture())
	if err != nil || len(report.ACKs) != 2 {
		t.Fatal("known absence of online sockets did not finish the online route", err)
	}
	// The durable store still retains its separate offline_pending duty. These
	// ACKs say only that the gateway processed its online roster.
}

func TestDurableGatewayRejectsDetachedContextOrUnpreparedPushCallbacksBeforeRPC(t *testing.T) {
	disableLegacyPushCallbacks(t)
	gateway := &frozenGatewayFixture{response: successfulRows}
	connection := gatewayFixture(t, gateway)
	transport, err := NewFrozenGatewayTransport(frozenDiscoveryFixture{[]*grpc.ClientConn{connection}}, "gateway", false, func(context.Context, proto.Message) (context.Context, error) { return context.Background(), nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	report, err := transport.Deliver(ctx, onlineCallFixture())
	if err == nil || report.DispatchStarted || len(gateway.snapshot()) != 0 {
		t.Fatal("detached unbounded context reached gateway")
	}
	config.Config.Callback.CallbackOnlinePush.Enable = true
	transport, err = NewFrozenGatewayTransport(frozenDiscoveryFixture{[]*grpc.ClientConn{connection}}, "gateway", false, sameDeadlineContext)
	if err != nil {
		t.Fatal(err)
	}
	report, err = transport.Deliver(ctx, onlineCallFixture())
	if !errors.Is(err, errPushCallbacksUnprepared) || report.DispatchStarted || len(gateway.snapshot()) != 0 {
		t.Fatal("enabled legacy callback silently skipped")
	}
}
