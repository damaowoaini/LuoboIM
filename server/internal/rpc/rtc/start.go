package rtc

import (
	"context"
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
	connection, err := unrelation.NewMongo()
	if err != nil {
		return err
	}
	rdb, err := cache.NewRedis()
	if err != nil {
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
	ctx := context.WithValue(context.Background(), constant.OperationID, "rtc-start-"+uuid.NewString())
	ctx = context.WithValue(ctx, constant.OpUserID, serviceUser)
	if _, err := users.GetUserInfo(ctx, serviceUser); err != nil {
		return err
	}
	start, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	instance, err := New(start, connection.GetDatabase(), &rpcAccess{users: &users, groups: &groups, friends: &friends}, media, &rpcDelivery{messages: &messages, serviceUser: serviceUser})
	if err != nil {
		return err
	}
	instance.rdb = rdb
	pb.RegisterRtcServiceServer(server, instance)
	go instance.Run(context.Background())
	return nil
}
