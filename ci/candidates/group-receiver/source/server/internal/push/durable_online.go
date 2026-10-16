package push

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	"github.com/OpenIMSDK/protocol/msggateway"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

var errFrozenOnline = errors.New("frozen online gateway result unconfirmed")
var errPushCallbacksUnprepared = errors.New("durable push callback obligations unprepared")

type frozenGatewayDiscovery interface {
	GetConns(context.Context, string, ...grpc.DialOption) ([]*grpc.ClientConn, error)
	GetConn(context.Context, string, ...grpc.DialOption) (*grpc.ClientConn, error)
	GetUserIdHashGatewayHost(context.Context, string) (string, error)
}
type gatewayContext func(context.Context, proto.Message) (context.Context, error)
type frozenGatewayTransport struct {
	discovery frozenGatewayDiscovery
	service   string
	k8s       bool
	authorize gatewayContext
}

// The caller supplies the owned internal authenticated client-context builder.
// There is no incoming opuser/token-to-service-identity fallback here. This
// adapter does not expose an HTTP/RPC entry point or weaken gateway middleware.
func NewFrozenGatewayTransport(discovery frozenGatewayDiscovery, service string, k8s bool, authorize gatewayContext) (messagedelivery.OnlineTransport, error) {
	if discovery == nil || service == "" || authorize == nil {
		return nil, errFrozenOnline
	}
	return &frozenGatewayTransport{discovery: discovery, service: service, k8s: k8s, authorize: authorize}, nil
}

type frozenGatewayRoute struct {
	connection *grpc.ClientConn
	users      []string
}

func digestGateway(body []byte) string { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }

// Deliver bypasses only the OLD >10second queue drop. It preserves the frozen
// SendTime, body, stable Seq/IDs and exact recipients. It calls the real gateway
// client and requires a matching response row for EVERY target from EVERY
// applicable gateway. Unlike GetConnsAndOnlinePush, RPC errors are not nil ACKs.
// No offline vendor, group/cache recipient expansion, deferred DismissGroup or
// repeated push callback runs on this recoverable path.
func (t *frozenGatewayTransport) Deliver(ctx context.Context, call messagedelivery.OnlineCall) (messagedelivery.OnlineReport, error) {
	var report messagedelivery.OnlineReport
	if ctx == nil || call.Message == nil || call.Message.Seq < 1 || !msgprocessor.Options(call.Message.Options).IsHistory() || call.ConversationID != msgprocessor.GetConversationIDByMsg(call.Message) || len(call.Recipients) == 0 || len(call.Recipients) > 64 {
		return report, errFrozenOnline
	}
	// This WIP cannot silently skip existing configured external callbacks.
	// Enabling durable mode with them requires their once-only frozen callback
	// obligations to be wired first. The ordinary legacy path remains unchanged.
	if config.Config.Callback.CallbackOnlinePush.Enable || config.Config.Callback.CallbackBeforeSuperGroupOnlinePush.Enable {
		return report, errPushCallbacksUnprepared
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	seen := map[string]bool{}
	for _, uid := range call.Recipients {
		if uid == "" || seen[uid] {
			return report, errFrozenOnline
		}
		seen[uid] = true
	}
	var routes []frozenGatewayRoute
	if t.k8s {
		byHost := map[string][]string{}
		for _, uid := range call.Recipients {
			host, err := t.discovery.GetUserIdHashGatewayHost(current, uid)
			if err != nil || host == "" {
				return report, errFrozenOnline
			}
			byHost[host] = append(byHost[host], uid)
		}
		hosts := make([]string, 0, len(byHost))
		for host := range byHost {
			hosts = append(hosts, host)
		}
		sort.Strings(hosts)
		for _, host := range hosts {
			conn, err := t.discovery.GetConn(current, host)
			if err != nil || conn == nil {
				return report, errFrozenOnline
			}
			routes = append(routes, frozenGatewayRoute{connection: conn, users: byHost[host]})
		}
	} else {
		connections, err := t.discovery.GetConns(current, t.service)
		if err != nil || len(connections) == 0 {
			return report, errFrozenOnline
		}
		for _, conn := range connections {
			if conn == nil {
				return report, errFrozenOnline
			}
			routes = append(routes, frozenGatewayRoute{connection: conn, users: append([]string(nil), call.Recipients...)})
		}
		sort.Slice(routes, func(i, j int) bool { return routes[i].connection.Target() < routes[j].connection.Target() })
	}
	remaining := map[string]int{}
	proofs := map[string][]byte{}
	for _, route := range routes {
		for _, uid := range route.users {
			remaining[uid]++
		}
	}
	failed := false
	for _, route := range routes {
		request := &msggateway.OnlineBatchPushOneMsgReq{MsgData: proto.Clone(call.Message).(*sdkws.MsgData), PushToUserIDs: append([]string(nil), route.users...)}
		requestCtx, err := t.authorize(current, request)
		if err != nil || requestCtx == nil || requestCtx.Done() != current.Done() {
			failed = true
			continue
		}
		deadline, ok := requestCtx.Deadline()
		expected, _ := current.Deadline()
		if !ok || !deadline.Equal(expected) {
			failed = true
			continue
		}
		report.DispatchStarted = true
		response, err := msggateway.NewMsgGatewayClient(route.connection).SuperGroupOnlineBatchPushOneMsg(requestCtx, request)
		if err != nil || response == nil {
			failed = true
			continue
		}
		wanted := map[string]bool{}
		for _, uid := range route.users {
			wanted[uid] = true
		}
		rows := map[string]*msggateway.SingleMsgToUserResults{}
		valid := true
		for _, row := range response.SinglePushResult {
			if row == nil || !wanted[row.UserID] || rows[row.UserID] != nil {
				valid = false
				break
			}
			rows[row.UserID] = row
		}
		if !valid || len(rows) != len(wanted) {
			failed = true
			continue
		}
		for _, uid := range route.users {
			row := rows[uid]
			ack := true
			for _, platform := range row.Resp {
				if platform == nil || platform.RecvID != uid || platform.ResultCode == -2 {
					ack = false
					break
				}
				if platform.ResultCode != 0 && platform.ResultCode != -3 {
					ack = false
					break
				}
			}
			if !ack {
				failed = true
				continue
			}
			body, err := proto.MarshalOptions{Deterministic: true}.Marshal(row)
			if err != nil {
				failed = true
				continue
			}
			remaining[uid]--
			proofs[uid] = append(proofs[uid], []byte(digestGateway([]byte(route.connection.Target()))+"\x00"+digestGateway(body)+"\x00")...)
		}
	}
	for _, uid := range call.Recipients {
		if remaining[uid] == 0 {
			report.ACKs = append(report.ACKs, messagedelivery.OnlineACK{UserID: uid, ResponseSHA256: digestGateway(proofs[uid])})
		}
	}
	if failed {
		return report, errFrozenOnline
	}
	return report, nil
}
