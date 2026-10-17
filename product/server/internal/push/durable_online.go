package push

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
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
	discovery    frozenGatewayDiscovery
	service      string
	k8s          bool
	authorize    gatewayContext
	policySHA    string
	policy       messagedelivery.KafkaDeliveryPolicy
	receiptStore *messagedelivery.Store
	callbacks    *FrozenKafkaPushCallbacks
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

// The ordinary worker uses this exact policy-aware constructor. Enabled push
// callbacks are checked against their durable decision, never current Config.
// Constructor is non-I/O; the managed worker explicitly checks Mongo readiness.
func NewKafkaFrozenGatewayTransport(discovery frozenGatewayDiscovery, service string, k8s bool, authorize gatewayContext, store *messagedelivery.Store, policy messagedelivery.KafkaDeliveryPolicy, callbacks ...*FrozenKafkaPushCallbacks) (messagedelivery.KafkaOnlineTransport, error) {
	digest, err := policy.Fingerprint()
	if err != nil || discovery == nil || service == "" || authorize == nil || store == nil || k8s != (policy.Discovery == "k8s") || len(callbacks) > 1 {
		return nil, errFrozenOnline
	}
	var callbackEngine *FrozenKafkaPushCallbacks
	if len(callbacks) == 1 {
		callbackEngine = callbacks[0]
		if callbackEngine == nil || callbackEngine.store != store || callbackEngine.policySHA != digest {
			return nil, errFrozenOnline
		}
	}
	if (policy.BeforeGroupPushBinding != "" || policy.OnlinePushBinding != "") && callbackEngine == nil {
		return nil, errPushCallbacksUnprepared
	}
	return &frozenGatewayTransport{discovery: discovery, service: service, k8s: k8s, authorize: authorize, policySHA: digest, policy: policy, receiptStore: store, callbacks: callbackEngine}, nil
}
func (t *frozenGatewayTransport) PolicyFingerprint() string {
	if t == nil {
		return ""
	}
	return t.policySHA
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
// No offline vendor, group/cache recipient expansion or deferred DismissGroup
// runs here. The ordinary adapter first consumes the exact durable callback
// decision; it cannot repeat an unknown HTTP attempt on a recovery pass.
func (t *frozenGatewayTransport) Deliver(ctx context.Context, call messagedelivery.OnlineCall) (messagedelivery.OnlineReport, error) {
	var report messagedelivery.OnlineReport
	if ctx == nil || call.Message == nil || (call.Message.Seq < 1 || !msgprocessor.Options(call.Message.Options).IsHistory()) && !((t.policy.Version == "group-effect-v1" || t.policy.Version == "ordinary-kafka-v1") && call.Message.Seq == 0 && !msgprocessor.Options(call.Message.Options).IsHistory()) || call.ConversationID != msgprocessor.GetConversationIDByMsg(call.Message) || len(call.Recipients) == 0 || len(call.Recipients) > 64 {
		return report, errFrozenOnline
	}
	// Existing callbacks execute from their frozen policy only. The separate
	// legacy constructor remains closed for enabled current-config callbacks.
	if t.policySHA != "" {
		if call.PolicySHA256 != t.policySHA || t.receiptStore == nil {
			return report, errFrozenOnline
		}
		if t.callbacks != nil {
			if err := t.callbacks.Execute(ctx, call); err != nil {
				return report, err
			}
		}
		if err := t.receiptStore.RequireKafkaPushCallbacks(ctx, call); err != nil {
			return report, err
		}
	} else if config.Config.Callback.CallbackOnlinePush.Enable || config.Config.Callback.CallbackBeforeSuperGroupOnlinePush.Enable {
		return report, errPushCallbacksUnprepared
	}
	if t.policy.Version == "group-effect-v1" {
		if t.receiptStore == nil {
			return report, errFrozenOnline
		}
		if err := t.receiptStore.AcquireOwnedActionPhysical(ctx, call); err != nil {
			return report, err
		}
	}
	if t.policy.Version == "ordinary-kafka-v1" && call.Message.Seq == 0 {
		if t.receiptStore == nil {
			return report, errFrozenOnline
		}
		if err := t.receiptStore.AcquireKafkaEphemeralPhysical(ctx, call); err != nil {
			return report, err
		}
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
	routingRemaining := map[string]int{}
	routingProofs := map[string][]byte{}
	onlineSeen := map[string]bool{}
	backgroundWebPC := map[string]bool{}
	for _, route := range routes {
		for _, uid := range route.users {
			remaining[uid]++
			routingRemaining[uid]++
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
		if t.policySHA != "" {
			if err := t.receiptStore.ValidateKafkaOnlineCall(requestCtx, call); err != nil {
				failed = true
				continue
			}
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
			if t.policySHA != "" {
				routingKnown := true
				background := false
				for _, platform := range row.Resp {
					if platform == nil || platform.RecvID != uid || platform.ResultCode != 0 && platform.ResultCode != -2 && platform.ResultCode != -3 {
						routingKnown = false
						break
					}
					if platform.ResultCode == -2 && (constant.PlatformIDToName(int(platform.RecvPlatFormID)) == constant.TerminalPC || platform.RecvPlatFormID == constant.WebPlatformID) {
						background = true
					}
				}
				if routingKnown {
					body, err := proto.MarshalOptions{Deterministic: true}.Marshal(row)
					if err == nil {
						routingRemaining[uid]--
						routingProofs[uid] = append(routingProofs[uid], []byte(digestGateway([]byte(route.connection.Target()))+"\x00"+digestGateway(body)+"\x00")...)
						onlineSeen[uid] = onlineSeen[uid] || row.OnlinePush
						backgroundWebPC[uid] = backgroundWebPC[uid] || background
					}
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
		if t.policySHA != "" && routingRemaining[uid] == 0 {
			report.Routes = append(report.Routes, messagedelivery.OnlineRouteResult{UserID: uid, ResponseSHA256: digestGateway(routingProofs[uid]), OnlineAccepted: onlineSeen[uid], BackgroundWebPC: backgroundWebPC[uid]})
		}
		if remaining[uid] == 0 {
			report.ACKs = append(report.ACKs, messagedelivery.OnlineACK{UserID: uid, ResponseSHA256: digestGateway(proofs[uid])})
		}
	}
	if failed {
		return report, errFrozenOnline
	}
	return report, nil
}
