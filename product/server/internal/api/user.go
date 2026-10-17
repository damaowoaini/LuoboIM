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

package api

import (
	"context"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/msggateway"
	"github.com/OpenIMSDK/protocol/user"
	"github.com/OpenIMSDK/tools/a2r"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/checker"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/gin-gonic/gin"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/admincapability"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/registrationproof"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
)

type UserApi rpcclient.User

func NewUserApi(client rpcclient.User) UserApi {
	return UserApi(client)
}

func (u *UserApi) UserRegister(c *gin.Context) {
	var request user.UserRegisterReq
	if c.ShouldBindJSON(&request) != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("注册请求无效"))
		return
	}
	snapshot, err := registrationproof.Snapshot(request.Users)
	if err != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("注册资料无效"))
		return
	}
	if err := checker.Validate(&request); err != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("注册资料无效"))
		return
	}
	// Gin owns the authenticated operation/custom values used by production
	// RPC interceptors;request context alone need not contain these values.
	ctx := context.Context(c)
	if snapshot.HasMarkers() {
		value, present := c.Get(admincapability.ContextClaims)
		claims, valid := value.(*authverify.ClassifiedClaims)
		if !present || !valid || claims == nil || claims.Purpose != authverify.ServicePurpose {
			apiresp.GinError(c, errs.ErrNoPermission.Wrap("开户证明写入未获服务授权"))
			return
		}
		ctx, err = registrationproof.WithRPCProof(c, []byte(config.Config.Secret), c.GetHeader("token"), &request, time.Now())
		if err != nil {
			apiresp.GinError(c, errs.ErrNoPermission.Wrap("开户证明写入未获服务授权"))
			return
		}
	} else {
		ctx = c
	}
	response, err := u.Client.UserRegister(ctx, &request)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, response)
}

func (u *UserApi) UpdateUserInfo(c *gin.Context) {
	a2r.Call(user.UserClient.UpdateUserInfo, u.Client, c)
}
func (u *UserApi) UpdateUserInfoEx(c *gin.Context) {
	a2r.Call(user.UserClient.UpdateUserInfoEx, u.Client, c)
}
func (u *UserApi) SetGlobalRecvMessageOpt(c *gin.Context) {
	a2r.Call(user.UserClient.SetGlobalRecvMessageOpt, u.Client, c)
}

func (u *UserApi) GetUsersPublicInfo(c *gin.Context) {
	a2r.Call(user.UserClient.GetDesignateUsers, u.Client, c)
}

func (u *UserApi) GetAllUsersID(c *gin.Context) {
	// ✅ 权限检查：只允许管理员调用此接口
	if err := authverify.CheckAdmin(c); err != nil {
		apiresp.GinError(c, errs.ErrNoPermission.Wrap("only admin can get all user IDs"))
		return
	}
	a2r.Call(user.UserClient.GetAllUserID, u.Client, c)
}

func (u *UserApi) AccountCheck(c *gin.Context) {
	a2r.Call(user.UserClient.AccountCheck, u.Client, c)
}

func (u *UserApi) GetUsers(c *gin.Context) {
	// ✅ 权限检查：只允许管理员调用此接口
	if err := authverify.CheckAdmin(c); err != nil {
		apiresp.GinError(c, errs.ErrNoPermission.Wrap("only admin can get users list"))
		return
	}
	a2r.Call(user.UserClient.GetPaginationUsers, u.Client, c)
}

// GetUsersOnlineStatus Get user online status.
func (u *UserApi) GetUsersOnlineStatus(c *gin.Context) {
	var req msggateway.GetUsersOnlineStatusReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, errs.ErrArgs.WithDetail(err.Error()).Wrap())
		return
	}
	conns, err := u.Discov.GetConns(c, config.Config.RpcRegisterName.OpenImMessageGatewayName)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}

	var wsResult []*msggateway.GetUsersOnlineStatusResp_SuccessResult
	var respResult []*msggateway.GetUsersOnlineStatusResp_SuccessResult
	flag := false

	// Online push message
	for _, v := range conns {
		msgClient := msggateway.NewMsgGatewayClient(v)
		reply, err := msgClient.GetUsersOnlineStatus(c, &req)
		if err != nil {
			log.ZWarn(c, "GetUsersOnlineStatus rpc err", err)

			parseError := apiresp.ParseError(err)
			if parseError.ErrCode == errs.NoPermissionError {
				apiresp.GinError(c, err)
				return
			}
		} else {
			wsResult = append(wsResult, reply.SuccessResult...)
		}
	}
	// Traversing the userIDs in the api request body
	for _, v1 := range req.UserIDs {
		flag = false
		res := new(msggateway.GetUsersOnlineStatusResp_SuccessResult)
		// Iterate through the online results fetched from various gateways
		for _, v2 := range wsResult {
			// If matches the above description on the line, and vice versa
			if v2.UserID == v1 {
				flag = true
				res.UserID = v1
				res.Status = constant.OnlineStatus
				res.DetailPlatformStatus = append(res.DetailPlatformStatus, v2.DetailPlatformStatus...)
				break
			}
		}
		if !flag {
			res.UserID = v1
			res.Status = constant.OfflineStatus
		}
		respResult = append(respResult, res)
	}
	apiresp.GinSuccess(c, respResult)
}

