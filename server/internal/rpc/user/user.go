// Copyright © 2023 OpenIM. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package user

import (
	"context"
	"errors"
	"github.com/OpenIMSDK/tools/pagination"
	"math/rand"
	"strings"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mongotx"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"

	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/riskcontrol"

	registry "github.com/OpenIMSDK/tools/discoveryregistry"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/convert"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	tablerelation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/registrationproof"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient/notification"

	pbuser "github.com/OpenIMSDK/protocol/user"
	"github.com/OpenIMSDK/tools/utils"
	"google.golang.org/grpc"
)

type userServer struct {
	controller.UserDatabase
	registrationAuthorizer   *registrationproof.Authorizer
	friendNotificationSender *notification.FriendNotificationSender
	userNotificationSender   *notification.UserNotificationSender
	friendRpcClient          *rpcclient.FriendRpcClient
	groupRpcClient           *rpcclient.GroupRpcClient
	RegisterCenter           registry.SvcDiscoveryRegistry
}

func (s *userServer) ProcessUserCommandGetAll(ctx context.Context, req *pbuser.ProcessUserCommandGetAllReq) (*pbuser.ProcessUserCommandGetAllResp, error) {
	if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	commands, err := s.UserDatabase.GetUserCommands(ctx, req.UserID, 0)
	if err != nil {
		return nil, err
	}
	result := &pbuser.ProcessUserCommandGetAllResp{CommandResp: make([]*pbuser.AllCommandInfoResp, 0, len(commands))}
	for _, command := range commands {
		result.CommandResp = append(result.CommandResp, &pbuser.AllCommandInfoResp{Type: command.Type, Uuid: command.Uuid, Value: command.Value, Ex: command.Ex, CreateTime: command.CreateTime})
	}
	return result, nil
}

func Start(client registry.SvcDiscoveryRegistry, server *grpc.Server) error {
	rdb, err := cache.NewRedis()
	if err != nil {
		return err
	}
	mongo, err := unrelation.NewMongo()
	if err != nil {
		return err
	}
	users, err := bootstrapUsers(config.Config.Manager.UserID, config.Config.Manager.Nickname, config.Config.IMAdmin.UserID, config.Config.IMAdmin.Nickname)
	if err != nil {
		return err
	}
	userDB, err := mgo.NewUserMongo(mongo.GetDatabase())
	if err != nil {
		return err
	}
	registrationAuthorizer := &registrationproof.Authorizer{Tokens: cache.NewMsgCacheModel(rdb), Nonces: registrationproof.RedisNonces{Client: rdb}, Secret: []byte(config.Config.Secret)}
	cache := cache.NewUserCacheRedis(rdb, userDB, cache.GetDefaultOpt())
	userMongoDB := unrelation.NewUserMongoDriver(mongo.GetDatabase())
	database := controller.NewUserDatabase(userDB, cache, mongotx.New(mongo.GetClient()), userMongoDB)
	friendRpcClient := rpcclient.NewFriendRpcClient(client)
	groupRpcClient := rpcclient.NewGroupRpcClient(client)
	msgRpcClient := rpcclient.NewMessageRpcClient(client)
	u := &userServer{
		UserDatabase:             database,
		registrationAuthorizer:   registrationAuthorizer,
		RegisterCenter:           client,
		friendRpcClient:          &friendRpcClient,
		groupRpcClient:           &groupRpcClient,
		friendNotificationSender: notification.NewFriendNotificationSender(&msgRpcClient, notification.WithDBFunc(database.FindWithError)),
		userNotificationSender:   notification.NewUserNotificationSender(&msgRpcClient, notification.WithUserFunc(database.FindWithError)),
	}
	pbuser.RegisterUserServer(server, u)
	return u.UserDatabase.InitOnce(context.Background(), users)
}

