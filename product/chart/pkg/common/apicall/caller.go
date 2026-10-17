// Copyright © 2023 OpenIM open source community. All rights reserved.
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

package apicall

import (
	"context"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/utils"
	"sync"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/protocol/auth"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/friend"
	"github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/protocol/user"
)

type CallerInterface interface {
	ImAdminTokenWithDefaultAdmin(ctx context.Context) (string, error)
	ImportFriend(ctx context.Context, ownerUserID string, friendUserID []string) error
	UserToken(ctx context.Context, userID string, platform int32) (string, error)
	InviteToGroup(ctx context.Context, userID string, groupIDs []string) error
	UpdateUserInfo(ctx context.Context, userID string, nickName string, faceURL string) error
	UpdateUserProfile(ctx context.Context, userID, nickname, faceURL string) error
	ForceOffLine(ctx context.Context, userID string) error
	RegisterUser(ctx context.Context, users []*sdkws.UserInfo) error
	FindGroupInfo(ctx context.Context, groupIDs []string) ([]*sdkws.GroupInfo, error)
	UserRegisterCount(ctx context.Context, start int64, end int64) (map[string]int64, int64, error)
	FriendUserIDs(ctx context.Context, userID string) ([]string, error)
	FindGroupMemberUserIDs(ctx context.Context, groupID string) ([]string, error)
	FindFriendUserIDs(ctx context.Context, userID string) ([]string, error)
	SendBusinessNotification(ctx context.Context, key string, data any, sendUserID string, recvUserID string) error
	SendMsg(ctx context.Context, req *SendMsgReq) (*SendMsgResp, error)
}

type Caller struct {
	token   string
	timeout time.Time
	lock    sync.Mutex
}

func NewCallerInterface() CallerInterface {
	return &Caller{}
}

func (c *Caller) ImportFriend(ctx context.Context, ownerUserID string, friendUserIDs []string) error {
	if len(friendUserIDs) == 0 {
		return nil
	}
	_, err := importFriend.Call(ctx, &friend.ImportFriendReq{
		OwnerUserID:   ownerUserID,
		FriendUserIDs: friendUserIDs,
	})
	return err
}

func (c *Caller) ImAdminTokenWithDefaultAdmin(ctx context.Context) (string, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if !internalToken(c.token) || c.timeout.Before(time.Now()) {
		userID := config.GetDefaultIMAdmin()
		token, err := c.UserToken(ctx, config.GetDefaultIMAdmin(), constant.AdminPlatformID)
		if err != nil {
			log.ZError(ctx, "get im admin token", err, "userID", userID)
			return "", err
		}
		log.ZDebug(ctx, "get im admin token", "userID", userID)
		c.token = token
		c.timeout = time.Now().Add(time.Minute * 5)
	}
	return c.token, nil
}

func (c *Caller) UserToken(ctx context.Context, userID string, platformID int32) (string, error) {
	resp, err := userToken.Call(mctx.WithApiToken(ctx, ""), &auth.UserTokenReq{
		Secret:     *config.Config.Secret,
		PlatformID: platformID,
		UserID:     userID,
	})
	if err != nil {
		return "", err
	}
	return resp.Token, nil
}

func (c *Caller) InviteToGroup(ctx context.Context, userID string, groupIDs []string) error {
	for _, groupID := range groupIDs {
		_, _ = inviteToGroup.Call(ctx, &group.InviteUserToGroupReq{
			GroupID:        groupID,
			Reason:         "",
			InvitedUserIDs: []string{userID},
		})
	}
	return nil
}

// InviteToGroupStrict is used by the durable account-provisioning worker. Unlike
// the legacy best-effort helper it propagates a failed membership write.
func (c *Caller) InviteToGroupStrict(ctx context.Context, userID, groupID string) error {
	_, err := inviteToGroup.Call(ctx, &group.InviteUserToGroupReq{GroupID: groupID, InvitedUserIDs: []string{userID}})
	return err
}

func (c *Caller) UpdateUserProfile(ctx context.Context, userID, nickname, faceURL string) error {
	_, err := updateUserProfile.Call(ctx, &profileUpdateReq{UserInfo: &profileInfo{UserID: userID, Nickname: &nickname, FaceURL: &faceURL}})
	return err
}