func (u *UserApi) UserRegisterCount(c *gin.Context) {
	a2r.Call(user.UserClient.UserRegisterCount, u.Client, c)
}

// GetUsersOnlineTokenDetail Get user online token details.
func (u *UserApi) GetUsersOnlineTokenDetail(c *gin.Context) {
	// ✅ 权限检查：只允许管理员调用此接口
	if err := authverify.CheckAdmin(c); err != nil {
		apiresp.GinError(c, errs.ErrNoPermission.Wrap("only admin can get users online token detail"))
		return
	}

	var wsResult []*msggateway.GetUsersOnlineStatusResp_SuccessResult
	var respResult []*msggateway.SingleDetail
	flag := false
	var req msggateway.GetUsersOnlineStatusReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, errs.ErrArgs.WithDetail(err.Error()).Wrap())
		return
	}
	conns, err := u.Discov.GetConns(c, config.Config.RpcRegisterName.OpenImMessageGatewayName)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	// Online push message
	for _, v := range conns {
		msgClient := msggateway.NewMsgGatewayClient(v)
		reply, err := msgClient.GetUsersOnlineStatus(c, &req)
		if err != nil {
			log.ZWarn(c, "GetUsersOnlineStatus rpc  err", err)
			continue
		} else {
			wsResult = append(wsResult, reply.SuccessResult...)
		}
	}

	for _, v1 := range req.UserIDs {
		m := make(map[string][]string, 10)
		flag = false
		temp := new(msggateway.SingleDetail)
		for _, v2 := range wsResult {
			if v2.UserID == v1 {
				flag = true
				temp.UserID = v1
				temp.Status = constant.OnlineStatus
				for _, status := range v2.DetailPlatformStatus {
					if v, ok := m[status.Platform]; ok {
						m[status.Platform] = append(v, status.Token)
					} else {
						m[status.Platform] = []string{status.Token}
					}
				}
			}
		}
		for p, tokens := range m {
			t := new(msggateway.SinglePlatformToken)
			t.Platform = p
			// Browser administrators receive platform/count only, never user JWTs.
			if c.GetString("luobo.delegated.admin") == "" {
				t.Token = tokens
			}
			t.Total = int32(len(tokens))
			temp.SinglePlatformToken = append(temp.SinglePlatformToken, t)
		}

		if flag {
			respResult = append(respResult, temp)
		}
	}

	apiresp.GinSuccess(c, respResult)
}

// SubscriberStatus Presence status of subscribed users.
func (u *UserApi) SubscriberStatus(c *gin.Context) {
	a2r.Call(user.UserClient.SubscribeOrCancelUsersStatus, u.Client, c)
}

// GetUserStatus Get the online status of the user.
func (u *UserApi) GetUserStatus(c *gin.Context) {
	a2r.Call(user.UserClient.GetUserStatus, u.Client, c)
}

// GetSubscribeUsersStatus Get the online status of subscribers.
func (u *UserApi) GetSubscribeUsersStatus(c *gin.Context) {
	a2r.Call(user.UserClient.GetSubscribeUsersStatus, u.Client, c)
}

// ProcessUserCommandAdd user general function add
func (u *UserApi) ProcessUserCommandAdd(c *gin.Context) {
	a2r.Call(user.UserClient.ProcessUserCommandAdd, u.Client, c)
}

// ProcessUserCommandDelete user general function delete
func (u *UserApi) ProcessUserCommandDelete(c *gin.Context) {
	a2r.Call(user.UserClient.ProcessUserCommandDelete, u.Client, c)
}

// ProcessUserCommandUpdate  user general function update
func (u *UserApi) ProcessUserCommandUpdate(c *gin.Context) {
	a2r.Call(user.UserClient.ProcessUserCommandUpdate, u.Client, c)
}

// ProcessUserCommandGet user general function get
func (u *UserApi) ProcessUserCommandGet(c *gin.Context) {
	a2r.Call(user.UserClient.ProcessUserCommandGet, u.Client, c)
}

func (u *UserApi) AddNotificationAccount(c *gin.Context) {
	a2r.Call(user.UserClient.AddNotificationAccount, u.Client, c)
}

func (u *UserApi) UpdateNotificationAccountInfo(c *gin.Context) {
	a2r.Call(user.UserClient.UpdateNotificationAccountInfo, u.Client, c)
}

func (u *UserApi) SearchNotificationAccount(c *gin.Context) {
	a2r.Call(user.UserClient.SearchNotificationAccount, u.Client, c)
}

func (u *UserApi) ProcessUserCommandGetAll(c *gin.Context) {
	a2r.Call(user.UserClient.ProcessUserCommandGetAll, u.Client, c)
}
