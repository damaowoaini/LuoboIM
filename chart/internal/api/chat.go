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

package api

import (
	"encoding/json"
	"io"
	"math/rand"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/admindelegation"
	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/apistruct"
	constant2 "github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/common/registration"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/checker"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/a2r"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	"github.com/OpenIMSDK/chat/pkg/proto/admin"
	"github.com/OpenIMSDK/chat/pkg/proto/chat"
)

func NewChat(chatConn, adminConn grpc.ClientConnInterface) *ChatApi {
	return &ChatApi{chatClient: chat.NewChatClient(chatConn), adminClient: admin.NewAdminClient(adminConn), imApiCaller: apicall.NewCallerInterface()}
}

type ChatApi struct {
	registrations *registrationAPI
	adminSessions *admindelegation.Service
	chatClient    chat.ChatClient
	adminClient   admin.AdminClient
	imApiCaller   apicall.CallerInterface
}

// ################## ACCOUNT ##################

func (o *ChatApi) SendVerifyCode(c *gin.Context) {
	req := chat.SendVerifyCodeReq{}

	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	ip, err := o.getClientIP(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	req.Ip = ip
	resp, err := o.chatClient.SendVerifyCode(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, resp)
}

func (o *ChatApi) VerifyCode(c *gin.Context) {
	a2r.Call(chat.ChatClient.VerifyCode, o.chatClient, c)
}

func (o *ChatApi) RegisterUser(c *gin.Context) {
	var req chat.RegisterUserReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	log.ZInfo(c, "registerUser", "platform", req.Platform)
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	if mctx.HaveOpUser(c) {
		if _, err := mctx.CheckAdmin(c); err != nil {
			apiresp.GinError(c, err)
			return
		}
		if o.adminSessions == nil {
			apiresp.GinError(c, errs.ErrNoPermission.Wrap("管理员会话不可用"))
			return
		}
		actor, _, err := o.adminSessions.AuthorizeSession(c, c.GetHeader("token"))
		expected, _ := mctx.CheckAdmin(c)
		if err != nil || actor != expected {
			apiresp.GinError(c, errs.ErrNoPermission.Wrap("管理员会话已失效"))
			return
		}
	}
	if o.registrations != nil && req.User != nil {
		handled, err := o.registrations.retry(c, req.User.Email, req.User.AreaCode, req.User.PhoneNumber)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		if handled {
			return
		}
	}
	// 请求体里的 ip 只有可信上游（路由服务）代报时才认，客户端自己填的一律忽略
	req.Ip = resolveClientIP(c, req.Ip)
	respRegisterUser, err := o.chatClient.RegisterUser(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if respRegisterUser.ProvisioningReceiptID != "" {
		if !mctx.HaveOpUser(c) {
			if respRegisterUser.ProvisioningPending || (respRegisterUser.ProvisioningState != "direct_only" && respRegisterUser.ProvisioningState != "bound") || respRegisterUser.UserID == "" {
				if respRegisterUser.UserID != "" {
					registrationCommittedFailure(c, errs.ErrNoPermission.Wrap(registrationPendingMessage))
				} else {
					apiresp.GinError(c, errs.ErrNoPermission.Wrap(registrationPendingMessage))
				}
				return
			}
			o.completePublicRegistration(c, respRegisterUser.UserID, respRegisterUser.ChatToken, req.Platform, req.AutoLogin)
			return
		}
		apiresp.GinSuccess(c, &apistruct.UserRegisterResp{UserID: respRegisterUser.UserID, AdminRequestID: respRegisterUser.AdminRequestID, ProvisioningReceiptID: respRegisterUser.ProvisioningReceiptID, ProvisioningState: respRegisterUser.ProvisioningState, ProvisioningPending: respRegisterUser.ProvisioningPending, Recovered: respRegisterUser.Recovered})
		return
	}
	userInfo := &sdkws.UserInfo{
		UserID:     respRegisterUser.UserID,
		Nickname:   req.User.Nickname,
		FaceURL:    req.User.FaceURL,
		CreateTime: time.Now().UnixMilli(),
	}
	if id := registration.ID(c); id != "" && o.registrations != nil {
		identity, identityErr := registration.Identity(req.User.Email, req.User.AreaCode, req.User.PhoneNumber)
		if identityErr != nil {
			apiresp.GinError(c, identityErr)
			return
		}
		receipt, receiptErr := o.registrations.find(c, id, identity)
		if receiptErr != nil {
			apiresp.GinError(c, receiptErr)
			return
		}
		err = o.registrations.ensureIM(c, receipt)
	} else {
		err = o.imApiCaller.RegisterUser(c, []*sdkws.UserInfo{userInfo})
	}
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiCtx := mctx.WithApiToken(c, imToken)
	rpcCtx := mctx.WithAdminUser(c)
	if resp, err := o.adminClient.FindDefaultFriend(rpcCtx, &admin.FindDefaultFriendReq{}); err == nil {
		// 随机选择一个默认好友添加
		if len(resp.UserIDs) > 0 {
			randomIndex := rand.Intn(len(resp.UserIDs))
			_ = o.imApiCaller.ImportFriend(apiCtx, respRegisterUser.UserID, []string{resp.UserIDs[randomIndex]})
			log.ZInfo(c, "Add random default friend", "userID", respRegisterUser.UserID, "friendID", resp.UserIDs[randomIndex])
		}
	}
	if resp, err := o.adminClient.FindDefaultGroup(rpcCtx, &admin.FindDefaultGroupReq{}); err == nil {
		_ = o.imApiCaller.InviteToGroup(apiCtx, respRegisterUser.UserID, resp.GroupIDs)
	}
	o.completePublicRegistration(c, respRegisterUser.UserID, respRegisterUser.ChatToken, req.Platform, req.AutoLogin)
}

func (o *ChatApi) completePublicRegistration(c *gin.Context, userID, chatToken string, platform int32, autoLogin bool) {
	resp := apistruct.UserRegisterResp{UserID: userID, ChatToken: chatToken}
	if autoLogin {
		// Route registration persists its IM receipt before account creation. The
		// final normal-token issuer correctly refuses that receipt until ensureIM
		// confirms its marker; complete the original autoLogin contract only now.
		if resp.ChatToken == "" {
			issued, issueErr := o.adminClient.CreateToken(mctx.WithAdminUser(c), &admin.CreateTokenReq{UserID: userID, UserType: constant2.NormalUser})
			if issueErr != nil || issued == nil || issued.Token == "" {
				if issueErr == nil {
					issueErr = errs.ErrInternalServer.Wrap("注册已完成，登录令牌尚未确认，请重新登录")
				}
				registrationCommittedFailure(c, issueErr)
				return
			}
			resp.ChatToken = issued.Token
		}
		var err error
		resp.ImToken, err = o.imApiCaller.UserToken(c, userID, platform)
		if err != nil || resp.ImToken == "" {
			if err == nil {
				err = errs.ErrInternalServer.Wrap("登录令牌尚未确认")
			}
			registrationCommittedFailure(c, err)
			return
		}
	}
	log.ZInfo(c, "registerUser api", "userID", resp.UserID)
	apiresp.GinSuccess(c, &resp)
}

func (o *ChatApi) Login(c *gin.Context) {
	var (
		req  chat.LoginReq
		resp apistruct.LoginResp
	)
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}

	log.ZInfo(c, "Chat API received login request",
		"platform", req.Platform,
		"deviceID", req.DeviceID,
		"deviceModel", req.DeviceModel)

	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}

	// 请求体里的 ip 只有可信上游（路由服务）代报时才认，客户端自己填的一律忽略
	req.Ip = resolveClientIP(c, req.Ip)

	log.ZInfo(c, "Chat API calling RPC",
		"deviceID", req.DeviceID,
		"deviceModel", req.DeviceModel)

	resp1, err := o.chatClient.Login(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	imToken, err := o.imApiCaller.UserToken(c, resp1.UserID, req.Platform)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	resp.ImToken = imToken
	resp.UserID = resp1.UserID
	resp.ChatToken = resp1.ChatToken
	apiresp.GinSuccess(c, resp)
}

func (o *ChatApi) ResetPassword(c *gin.Context) {
	a2r.Call(chat.ChatClient.ResetPassword, o.chatClient, c)
}

func (o *ChatApi) ChangePassword(c *gin.Context) {
	a2r.Call(chat.ChatClient.ChangePassword, o.chatClient, c)
}

// ################## USER ##################

func (o *ChatApi) UpdateUserInfo(c *gin.Context) {
	var (
		req  chat.UpdateUserInfoReq
		resp apistruct.UpdateUserInfoResp
	)
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	log.ZInfo(c, "updateUserInfo", "userID", req.UserID)
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	respUpdate, err := o.chatClient.UpdateUserInfo(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	resp.IMSyncPending = respUpdate.SyncPending
	apiresp.GinSuccess(c, resp)
}

func (o *ChatApi) FindUserPublicInfo(c *gin.Context) {
	a2r.Call(chat.ChatClient.FindUserPublicInfo, o.chatClient, c)
}

func (o *ChatApi) FindUserFullInfo(c *gin.Context) {
	a2r.Call(chat.ChatClient.FindUserFullInfo, o.chatClient, c)
}

//func (o *ChatApi) GetUsersFullInfo(c *gin.Context) {
//	a2r.Call(chat.ChatClient.GetUsersFullInfo, o.chatClient, c)
//}

func (o *ChatApi) SearchUserFullInfo(c *gin.Context) {
	a2r.Call(chat.ChatClient.SearchUserFullInfo, o.chatClient, c)
}

func (o *ChatApi) SearchUserPublicInfo(c *gin.Context) {
	a2r.Call(chat.ChatClient.SearchUserPublicInfo, o.chatClient, c)
}

// ################## APPLET ##################

func (o *ChatApi) FindApplet(c *gin.Context) {
	a2r.Call(admin.AdminClient.FindApplet, o.adminClient, c)
}

// ################## CONFIG ##################

func (o *ChatApi) GetClientConfig(c *gin.Context) {
	a2r.Call(admin.AdminClient.GetClientConfig, o.adminClient, c)
}

// ################## CALLBACK ##################

func (o *ChatApi) OpenIMCallback(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	command := c.Param("command")
	if len(command) > 0 && command[0] == '/' {
		command = command[1:]
	}
	if command == "" {
		command = c.Query(constant.CallbackCommand)
	}

	// Fallback: extract from body
	if command == "" {
		var bodyMap map[string]interface{}
		if err := json.Unmarshal(body, &bodyMap); err == nil {
			if cmd, ok := bodyMap["callbackCommand"].(string); ok {
				command = cmd
			}
		}
	}
	req := &chat.OpenIMCallbackReq{
		Command: command,
		Body:    string(body),
	}
	if _, err := o.chatClient.OpenIMCallback(c, req); err != nil {
		log.ZError(c, "OpenIMCallback 拒绝", err, "command", command)
		// 必须返回 actionCode != 0，OpenIM Server 才会识别为拒绝
		c.JSON(200, gin.H{
			"actionCode": 1,
			"errCode":    20013,
			"errMsg":     err.Error(),
		})
		return
	}
	apiresp.GinSuccess(c, nil)
}

func (o *ChatApi) getClientIP(c *gin.Context) (string, error) {
	return resolveClientIP(c, ""), nil
}

// splitComma 按逗号分割字符串
func splitComma(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

// trimSpace 去除字符串首尾空格
func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func (o *ChatApi) UploadLogs(c *gin.Context) {
	a2r.Call(chat.ChatClient.UploadLogs, o.chatClient, c)
}

func (o *ChatApi) SearchFriend(c *gin.Context) {
	var req struct {
		UserID string `json:"userID"`
		chat.SearchUserInfoReq
	}
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	opUserID, userType, err := ginOpUser(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	// 普通用户只能搜自己的好友；指定别人的 userID 仅限管理员，否则可遍历任意用户的好友关系
	if req.UserID == "" || userType != constant2.AdminUser {
		req.UserID = opUserID
	}
	imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	userIDs, err := o.imApiCaller.FriendUserIDs(mctx.WithApiToken(c, imToken), req.UserID)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if len(userIDs) == 0 {
		apiresp.GinSuccess(c, &chat.SearchUserInfoResp{})
		return
	}
	req.SearchUserInfoReq.UserIDs = userIDs
	resp, err := o.chatClient.SearchUserInfo(c, &req.SearchUserInfoReq)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, resp)
}

func (o *ChatApi) DeleteLogs(c *gin.Context) {
	a2r.Call(chat.ChatClient.DeleteLogs, o.chatClient, c)
}

// ################## LOGIN RECORD ##################

// 获取用户最新登录记录
func (o *ChatApi) GetUserLatestLoginRecord(c *gin.Context) {
	a2r.Call(chat.ChatClient.GetUserLatestLoginRecord, o.chatClient, c)
}

// 获取用户登录记录列表
func (o *ChatApi) GetUserLoginRecords(c *gin.Context) {
	a2r.Call(chat.ChatClient.GetUserLoginRecords, o.chatClient, c)
}

// ################## GOOGLE AUTH ##################

// GetGoogleAuthQRCode 获取谷歌验证码二维码
func (o *ChatApi) GetGoogleAuthQRCode(c *gin.Context) {
	a2r.Call(chat.ChatClient.GetGoogleAuthQRCode, o.chatClient, c)
}

// BindGoogleAuth 绑定谷歌验证码
func (o *ChatApi) BindGoogleAuth(c *gin.Context) {
	a2r.Call(chat.ChatClient.BindGoogleAuth, o.chatClient, c)
}

// ResetGoogleAuth 重置谷歌验证码
func (o *ChatApi) ResetGoogleAuth(c *gin.Context) {
	a2r.Call(chat.ChatClient.ResetGoogleAuth, o.chatClient, c)
}

// GetUserGoogleAuthStatus 获取用户谷歌验证码状态
func (o *ChatApi) GetUserGoogleAuthStatus(c *gin.Context) {
	a2r.Call(chat.ChatClient.GetUserGoogleAuthStatus, o.chatClient, c)
}