func (c *Caller) UpdateUserInfo(ctx context.Context, userID string, nickName string, faceURL string) error {
	_, err := updateUserInfo.Call(ctx, &user.UpdateUserInfoReq{UserInfo: &sdkws.UserInfo{
		UserID:   userID,
		Nickname: nickName,
		FaceURL:  faceURL,
	}})
	return err
}

func (c *Caller) RegisterUser(ctx context.Context, users []*sdkws.UserInfo) error {
	_, err := registerUser.Call(ctx, &user.UserRegisterReq{
		Secret: *config.Config.Secret,
		Users:  users,
	})
	return err
}

func (c *Caller) ForceOffLine(ctx context.Context, userID string) error {
	var firstErr error
	for id := range constant.PlatformID2Name {
		_, err := forceOffLine.Call(ctx, &auth.ForceLogoutReq{
			PlatformID: int32(id),
			UserID:     userID,
		})
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (c *Caller) FindGroupInfo(ctx context.Context, groupIDs []string) ([]*sdkws.GroupInfo, error) {
	resp, err := getGroupsInfo.Call(ctx, &group.GetGroupsInfoReq{
		GroupIDs: groupIDs,
	})
	if err != nil {
		return nil, err
	}
	return resp.GroupInfos, nil
}

func (c *Caller) UserRegisterCount(ctx context.Context, start int64, end int64) (map[string]int64, int64, error) {
	resp, err := registerUserCount.Call(ctx, &user.UserRegisterCountReq{
		Start: start,
		End:   end,
	})
	if err != nil {
		return nil, 0, err
	}
	return resp.Count, resp.Total, nil
}

func (c *Caller) FriendUserIDs(ctx context.Context, userID string) ([]string, error) {
	resp, err := friendUserIDs.Call(ctx, &friend.GetFriendIDsReq{UserID: userID})
	if err != nil {
		return nil, err
	}
	return resp.FriendIDs, nil
}

func (c *Caller) FindGroupMemberUserIDs(ctx context.Context, groupID string) ([]string, error) {
	resp, err := getGroupMemberUserIDs.Call(ctx, &group.GetGroupMemberUserIDsReq{GroupID: groupID})
	if err != nil {
		return nil, err
	}
	return resp.UserIDs, nil
}

func (c *Caller) FindFriendUserIDs(ctx context.Context, userID string) ([]string, error) {
	resp, err := getFriendIDs.Call(ctx, &friend.GetFriendIDsReq{UserID: userID})
	if err != nil {
		return nil, err
	}
	return resp.FriendIDs, nil
}

func (c *Caller) SendBusinessNotification(ctx context.Context, key string, data any, sendUserID string, recvUserID string) error {
	resp, err := sendBusinessNotification.Call(ctx, &SendBusinessNotificationReq{
		Key: key,
		Data: utils.StructToJsonString(&struct {
			Body any `json:"body"`
		}{Body: data}),
		SendUserID: sendUserID,
		RecvUserID: recvUserID,
	})
	if err != nil {
		log.ZError(ctx, "sendBusinessNotification failed", err, "key", key, "data", data, "sendUserID", sendUserID, "recvUserID", recvUserID)
		return err
	}
	log.ZDebug(ctx, "sendBusinessNotification success", "resp", resp, "key", key, "data", data, "sendUserID", sendUserID, "recvUserID", recvUserID)
	return err
}

func (c *Caller) SendMsg(ctx context.Context, req *SendMsgReq) (*SendMsgResp, error) {
	return sendMsg.Call(ctx, req)
}

// FindRegisteredUsers is used only to reconcile an authenticated registration
// receipt after an uncertain IM create response.
func (c *Caller) FindRegisteredUsers(ctx context.Context, userIDs []string) ([]*sdkws.UserInfo, error) {
	token, err := c.ImAdminTokenWithDefaultAdmin(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := NewApiCaller[user.GetDesignateUsersReq, user.GetDesignateUsersResp]("/user/get_users_info", imApi).Call(mctx.WithApiToken(ctx, token), &user.GetDesignateUsersReq{UserIDs: userIDs})
	if err != nil {
		return nil, err
	}
	return resp.UsersInfo, nil
}
