package push

import (
	"context"
	"errors"
	"github.com/IBM/sarama"
	"github.com/OpenIMSDK/protocol/constant"
	pbconversation "github.com/OpenIMSDK/protocol/conversation"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	pbchat "github.com/OpenIMSDK/protocol/msg"
	pbpush "github.com/OpenIMSDK/protocol/push"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/OpenIMSDK/tools/utils"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/localcache"
	http2 "github.com/openimsdk/open-im-server/v3/pkg/common/http"
	"github.com/openimsdk/open-im-server/v3/pkg/common/kafka"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"github.com/openimsdk/open-im-server/v3/pkg/common/startrpc"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"time"
)

var errOwnedPush = errors.New("original owned legacy push is unconfirmed")

func startOwnedPush(registry discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) error {
	// Modern delivery is already executed by the actual managed positive/Seq0
	// online/offline workers. Never double-consume a raw topic or enable a raw RPC
	// as a fallback without its original typed source/cut/receipt.
	mode := startrpc.OwnedRPCMode(server)
	if mode != "legacy" && mode != "required" {
		return errOwnedPush
	}
	parent := startrpc.OwnedRPCContext(server)
	if parent == nil || ownedsource.FromContext(parent) == nil {
		return errOwnedPush
	}
	ctx, cancel := context.WithCancel(parent)
	failOwner := func() { cancel(); startrpc.CancelOwnedRPC(server) }
	rdb, err := cache.NewOwnedSourceRedis(ctx)
	if err != nil {
		cancel()
		return err
	}
	if err = startrpc.OwnRPCResource(server, "redis", func(context.Context) error { defer cancel(); return rdb.Close() }); err != nil {
		return errors.Join(err, rdb.Close())
	}
	if mode == "required" {
		// Existing logout/token administration is independent of raw delivery. Keep
		// its original authenticated RPC and actual Redis source, without opening
		// any provider/consumer/opaque PushMsg fallback.
		original := &pushServer{pusher: &Pusher{database: controller.NewPushDatabase(cache.NewMsgCacheModel(rdb))}}
		pbpush.RegisterPushMsgServiceServer(server, &ownedRequiredPushServer{original: original, binding: &ownedPushBinding{ctx: ctx, fail: failOwner}})
		return nil
	}
	callbacks, err := http2.NewOwnedPushHTTP(ctx, failOwner)
	if err != nil {
		return err
	}
	if err = startrpc.OwnRPCResource(server, "callback-http", func(context.Context) error { return callbacks.Close() }); err != nil {
		return errors.Join(err, callbacks.Close())
	}
	cacheModel := cache.NewMsgCacheModel(rdb)
	provider, closeProvider, err := newOwnedOfflinePusher(ctx, cacheModel, callbacks, failOwner)
	if err != nil {
		return err
	}
	if err = startrpc.OwnRPCResource(server, "push-provider", closeProvider); err != nil {
		return errors.Join(err, closeProvider(context.Background()))
	}
	// The exact original registry still owns these connections. Constructor
	// cancellation/errors must return through the resource stop path, never the
	// default Background/panic wrappers while the owner is only half started.
	groupConn, err := registry.GetConn(ctx, config.Config.RpcRegisterName.OpenImGroupName)
	if err != nil || groupConn == nil || ctx.Err() != nil {
		return errors.Join(err, ctx.Err(), errOwnedPush)
	}
	conversationConn, err := registry.GetConn(ctx, config.Config.RpcRegisterName.OpenImConversationName)
	if err != nil || conversationConn == nil || ctx.Err() != nil {
		return errors.Join(err, ctx.Err(), errOwnedPush)
	}
	msgConn, err := registry.GetConn(ctx, config.Config.RpcRegisterName.OpenImMsgName)
	if err != nil || msgConn == nil || ctx.Err() != nil {
		return errors.Join(err, ctx.Err(), errOwnedPush)
	}
	group := rpcclient.GroupRpcClient{Client: pbgroup.NewGroupClient(groupConn)}
	conversation := rpcclient.ConversationRpcClient{Client: pbconversation.NewConversationClient(conversationConn)}
	msg := rpcclient.MessageRpcClient{Client: pbchat.NewMsgClient(msgConn)}
	pusher := NewPusher(registry, provider, controller.NewPushDatabase(cacheModel), localcache.NewGroupLocalCache(&group), localcache.NewConversationLocalCache(&conversation), &conversation, &group, &msg)
	pusher.owned = &ownedPushBinding{ctx: ctx, callbacks: callbacks, fail: failOwner}
	consumer, err := kafka.NewOwnedConsumerGroup(ctx, config.Config.Kafka.Addr, config.Config.Kafka.ConsumerGroupID.MsgToPush)
	if err != nil {
		return err
	}
	handler := &ownedPushClaim{pusher: pusher, parent: ctx, cancel: failOwner}
	run, err := kafka.NewOwnedConsumerRun(ctx, consumer, []string{config.Config.Kafka.MsgToPush.Topic}, handler, failOwner)
	if err != nil {
		return errors.Join(err, consumer.Close())
	}
	if err = startrpc.OwnRPCResource(server, "push-consumer", run.Stop); err != nil {
		return errors.Join(err, run.Stop(context.Background()))
	}
	handler.active = run
	if err = startrpc.OwnRPCPushAdmission(server, run.Enable, func(stopCtx context.Context) error { err := run.StopAdmission(stopCtx); cancel(); return err }, run.Failure); err != nil {
		return err
	}
	if err = run.StartBounded(ctx); err != nil {
		return err
	}
	pbpush.RegisterPushMsgServiceServer(server, &ownedPushServer{original: &pushServer{pusher: pusher}, binding: pusher.owned, admitted: run})
	return nil
}

