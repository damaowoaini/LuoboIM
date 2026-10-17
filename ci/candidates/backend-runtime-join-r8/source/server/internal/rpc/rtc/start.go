package rtc

import (
	"context"
	"errors"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/startrpc"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/msg"
	pb "github.com/OpenIMSDK/protocol/rtc"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type rpcAccess struct {
	users   *rpcclient.UserRpcClient
	groups  *rpcclient.GroupRpcClient
	friends *rpcclient.FriendRpcClient
}

func (a *rpcAccess) Metadata(ctx context.Context, id, groupID string) (*pb.ParticipantMetaData, error) {
	user, err := a.users.GetUserInfo(ctx, id)
	if err != nil {
		return nil, err
	}
	result := &pb.ParticipantMetaData{UserInfo: &sdkws.PublicUserInfo{UserID: user.UserID, Nickname: user.Nickname, FaceURL: user.FaceURL, Ex: user.Ex}}
	if groupID != "" {
		group, err := a.groups.GetGroupInfo(ctx, groupID)
		if err != nil {
			return nil, err
		}
		if group.Status == constant.GroupStatusDismissed {
			return nil, deny("群已解散")
		}
		members, err := a.groups.GetGroupMemberInfos(ctx, groupID, []string{id}, true)
		if err != nil {
			return nil, err
		}
		if len(members) != 1 {
			return nil, deny("当前不在群中")
		}
		result.GroupInfo = group
		result.GroupMemberInfo = members[0]
	}
	return result, nil
}
func (a *rpcAccess) Invitees(ctx context.Context, inviter, group string, users []string) error {
	if len(users) > 0 {
		if _, err := a.users.GetUsersInfo(ctx, users); err != nil {
			return err
		}
	}
	if group != "" {
		if _, err := a.Metadata(ctx, inviter, group); err != nil {
			return err
		}
		if len(users) > 0 {
			if _, err := a.groups.GetGroupMemberInfos(ctx, group, users, true); err != nil {
				return err
			}
		}
		return nil
	}
	for _, id := range users {
		blocked, err := a.friends.IsBlocked(ctx, inviter, id)
		if err != nil {
			return err
		}
		if blocked {
			return deny("对方拒绝接收此邀请")
		}
		if config.Config.MessageVerify.FriendVerify != nil && *config.Config.MessageVerify.FriendVerify {
			friends, err := a.friends.IsFriend(ctx, inviter, id)
			if err != nil {
				return err
			}
			if !friends {
				return deny("仅好友可以发起此邀请")
			}
		}
	}
	return nil
}

type rpcDelivery struct {
	messages    *rpcclient.MessageRpcClient
	serviceUser string
}

func (d *rpcDelivery) Send(ctx context.Context, event *Outbox) error {
	ctx = context.WithValue(ctx, constant.OperationID, "rtc-event-"+event.ID)
	ctx = context.WithValue(ctx, constant.OpUserID, d.serviceUser)
	push := &sdkws.OfflinePushInfo{}
	if err := proto.Unmarshal(event.OfflinePush, push); err != nil {
		return err
	}
	_, err := d.messages.SendMsg(ctx, &msg.SendMsgReq{MsgData: &sdkws.MsgData{SendID: d.serviceUser, RecvID: event.Receiver, MsgFrom: constant.SysMsgType, SessionType: constant.NotificationChatType, ContentType: event.ContentType, ClientMsgID: event.ID, CreateTime: event.CreatedAt.UnixMilli(), Content: event.Payload, OfflinePushInfo: push, Options: map[string]bool{constant.IsNotNotification: false, constant.IsSendMsg: false, constant.IsHistory: false, constant.IsPersistent: true, constant.IsSenderSync: false, constant.IsConversationUpdate: false, constant.IsUnreadCount: false, constant.IsOfflinePush: len(event.OfflinePush) > 0}}})
	return err
}
func Start(discov discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) error {
	media, err := NewLiveKitFromEnv()
	if err != nil {
		return err
	}
	connection, err := unrelation.NewProcessRuntimeMongo(startrpc.OwnedRPCContext(server))
	if connection != nil {
		if trackErr := startrpc.OwnRPCDatabase(server, connection.GetDatabase().Name()); trackErr != nil {
			_ = connection.GetClient().Disconnect(context.Background())
			return trackErr
		}
		if trackErr := startrpc.OwnRPCResource(server, "mongo", connection.GetClient().Disconnect); trackErr != nil {
			_ = connection.GetClient().Disconnect(context.Background())
			return trackErr
		}
	}
	if err != nil {
		return err
	}

	rdb, err := cache.NewProcessRuntimeRedis(startrpc.OwnedRPCContext(server))
	if err != nil {
		return err
	}
	if err := startrpc.OwnRPCResource(server, "redis", func(context.Context) error { return rdb.Close() }); err != nil {
		_ = rdb.Close()
		return err
	}

	users := rpcclient.NewUserRpcClient(discov)
	groups := rpcclient.NewGroupRpcClient(discov)
	friends := rpcclient.NewFriendRpcClient(discov)
	messages := rpcclient.NewMessageRpcClient(discov)
	serviceUser := ""
	for _, id := range append(append([]string{}, config.Config.IMAdmin.UserID...), config.Config.Manager.UserID...) {
		if id != "" {
			serviceUser = id
			break
		}
	}
	if serviceUser == "" {
		return argument("RTC服务消息发送账号未配置")
	}
	parent := context.Background()
	owned := startrpc.OwnedRPCContext(server)
	gate := ownedcontrol.NamespaceRuntimeStartupFromContext(owned)
	if gate != nil {
		parent = owned
	}
	ctx := context.WithValue(parent, constant.OperationID, "rtc-start-"+uuid.NewString())
	ctx = context.WithValue(ctx, constant.OpUserID, serviceUser)
	if gate == nil {
		// Preserve the exact default/legacy lookup and any original error behavior.
		if _, err := users.GetUserInfo(ctx, serviceUser); err != nil {
			return err
		}
	}
	start, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	instance, err := New(start, connection.GetDatabase(), &rpcAccess{users: &users, groups: &groups, friends: &friends}, media, &rpcDelivery{messages: &messages, serviceUser: serviceUser})
	if err != nil {
		return err
	}
	instance.rdb = rdb
	pb.RegisterRtcServiceServer(server, instance)
	if gate != nil {
		// Actual SDK wrapper semantics, one exact serviceUser request. WaitForReady
		// queues this initial read until the independently admitted USER role really
		// Serves; it does not expose USER before admission or replay an application call.
		preparedUsers := users
		preparedUsers.Client = &ownedRTCReadyUserClient{UserClient: users.Client}
		activate := newOwnedRTCUserActivation(ctx, func() error { _, _, e := gate.KnownPublication(); return e }, func(call context.Context) error {
			_, e := preparedUsers.GetUserInfo(call, serviceUser)
			return e
		})
		if err := startrpc.OwnRPCNamespaceActivation(server, activate); err != nil {
			return err
		}
	}
	// Under private-v2 this is the original queued worker. The owner activates
	// it only after the synchronous user check succeeds, before Serve/announce.
	if !startrpc.OwnedRPCWorkers(server).Run(instance.Run) {
		return errors.New("owned RTC worker admission closed")
	}
	return nil
}