// Manager and notification accounts may intentionally share an ID (as in the
// shipped configuration). AppAdmin is the management role; the numerically
// larger AppNotificationAdmin is a different, narrower account category.
func bootstrapUsers(managerIDs, managerNames, notificationIDs, notificationNames []string) ([]*tablerelation.UserModel, error) {
	if len(managerIDs) != len(managerNames) || len(notificationIDs) != len(notificationNames) {
		return nil, errors.New("bootstrap account IDs and nicknames must have equal lengths")
	}
	users := make([]*tablerelation.UserModel, 0, len(managerIDs)+len(notificationIDs))
	seen := make(map[string]bool)
	for _, group := range []struct {
		ids, names []string
		level      int32
	}{{managerIDs, managerNames, constant.AppAdmin}, {notificationIDs, notificationNames, constant.AppNotificationAdmin}} {
		for i, id := range group.ids {
			if strings.TrimSpace(id) == "" {
				return nil, errors.New("bootstrap account ID must not be empty")
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			users = append(users, &tablerelation.UserModel{UserID: id, Nickname: group.names[i], AppMangerLevel: group.level})
		}
	}
	return users, nil
}

func (s *userServer) GetDesignateUsers(ctx context.Context, req *pbuser.GetDesignateUsersReq) (resp *pbuser.GetDesignateUsersResp, err error) {
	resp = &pbuser.GetDesignateUsersResp{}
	users, err := s.FindWithError(ctx, req.UserIDs)
	if err != nil {
		return nil, err
	}
	resp.UsersInfo = convert.UsersDB2Pb(users)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *userServer) UpdateUserInfo(ctx context.Context, req *pbuser.UpdateUserInfoReq) (resp *pbuser.UpdateUserInfoResp, err error) {
	resp = &pbuser.UpdateUserInfoResp{}
	if req == nil || req.UserInfo == nil {
		return nil, errs.ErrArgs.Wrap("userInfo is required")
	}
	err = authverify.CheckAccessV3(ctx, req.UserInfo.UserID)
	if err != nil {
		return nil, err
	}
	if blocked, err := riskcontrol.IsActionBlocked(ctx, riskcontrol.RiskDisableUpdateUserKey); err != nil {
		return nil, err
	} else if blocked {
		return nil, errs.ErrNoPermission.Wrap("当前时段禁止修改用户基本信息")
	}
	if err := CallbackBeforeUpdateUserInfo(ctx, req); err != nil {
		return nil, err
	}
	data := convert.UserPb2DBMap(req.UserInfo)
	if err := s.UpdateByMap(ctx, req.UserInfo.UserID, data); err != nil {
		return nil, err
	}
	_ = s.friendNotificationSender.UserInfoUpdatedNotification(ctx, req.UserInfo.UserID)
	friends, err := s.friendRpcClient.GetFriendIDs(ctx, req.UserInfo.UserID)
	if err != nil {
		return nil, err
	}
	if req.UserInfo.Nickname != "" || req.UserInfo.FaceURL != "" {
		if err := s.groupRpcClient.NotificationUserInfoUpdate(ctx, req.UserInfo.UserID); err != nil {
			log.ZError(ctx, "NotificationUserInfoUpdate", err)
		}
	}
	for _, friendID := range friends {
		s.friendNotificationSender.FriendInfoUpdatedNotification(ctx, req.UserInfo.UserID, friendID)
	}
	if err := CallbackAfterUpdateUserInfo(ctx, req); err != nil {
		return nil, err
	}
	if err := s.groupRpcClient.NotificationUserInfoUpdate(ctx, req.UserInfo.UserID); err != nil {
		log.ZError(ctx, "NotificationUserInfoUpdate", err, "userID", req.UserInfo.UserID)
	}
	return resp, nil
}
func (s *userServer) UpdateUserInfoEx(ctx context.Context, req *pbuser.UpdateUserInfoExReq) (resp *pbuser.UpdateUserInfoExResp, err error) {
	resp = &pbuser.UpdateUserInfoExResp{}
	if req == nil || req.UserInfo == nil {
		return nil, errs.ErrArgs.Wrap("userInfo is required")
	}
	err = authverify.CheckAccessV3(ctx, req.UserInfo.UserID)
	if err != nil {
		return nil, err
	}

	if err = CallbackBeforeUpdateUserInfoEx(ctx, req); err != nil {
		return nil, err
	}
	data := convert.UserPb2DBMapEx(req.UserInfo)
	if err = s.UpdateByMap(ctx, req.UserInfo.UserID, data); err != nil {
		return nil, err
	}
	_ = s.friendNotificationSender.UserInfoUpdatedNotification(ctx, req.UserInfo.UserID)
	friends, err := s.friendRpcClient.GetFriendIDs(ctx, req.UserInfo.UserID)
	if err != nil {
		return nil, err
	}
	if req.UserInfo.Nickname != nil || req.UserInfo.FaceURL != nil {
		if err := s.groupRpcClient.NotificationUserInfoUpdate(ctx, req.UserInfo.UserID); err != nil {
			log.ZError(ctx, "NotificationUserInfoUpdate", err)
		}
	}
	for _, friendID := range friends {
		s.friendNotificationSender.FriendInfoUpdatedNotification(ctx, req.UserInfo.UserID, friendID)
	}
	if err := CallbackAfterUpdateUserInfoEx(ctx, req); err != nil {
		return nil, err
	}
	if err := s.groupRpcClient.NotificationUserInfoUpdate(ctx, req.UserInfo.UserID); err != nil {
		log.ZError(ctx, "NotificationUserInfoUpdate", err, "userID", req.UserInfo.UserID)
	}
	return resp, nil
}
func (s *userServer) SetGlobalRecvMessageOpt(ctx context.Context, req *pbuser.SetGlobalRecvMessageOptReq) (resp *pbuser.SetGlobalRecvMessageOptResp, err error) {
	resp = &pbuser.SetGlobalRecvMessageOptResp{}
	if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	if _, err := s.FindWithError(ctx, []string{req.UserID}); err != nil {
		return nil, err
	}
	m := make(map[string]any, 1)
	m["global_recv_msg_opt"] = req.GlobalRecvMsgOpt
	if err := s.UpdateByMap(ctx, req.UserID, m); err != nil {
		return nil, err
	}
	s.friendNotificationSender.UserInfoUpdatedNotification(ctx, req.UserID)
	return resp, nil
}

func (s *userServer) AccountCheck(ctx context.Context, req *pbuser.AccountCheckReq) (resp *pbuser.AccountCheckResp, err error) {
	resp = &pbuser.AccountCheckResp{}
	if utils.Duplicate(req.CheckUserIDs) {
		return nil, errs.ErrArgs.Wrap("userID repeated")
	}
	err = authverify.CheckAdmin(ctx)
	if err != nil {
		return nil, err
	}
	users, err := s.Find(ctx, req.CheckUserIDs)
	if err != nil {
		return nil, err
	}
	userIDs := make(map[string]any, 0)
	for _, v := range users {
		userIDs[v.UserID] = nil
	}
	for _, v := range req.CheckUserIDs {
		temp := &pbuser.AccountCheckRespSingleUserStatus{UserID: v}
		if _, ok := userIDs[v]; ok {
			temp.AccountStatus = constant.Registered
		} else {
			temp.AccountStatus = constant.UnRegistered
		}
		resp.Results = append(resp.Results, temp)
	}
	return resp, nil
}

func (s *userServer) GetPaginationUsers(ctx context.Context, req *pbuser.GetPaginationUsersReq) (resp *pbuser.GetPaginationUsersResp, err error) {
	if err = authverify.CheckAdmin(ctx); err != nil {
		return nil, err
	}
	total, users, err := s.PageFindUser(ctx, constant.IMOrdinaryUser, req.Pagination)
	if err != nil {
		return nil, err
	}
	return &pbuser.GetPaginationUsersResp{Total: int32(total), Users: convert.UsersDB2Pb(users)}, err
}

func (s *userServer) UserRegister(ctx context.Context, req *pbuser.UserRegisterReq) (resp *pbuser.UserRegisterResp, err error) {
	resp = &pbuser.UserRegisterResp{}
	if req == nil || len(req.Users) == 0 {
		return nil, errs.ErrArgs.Wrap("users is empty")
	}
	if req.Secret != config.Config.Secret {
		log.ZDebug(ctx, "UserRegister secret mismatch")
		return nil, errs.ErrNoPermission.Wrap("secret invalid")
	}
	snapshot, err := registrationproof.Snapshot(req.Users)
	if err != nil {
		return nil, errs.ErrArgs.Wrap("注册资料无效")
	}
	ctx, err = s.registrationAuthorizer.Authorize(ctx, req, snapshot)
	if err != nil {
		return nil, err
	}
	if utils.DuplicateAny(req.Users, func(e *sdkws.UserInfo) string { return e.UserID }) {
		return nil, errs.ErrArgs.Wrap("userID repeated")
	}

	// ========== 注意：userID 已在 chart 服务生成时添加了 serverTag 前缀，这里不再添加 ==========
	log.ZInfo(ctx, "用户注册", "用户数量", len(req.Users))

	userIDs := make([]string, 0)
	for _, user := range req.Users {
		if user.UserID == "" {
			return nil, errs.ErrArgs.Wrap("userID is empty")
		}
		if strings.Contains(user.UserID, ":") {
			return nil, errs.ErrArgs.Wrap("userID contains ':' is invalid userID")
		}
		userIDs = append(userIDs, user.UserID)
	}
	exist, err := s.IsExist(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	if exist {
		return nil, errs.ErrRegisteredAlready.Wrap("userID registered already")
	}
	if err := CallbackBeforeUserRegister(ctx, req); err != nil {
		return nil, err
	}
	if err := snapshot.ProtectCallback(req.Users); err != nil {
		return nil, errs.ErrNoPermission.Wrap("注册回调不能改变账号归属证明")
	}
	now := time.Now()
	users := make([]*tablerelation.UserModel, 0, len(req.Users))
	for _, user := range req.Users {
		users = append(users, &tablerelation.UserModel{
			UserID:           user.UserID,
			Nickname:         user.Nickname,
			FaceURL:          user.FaceURL,
			Ex:               user.Ex,
			CreateTime:       now,
			AppMangerLevel:   user.AppMangerLevel,
			GlobalRecvMsgOpt: user.GlobalRecvMsgOpt,
		})
	}
	if err := s.Create(ctx, users); err != nil {
		return nil, err
	}

	if err := CallbackAfterUserRegister(ctx, req); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *userServer) GetGlobalRecvMessageOpt(ctx context.Context, req *pbuser.GetGlobalRecvMessageOptReq) (resp *pbuser.GetGlobalRecvMessageOptResp, err error) {
	user, err := s.FindWithError(ctx, []string{req.UserID})
	if err != nil {
		return nil, err
	}
	return &pbuser.GetGlobalRecvMessageOptResp{GlobalRecvMsgOpt: user[0].GlobalRecvMsgOpt}, nil
}

// GetAllUserID Get user account by page.
func (s *userServer) GetAllUserID(ctx context.Context, req *pbuser.GetAllUserIDReq) (resp *pbuser.GetAllUserIDResp, err error) {
	if err = authverify.CheckAdmin(ctx); err != nil {
		return nil, err
	}
	total, userIDs, err := s.UserDatabase.GetAllUserID(ctx, req.Pagination)
	if err != nil {
		return nil, err
	}
	return &pbuser.GetAllUserIDResp{Total: int32(total), UserIDs: userIDs}, nil
}

// filterVisibleStatusUserIDs 仅允许查看本人及好友的在线状态；管理员可查看全部。
func (s *userServer) filterVisibleStatusUserIDs(ctx context.Context, viewerUserID string, targetUserIDs []string) ([]string, error) {
	if len(targetUserIDs) == 0 {
		return targetUserIDs, nil
	}
	if authverify.IsAppManagerUid(ctx) {
		return targetUserIDs, nil
	}
	friendIDs, err := s.friendRpcClient.GetFriendIDs(ctx, viewerUserID)
	if err != nil {
		return nil, err
	}
	allowedSet := make(map[string]struct{}, len(friendIDs)+1)
	allowedSet[viewerUserID] = struct{}{}
	for _, id := range friendIDs {
		allowedSet[id] = struct{}{}
	}
	allowed := make([]string, 0, len(targetUserIDs))
	for _, id := range targetUserIDs {
		if _, ok := allowedSet[id]; ok {
			allowed = append(allowed, id)
		}
	}
	return allowed, nil
}

// SubscribeOrCancelUsersStatus Subscribe online or cancel online users.
func (s *userServer) SubscribeOrCancelUsersStatus(ctx context.Context, req *pbuser.SubscribeOrCancelUsersStatusReq) (resp *pbuser.SubscribeOrCancelUsersStatusResp, err error) {
	if err = authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	if req.Genre == constant.SubscriberUser {
		allowedUserIDs, err := s.filterVisibleStatusUserIDs(ctx, req.UserID, req.UserIDs)
		if err != nil {
			return nil, err
		}
		if len(allowedUserIDs) == 0 {
			return &pbuser.SubscribeOrCancelUsersStatusResp{}, nil
		}
		err = s.UserDatabase.SubscribeUsersStatus(ctx, req.UserID, allowedUserIDs)
		if err != nil {
			return nil, err
		}
		var status []*pbuser.OnlineStatus
		status, err = s.UserDatabase.GetUserStatus(ctx, allowedUserIDs)
		if err != nil {
			return nil, err
		}
		return &pbuser.SubscribeOrCancelUsersStatusResp{StatusList: status}, nil
	} else if req.Genre == constant.Unsubscribe {
		err = s.UserDatabase.UnsubscribeUsersStatus(ctx, req.UserID, req.UserIDs)
		if err != nil {
			return nil, err
		}
	}
	return &pbuser.SubscribeOrCancelUsersStatusResp{}, nil
}

// GetUserStatus Get the online status of the user.
func (s *userServer) GetUserStatus(ctx context.Context, req *pbuser.GetUserStatusReq) (resp *pbuser.GetUserStatusResp,
	err error) {
	if err = authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	allowedUserIDs, err := s.filterVisibleStatusUserIDs(ctx, req.UserID, req.UserIDs)
	if err != nil {
		return nil, err
	}
	if len(allowedUserIDs) == 0 {
		return &pbuser.GetUserStatusResp{}, nil
	}
	onlineStatusList, err := s.UserDatabase.GetUserStatus(ctx, allowedUserIDs)
	if err != nil {
		return nil, err
	}
	return &pbuser.GetUserStatusResp{StatusList: onlineStatusList}, nil
}

// SetUserStatus Synchronize user's online status.
func (s *userServer) SetUserStatus(ctx context.Context, req *pbuser.SetUserStatusReq) (resp *pbuser.SetUserStatusResp,
	err error) {
	err = s.UserDatabase.SetUserStatus(ctx, req.UserID, req.Status, req.PlatformID)
	if err != nil {
		return nil, err
	}
	list, err := s.UserDatabase.GetSubscribedList(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return &pbuser.SetUserStatusResp{}, nil
	}
	friendIDs, err := s.friendRpcClient.GetFriendIDs(ctx, req.UserID)
	if err != nil {
		log.ZWarn(ctx, "SetUserStatus get friend ids failed, skip status notify", err, "userID", req.UserID)
		return &pbuser.SetUserStatusResp{}, nil
	}
	friendSet := make(map[string]struct{}, len(friendIDs))
	for _, id := range friendIDs {
		friendSet[id] = struct{}{}
	}
	for _, userID := range list {
		if _, ok := friendSet[userID]; !ok {
			continue
		}
		tips := &sdkws.UserStatusChangeTips{
			FromUserID: req.UserID,
			ToUserID:   userID,
			Status:     req.Status,
			PlatformID: req.PlatformID,
		}
		s.userNotificationSender.UserStatusChangeNotification(ctx, tips)
	}

	return &pbuser.SetUserStatusResp{}, nil
}

// GetSubscribeUsersStatus Get the online status of subscribers.
func (s *userServer) GetSubscribeUsersStatus(ctx context.Context,
	req *pbuser.GetSubscribeUsersStatusReq) (*pbuser.GetSubscribeUsersStatusResp, error) {
	if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	userList, err := s.UserDatabase.GetAllSubscribeList(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	userList, err = s.filterVisibleStatusUserIDs(ctx, req.UserID, userList)
	if err != nil {
		return nil, err
	}
	if len(userList) == 0 {
		return &pbuser.GetSubscribeUsersStatusResp{}, nil
	}
	onlineStatusList, err := s.UserDatabase.GetUserStatus(ctx, userList)
	if err != nil {
		return nil, err
	}
	return &pbuser.GetSubscribeUsersStatusResp{StatusList: onlineStatusList}, nil
}

// ProcessUserCommandAdd user general function add
func (s *userServer) ProcessUserCommandAdd(ctx context.Context, req *pbuser.ProcessUserCommandAddReq) (*pbuser.ProcessUserCommandAddResp, error) {

	if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	if err := req.Check(); err != nil {
		return nil, errs.ErrArgs.Wrap(err.Error())
	}
	if err := s.UserDatabase.AddUserCommand(ctx, req.UserID, req.Type, req.Uuid, req.GetValue().GetValue(), req.GetEx().GetValue()); err != nil {
		return nil, err
	}
	return &pbuser.ProcessUserCommandAddResp{}, nil
}

// ProcessUserCommandDelete user general function delete
func (s *userServer) ProcessUserCommandDelete(ctx context.Context, req *pbuser.ProcessUserCommandDeleteReq) (*pbuser.ProcessUserCommandDeleteResp, error) {
	if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	// Assuming you have a method in s.UserDatabase to delete a user command
	err := s.UserDatabase.DeleteUserCommand(ctx, req.UserID, req.Type, req.Uuid)
	if err != nil {
		return nil, err
	}
	return &pbuser.ProcessUserCommandDeleteResp{}, nil
}

// ProcessUserCommandUpdate user general function update
func (s *userServer) ProcessUserCommandUpdate(ctx context.Context, req *pbuser.ProcessUserCommandUpdateReq) (*pbuser.ProcessUserCommandUpdateResp, error) {

	if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	if err := req.Check(); err != nil {
		return nil, errs.ErrArgs.Wrap(err.Error())
	}
	var value, ex *string
	if req.Value != nil {
		v := req.Value.Value
		value = &v
	}
	if req.Ex != nil {
		v := req.Ex.Value
		ex = &v
	}
	if err := s.UserDatabase.UpdateUserCommand(ctx, req.UserID, req.Type, req.Uuid, value, ex); err != nil {
		return nil, err
	}
	return &pbuser.ProcessUserCommandUpdateResp{}, nil
}

func (s *userServer) ProcessUserCommandGet(ctx context.Context, req *pbuser.ProcessUserCommandGetReq) (*pbuser.ProcessUserCommandGetResp, error) {

	if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	if err := req.Check(); err != nil {
		return nil, errs.ErrArgs.Wrap(err.Error())
	}
	commands, err := s.UserDatabase.GetUserCommands(ctx, req.UserID, req.Type)
	if err != nil {
		return nil, err
	}
	return &pbuser.ProcessUserCommandGetResp{CommandResp: commands}, nil
}

func (s *userServer) AddNotificationAccount(ctx context.Context, req *pbuser.AddNotificationAccountReq) (*pbuser.AddNotificationAccountResp, error) {
	if err := authverify.CheckIMAdmin(ctx); err != nil {
		return nil, err
	}

	if req.UserID == "" {
		for i := 0; i < 20; i++ {
			userId := s.genUserID()
			_, err := s.UserDatabase.FindWithError(ctx, []string{userId})
			if err == nil {
				continue
			}
			req.UserID = userId
			break
		}
		if req.UserID == "" {
			return nil, errs.ErrInternalServer.Wrap("gen user id failed")
		}
	}

	user := &tablerelation.UserModel{
		UserID:         req.UserID,
		Nickname:       req.NickName,
		FaceURL:        req.FaceURL,
		CreateTime:     time.Now(),
		AppMangerLevel: constant.AppNotificationAdmin,
	}
	if err := s.UserDatabase.Create(ctx, []*tablerelation.UserModel{user}); err != nil {
		return nil, err
	}

	return &pbuser.AddNotificationAccountResp{
		UserID:   req.UserID,
		NickName: req.NickName,
		FaceURL:  req.FaceURL,
	}, nil
}

func (s *userServer) UpdateNotificationAccountInfo(ctx context.Context, req *pbuser.UpdateNotificationAccountInfoReq) (*pbuser.UpdateNotificationAccountInfoResp, error) {
	if err := authverify.CheckIMAdmin(ctx); err != nil {
		return nil, err
	}

	if _, err := s.UserDatabase.FindWithError(ctx, []string{req.UserID}); err != nil {
		return nil, errs.ErrArgs.Wrap()
	}

	user := map[string]interface{}{}

	if req.NickName != "" {
		user["nickname"] = req.NickName
	}

	if req.FaceURL != "" {
		user["face_url"] = req.FaceURL
	}

	if err := s.UserDatabase.UpdateByMap(ctx, req.UserID, user); err != nil {
		return nil, err
	}

	return &pbuser.UpdateNotificationAccountInfoResp{}, nil
}

func (s *userServer) SearchNotificationAccount(ctx context.Context, req *pbuser.SearchNotificationAccountReq) (*pbuser.SearchNotificationAccountResp, error) {
	if err := authverify.CheckIMAdmin(ctx); err != nil {
		return nil, err
	}

	var users []*relation.UserModel
	var err error
	if req.Keyword != "" {
		users, err = s.UserDatabase.Find(ctx, []string{req.Keyword})
		if err != nil {
			return nil, err
		}
		resp := s.userModelToResp(users, req.Pagination)
		if resp.Total != 0 {
			return resp, nil
		}
		users, err = s.UserDatabase.FindByNickname(ctx, req.Keyword)
		if err != nil {
			return nil, err
		}
		resp = s.userModelToResp(users, req.Pagination)
		return resp, nil
	}

	users, err = s.UserDatabase.FindNotification(ctx, constant.AppNotificationAdmin)
	if err != nil {
		return nil, err
	}

	resp := s.userModelToResp(users, req.Pagination)
	return resp, nil
}

func (s *userServer) GetNotificationAccount(ctx context.Context, req *pbuser.GetNotificationAccountReq) (*pbuser.GetNotificationAccountResp, error) {
	if req.UserID == "" {
		return nil, errs.ErrArgs.Wrap("userID is empty")
	}
	user, err := s.UserDatabase.GetUserByID(ctx, req.UserID)
	if err != nil {
		return nil, errs.ErrUserIDNotFound.Wrap()
	}
	if user.AppMangerLevel == constant.AppAdmin || user.AppMangerLevel == constant.AppNotificationAdmin {
		return &pbuser.GetNotificationAccountResp{}, nil
	}

	return nil, errs.ErrNoPermission.Wrap("notification messages cannot be sent for this ID")
}

func (s *userServer) genUserID() string {
	const l = 10
	data := make([]byte, l)
	rand.Read(data)
	chars := []byte("0123456789")
	for i := 0; i < len(data); i++ {
		if i == 0 {
			data[i] = chars[1:][data[i]%9]
		} else {
			data[i] = chars[data[i]%10]
		}
	}
	return string(data)
}

func (s *userServer) userModelToResp(users []*relation.UserModel, pagination pagination.Pagination) *pbuser.SearchNotificationAccountResp {
	accounts := make([]*pbuser.NotificationAccountInfo, 0)
	var total int64
	for _, v := range users {
		if v.AppMangerLevel == constant.AppNotificationAdmin && !utils.IsContain(v.UserID, config.Config.IMAdmin.UserID) {
			temp := &pbuser.NotificationAccountInfo{
				UserID:   v.UserID,
				FaceURL:  v.FaceURL,
				NickName: v.Nickname,
			}
			accounts = append(accounts, temp)
			total += 1
		}
	}

	notificationAccounts := utils.Paginate(accounts, int(pagination.GetPageNumber()), int(pagination.GetShowNumber()))

	return &pbuser.SearchNotificationAccountResp{Total: total, NotificationAccounts: notificationAccounts}
}