type ownedPushBinding struct {
	ctx       context.Context
	callbacks *http2.OwnedCallbackHTTP
	fail      context.CancelFunc
}

func (b *ownedPushBinding) bind(caller context.Context) (context.Context, func(), error) {
	if caller == nil || caller.Err() != nil || b.ctx.Err() != nil {
		return nil, nil, errOwnedPush
	}
	current, cancel := context.WithCancel(caller)
	stop := context.AfterFunc(b.ctx, cancel)
	// Original observer always wins over any RPC caller context.
	owner := ownedsource.FromContext(b.ctx)
	bound, err := ownedsource.Bind(current, owner)
	if err != nil {
		stop()
		cancel()
		return nil, nil, err
	}
	if b.callbacks != nil {
		bound = b.callbacks.Bind(bound)
	}
	return bound, func() { stop(); cancel() }, nil
}

type ownedPushServer struct {
	original *pushServer
	binding  *ownedPushBinding
	admitted *kafka.OwnedConsumerRun
}

func (s *ownedPushServer) PushMsg(ctx context.Context, req *pbpush.PushMsgReq) (*pbpush.PushMsgResp, error) {
	if !s.admitted.Admitted() || req == nil || req.MsgData == nil {
		return nil, errOwnedPush
	}
	current, done, err := s.binding.bind(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	response, err := s.original.PushMsg(current, req)
	if ownedsource.FromContext(s.binding.ctx).Snapshot().Unconfirmed() {
		s.binding.fail()
		return nil, errOwnedPush
	}
	return response, err
}
func (s *ownedPushServer) DelUserPushToken(ctx context.Context, req *pbpush.DelUserPushTokenReq) (*pbpush.DelUserPushTokenResp, error) {
	if !s.admitted.Admitted() || req == nil {
		return nil, errOwnedPush
	}
	current, done, err := s.binding.bind(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	response, err := s.original.DelUserPushToken(current, req)
	if ownedsource.FromContext(s.binding.ctx).Snapshot().Unconfirmed() {
		s.binding.fail()
		return nil, errOwnedPush
	}
	return response, err
}

type ownedPushClaim struct {
	pusher *Pusher
	parent context.Context
	cancel context.CancelFunc
	active *kafka.OwnedConsumerRun
}

func (*ownedPushClaim) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (*ownedPushClaim) Cleanup(sarama.ConsumerGroupSession) error { return nil }
func (h *ownedPushClaim) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case <-h.parent.Done():
			return nil
		case <-session.Context().Done():
			return nil
		case record, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			if !h.active.Admitted() || h.parent.Err() != nil || session.Context().Err() != nil {
				return nil
			}
			current, cancel := context.WithCancel(h.parent)
			stop := context.AfterFunc(session.Context(), cancel)
			original, err := kafka.OwnedMQContext(current, record.Headers)
			if err == nil {
				err = h.handle(original, record.Value)
			}
			stop()
			cancel()
			if ownedsource.FromContext(h.parent).Snapshot().Unconfirmed() {
				err = errOwnedPush
			}
			if h.parent.Err() != nil {
				return nil
			}
			if session.Context().Err() != nil {
				h.active.Fail()
				return nil
			}
			if err != nil {
				h.active.Fail()
				return errOwnedPush
			}
			// Processing and all its synchronous errgroup callbacks have finished before
			// marking. No goroutine/ticker or asynchronous unowned queue survives claim.
			if h.parent.Err() != nil || session.Context().Err() != nil {
				return nil
			}
			session.MarkMessage(record, "")
		}
	}
}
func (h *ownedPushClaim) handle(ctx context.Context, raw []byte) error {
	var message pbchat.PushMsgDataToMQ
	if proto.Unmarshal(raw, &message) != nil || message.MsgData == nil {
		return errOwnedPush
	}
	// Preserve the old raw delivery freshness rule; malformed/future timestamps
	// are not authorization, nor a reason to replay a source with lost ACK.
	if time.Now().Unix()-message.MsgData.SendTime/1000 > 10 {
		return nil
	}
	current, done, err := h.pusher.owned.bind(ctx)
	if err != nil {
		return err
	}
	defer done()
	if message.MsgData.SessionType == constant.SuperGroupChatType {
		return h.pusher.Push2SuperGroup(current, message.MsgData.GroupID, message.MsgData)
	}
	users := []string{message.MsgData.RecvID}
	if utils.GetSwitchFromOptions(message.MsgData.Options, constant.IsSenderSync) && message.MsgData.SendID != message.MsgData.RecvID {
		users = append(users, message.MsgData.SendID)
	}
	return h.pusher.Push2User(current, users, message.MsgData)
}

// The old method is retained for protocol compatibility and fails before any
// raw message/provider dispatch. It cannot mint a typed cut or delivery receipt.
type ownedRequiredPushServer struct {
	original *pushServer
	binding  *ownedPushBinding
}

func (s *ownedRequiredPushServer) PushMsg(context.Context, *pbpush.PushMsgReq) (*pbpush.PushMsgResp, error) {
	return nil, errOwnedPush
}
func (s *ownedRequiredPushServer) DelUserPushToken(ctx context.Context, req *pbpush.DelUserPushTokenReq) (*pbpush.DelUserPushTokenResp, error) {
	if req == nil {
		return nil, errOwnedPush
	}
	current, done, err := s.binding.bind(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	response, err := s.original.DelUserPushToken(current, req)
	if ownedsource.FromContext(s.binding.ctx).Snapshot().Unconfirmed() {
		s.binding.fail()
		return nil, errOwnedPush
	}
	return response, err
}
