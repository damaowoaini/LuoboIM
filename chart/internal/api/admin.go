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
	"context"
	"crypto/md5"
	"encoding/hex"
	"github.com/OpenIMSDK/chat/pkg/common/admindelegation"
	"github.com/OpenIMSDK/chat/pkg/common/adminprovision"
	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/apistruct"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	constant2 "github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/common/xlsx"
	"github.com/OpenIMSDK/chat/pkg/common/xlsx/model"
	"github.com/OpenIMSDK/chat/pkg/proto/admin"
	"github.com/OpenIMSDK/chat/pkg/proto/chat"
	"github.com/OpenIMSDK/chat/pkg/proto/rtc"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/protocol/user"
	"github.com/OpenIMSDK/tools/a2r"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/checker"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func NewAdmin(chatConn, adminConn, rtcConn grpc.ClientConnInterface) *AdminApi {
	return &AdminApi{chatClient: chat.NewChatClient(chatConn), adminClient: admin.NewAdminClient(adminConn),
		imApiCaller: apicall.NewCallerInterface(), rtcClient: rtc.NewRtcServiceClient(rtcConn)}
}

type AdminApi struct {
	chatClient      chat.ChatClient
	adminClient     admin.AdminClient
	rtcClient       rtc.RtcServiceClient
	imApiCaller     apicall.CallerInterface
	credentialAudit credentialIssuanceAudit
	delegation      *admindelegation.Service
	provisioning    *adminprovision.Service
}

func (o *AdminApi) AdminLogin(c *gin.Context) {
	var (
		req  admin.LoginReq
		resp apistruct.AdminLoginResp
	)
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	log.ZInfo(c, "AdminLogin api", "account", req.Account)
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	loginResp, err := o.adminClient.Login(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	// The RPC deliberately returns no token until 2FA enrollment is complete.
	// Do not mint a privileged IM token during that unauthenticated phase.
	if loginResp.AdminToken == "" {
		if err := utils.CopyStructFields(&resp, loginResp); err != nil {
			apiresp.GinError(c, err)
			return
		}
		apiresp.GinSuccess(c, resp)
		return
	}
	// Login RPC emits a token only after successful password/2FA authentication.
	imAdminUserID := config.GetIMAdmin(loginResp.AdminUserID)
	if imAdminUserID == "" {
		imAdminUserID = config.GetDefaultIMAdmin()
	}
	if o.delegation == nil {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("管理能力签发未配置"))
		return
	}
	issueCtx, cancelIssue := context.WithTimeout(c.Request.Context(), 2*time.Second)
	imToken, err := o.delegation.Issue(issueCtx, loginResp.AdminUserID, loginResp.AdminToken, imAdminUserID)
	cancelIssue()
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	err = utils.CopyStructFields(&resp, loginResp)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	resp.ImToken = imToken
	resp.ImUserID = imAdminUserID
	apiresp.GinSuccess(c, resp)
}

func (o *AdminApi) ResetUserPassword(c *gin.Context) {
	a2r.Call(chat.ChatClient.ChangePassword, o.chatClient, c)
}

func (o *AdminApi) GetIMUserToken(c *gin.Context) {
	if os.Getenv("LUOBO_ENABLE_LEGACY_USER_TOKEN_ISSUANCE") != "true" {
		apiresp.GinError(c, errs.ErrNoPermission.Wrap("完整用户凭证签发已关闭，请使用admin/message_preview只读接口"))
		return
	}
	if err := o.previewPermission(c, true); err != nil {
		apiresp.GinError(c, err)
		return
	}
	var req struct {
		UserID     string `json:"userID"`
		PlatformID int32  `json:"platformID"`
	}
	if c.ShouldBindJSON(&req) != nil || req.UserID == "" || len(req.UserID) > 64 {
		apiresp.GinError(c, errs.ErrArgs.Wrap("userID无效"))
		return
	}
	if req.PlatformID == 0 {
		req.PlatformID = constant.AdminPlatformID
	}
	if req.PlatformID != constant.AdminPlatformID {
		apiresp.GinError(c, errs.ErrNoPermission.Wrap("兼容接口仅允许后台预览平台"))
		return
	}
	if o.credentialAudit == nil {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("兼容凭证审计不可用"))
		return
	}
	if o.provisioning != nil {
		if err := o.provisioning.RequireReady(c, req.UserID); err != nil {
			apiresp.GinError(c, err)
			return
		}
	}
	audit, err := o.credentialAudit.Begin(c, mctx.GetOpUserID(c), req.UserID, req.PlatformID)
	if err != nil {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("兼容凭证审计写入失败"))
		return
	}
	token, err := o.imApiCaller.UserToken(c, req.UserID, req.PlatformID)
	if err != nil {
		o.credentialAudit.Finish(c, audit, "failed")
		apiresp.GinError(c, err)
		return
	}
	if o.credentialAudit.Finish(c, audit, "issued") != nil {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("兼容凭证审计收尾失败"))
		return
	}
	apiresp.GinSuccess(c, gin.H{"token": token})
}

func (o *AdminApi) AdminUpdateInfo(c *gin.Context) {
	var req admin.AdminUpdateInfoReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	resp, err := o.adminClient.AdminUpdateInfo(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, nil)
	imAdminUserID := config.GetIMAdmin(resp.UserID)
	// Dynamic administrators have their own chart profile. Never overwrite the
	// shared IM service identity with a dynamic admin's nickname or avatar.
	if imAdminUserID == "" {
		return
	}
	imToken, err := o.imApiCaller.UserToken(c, imAdminUserID, constant.AdminPlatformID)
	if err != nil {
		log.ZError(c, "AdminUpdateInfo ImAdminTokenWithDefaultAdmin", err)
		return
	}
	if err := o.imApiCaller.UpdateUserInfo(mctx.WithApiToken(c, imToken), imAdminUserID, resp.Nickname, resp.FaceURL); err != nil {
		log.ZError(c, "AdminUpdateInfo UpdateUserInfo", err, "userID", resp.UserID, "nickName", resp.Nickname, "faceURL", resp.FaceURL)
	}
}

func (o *AdminApi) AdminInfo(c *gin.Context) {
	a2r.Call(admin.AdminClient.GetAdminInfo, o.adminClient, c)
}

func (o *AdminApi) ChangeAdminPassword(c *gin.Context) {
	a2r.Call(admin.AdminClient.ChangeAdminPassword, o.adminClient, c)
}

func (o *AdminApi) AddAdminAccount(c *gin.Context) {
	a2r.Call(admin.AdminClient.AddAdminAccount, o.adminClient, c)
}

func (o *AdminApi) AddUserAccount(c *gin.Context) {
	a2r.Call(chat.ChatClient.AddUserAccount, o.chatClient, c)
}

func (o *AdminApi) DelAdminAccount(c *gin.Context) {
	a2r.Call(admin.AdminClient.DelAdminAccount, o.adminClient, c)
}

func (o *AdminApi) SearchAdminAccount(c *gin.Context) {
	a2r.Call(admin.AdminClient.SearchAdminAccount, o.adminClient, c)
}

func (o *AdminApi) AddDefaultFriend(c *gin.Context) {
	a2r.Call(admin.AdminClient.AddDefaultFriend, o.adminClient, c)
}

func (o *AdminApi) DelDefaultFriend(c *gin.Context) {
	a2r.Call(admin.AdminClient.DelDefaultFriend, o.adminClient, c)
}

func (o *AdminApi) SearchDefaultFriend(c *gin.Context) {
	a2r.Call(admin.AdminClient.SearchDefaultFriend, o.adminClient, c)
}

func (o *AdminApi) FindDefaultFriend(c *gin.Context) {
	a2r.Call(admin.AdminClient.FindDefaultFriend, o.adminClient, c)
}

func (o *AdminApi) AddDefaultGroup(c *gin.Context) {
	var req admin.AddDefaultGroupReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	imToken, err := currentAdminIMToken(c, o.imApiCaller)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	groups, err := o.imApiCaller.FindGroupInfo(mctx.WithApiToken(c, imToken), req.GroupIDs)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if len(req.GroupIDs) != len(groups) {
		apiresp.GinError(c, errs.ErrArgs.Wrap("group id not found"))
		return
	}
	resp, err := o.adminClient.AddDefaultGroup(c, &admin.AddDefaultGroupReq{
		GroupIDs: req.GroupIDs,
	})
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, resp)
}

func (o *AdminApi) DelDefaultGroup(c *gin.Context) {
	a2r.Call(admin.AdminClient.DelDefaultGroup, o.adminClient, c)
}

func (o *AdminApi) FindDefaultGroup(c *gin.Context) {
	a2r.Call(admin.AdminClient.FindDefaultGroup, o.adminClient, c)
}

func (o *AdminApi) SearchDefaultGroup(c *gin.Context) {
	var req admin.SearchDefaultGroupReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	searchResp, err := o.adminClient.SearchDefaultGroup(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	resp := apistruct.SearchDefaultGroupResp{
		Total:  searchResp.Total,
		Groups: make([]*sdkws.GroupInfo, 0, len(searchResp.GroupIDs)),
	}
	if len(searchResp.GroupIDs) > 0 {
		imToken, err := currentAdminIMToken(c, o.imApiCaller)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		groups, err := o.imApiCaller.FindGroupInfo(mctx.WithApiToken(c, imToken), searchResp.GroupIDs)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		groupMap := make(map[string]*sdkws.GroupInfo)
		for _, group := range groups {
			groupMap[group.GroupID] = group
		}
		for _, groupID := range searchResp.GroupIDs {
			if group, ok := groupMap[groupID]; ok {
				resp.Groups = append(resp.Groups, group)
			} else {
				resp.Groups = append(resp.Groups, &sdkws.GroupInfo{
					GroupID: groupID,
				})
			}
		}
	}
	apiresp.GinSuccess(c, resp)
}

func (o *AdminApi) AddInvitationCode(c *gin.Context) {
	a2r.Call(admin.AdminClient.AddInvitationCode, o.adminClient, c)
}

func (o *AdminApi) GenInvitationCode(c *gin.Context) {
	a2r.Call(admin.AdminClient.GenInvitationCode, o.adminClient, c)
}

func (o *AdminApi) DelInvitationCode(c *gin.Context) {
	a2r.Call(admin.AdminClient.DelInvitationCode, o.adminClient, c)
}

func (o *AdminApi) SearchInvitationCode(c *gin.Context) {
	a2r.Call(admin.AdminClient.SearchInvitationCode, o.adminClient, c)
}

func (o *AdminApi) AddUserIPLimitLogin(c *gin.Context) {
	a2r.Call(admin.AdminClient.AddUserIPLimitLogin, o.adminClient, c)
}

func (o *AdminApi) SearchUserIPLimitLogin(c *gin.Context) {
	a2r.Call(admin.AdminClient.SearchUserIPLimitLogin, o.adminClient, c)
}

func (o *AdminApi) DelUserIPLimitLogin(c *gin.Context) {
	a2r.Call(admin.AdminClient.DelUserIPLimitLogin, o.adminClient, c)
}

func (o *AdminApi) SearchIPForbidden(c *gin.Context) {
	a2r.Call(admin.AdminClient.SearchIPForbidden, o.adminClient, c)
}

func (o *AdminApi) AddIPForbidden(c *gin.Context) {
	a2r.Call(admin.AdminClient.AddIPForbidden, o.adminClient, c)
}

func (o *AdminApi) DelIPForbidden(c *gin.Context) {
	a2r.Call(admin.AdminClient.DelIPForbidden, o.adminClient, c)
}

func (o *AdminApi) ParseToken(c *gin.Context) {
	a2r.Call(admin.AdminClient.ParseToken, o.adminClient, c)
}

func (o *AdminApi) BlockUser(c *gin.Context) {
	var req admin.BlockUserReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	log.ZInfo(c, "BlockUser api", "req", &req)
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	resp, err := o.adminClient.BlockUser(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	imToken, err := currentAdminIMToken(c, o.imApiCaller)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	err = o.imApiCaller.ForceOffLine(mctx.WithApiToken(c, imToken), req.UserID)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, resp)
}

func (o *AdminApi) UnblockUser(c *gin.Context) {
	a2r.Call(admin.AdminClient.UnblockUser, o.adminClient, c)
}

func (o *AdminApi) SearchBlockUser(c *gin.Context) {
	a2r.Call(admin.AdminClient.SearchBlockUser, o.adminClient, c)
}

func (o *AdminApi) SetClientConfig(c *gin.Context) {
	a2r.Call(admin.AdminClient.SetClientConfig, o.adminClient, c)
}

func (o *AdminApi) DelClientConfig(c *gin.Context) {
	a2r.Call(admin.AdminClient.DelClientConfig, o.adminClient, c)
}

func (o *AdminApi) GetClientConfig(c *gin.Context) {
	a2r.Call(admin.AdminClient.GetClientConfig, o.adminClient, c)
}

func (o *AdminApi) AddApplet(c *gin.Context) {
	a2r.Call(admin.AdminClient.AddApplet, o.adminClient, c)
}

func (o *AdminApi) DelApplet(c *gin.Context) {
	a2r.Call(admin.AdminClient.DelApplet, o.adminClient, c)
}

func (o *AdminApi) UpdateApplet(c *gin.Context) {
	a2r.Call(admin.AdminClient.UpdateApplet, o.adminClient, c)
}

func (o *AdminApi) SearchApplet(c *gin.Context) {
	a2r.Call(admin.AdminClient.SearchApplet, o.adminClient, c)
}

func (o *AdminApi) LoginUserCount(c *gin.Context) {
	a2r.Call(chat.ChatClient.UserLoginCount, o.chatClient, c)
}

func (o *AdminApi) NewUserCount(c *gin.Context) {
	var req user.UserRegisterCountReq
	var resp apistruct.NewUserCountResp
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	log.ZInfo(c, "NewUserCount api", "req", &req)
	if err := checker.Validate(&req); err != nil {
		apiresp.GinError(c, err) // 参数校验失败
		return
	}
	imToken, err := currentAdminIMToken(c, o.imApiCaller)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	dateCount, total, err := o.imApiCaller.UserRegisterCount(mctx.WithApiToken(c, imToken), req.Start, req.End)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	resp.DateCount = dateCount
	resp.Total = total
	apiresp.GinSuccess(c, resp)
}

func (o *AdminApi) SearchLogs(c *gin.Context) {
	a2r.Call(chat.ChatClient.SearchLogs, o.chatClient, c)
}

func (o *AdminApi) DeleteLogs(c *gin.Context) {
	a2r.Call(chat.ChatClient.DeleteLogs, o.chatClient, c)
}

func (o *AdminApi) getClientIP(c *gin.Context) (string, error) {
	return resolveClientIP(c, ""), nil
}

func (o *AdminApi) checkSecretAdmin(c *gin.Context, secret string) error {
	if _, ok := c.Get(constant2.RpcOpUserID); ok {
		if o.delegation == nil {
			return errs.ErrNoPermission.Wrap("管理员会话不可用")
		}
		actor, _, err := o.delegation.AuthorizeSession(c, c.GetHeader("token"))
		expected, authErr := mctx.CheckAdmin(c)
		if err != nil || authErr != nil || actor != expected {
			return errs.ErrNoPermission.Wrap("管理员会话已失效")
		}
		return nil
	}
	if config.Config.ChatSecret == "" {
		return errs.ErrNoPermission.Wrap("未配置聊天密钥")
	}
	if config.Config.ChatSecret != secret {
		return errs.ErrNoPermission.Wrap("密钥错误")
	}
	SetToken(c, config.GetDefaultIMAdmin(), constant2.AdminUser)
	return nil
}

func (o *AdminApi) ImportUserByXlsx(c *gin.Context) {
	defer log.ZDebug(c, "ImportUserByXlsx return")
	formFile, err := c.FormFile("data")
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	ip, err := o.getClientIP(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	secret := c.PostForm("secret")
	if err := o.checkSecretAdmin(c, secret); err != nil {
		apiresp.GinError(c, err)
		return
	}
	file, err := formFile.Open()
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	defer file.Close()
	var users []model.User
	if err := xlsx.ParseAll(file, &users); err != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("xlsx file parse error "+err.Error()))
		return
	}
	us, err := o.xlsx2user(users)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}

	ctx := mctx.WithApiToken(c, imToken)
	result, err := o.registerChatUser(ctx, ip, us, c.PostForm("adminRequestID"))
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, result)
}

func (o *AdminApi) ImportUserByJson(c *gin.Context) {
	var req struct {
		Secret         string                   `json:"secret"`
		Users          []*chat.RegisterUserInfo `json:"users"`
		AdminRequestID string                   `json:"adminRequestID"`
	}
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	ip, err := o.getClientIP(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := o.checkSecretAdmin(c, req.Secret); err != nil {
		apiresp.GinError(c, err)
		return
	}
	imToken, err := o.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	ctx := mctx.WithApiToken(c, imToken)
	result, err := o.registerChatUser(ctx, ip, req.Users, req.AdminRequestID)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, result)
}

func (o *AdminApi) xlsx2user(users []model.User) ([]*chat.RegisterUserInfo, error) {
	chatUsers := make([]*chat.RegisterUserInfo, len(users))
	for i, info := range users {
		if info.Nickname == "" {
			return nil, errs.ErrArgs.Wrap("昵称不能为空")
		}
		if info.AreaCode == "" || info.PhoneNumber == "" {
			return nil, errs.ErrArgs.Wrap("区号或手机号不能为空")
		}
		if info.Password == "" {
			return nil, errs.ErrArgs.Wrap("密码不能为空")
		}
		if !strings.HasPrefix(info.AreaCode, "+") {
			return nil, errs.ErrArgs.Wrap("区号格式错误")
		}
		if _, err := strconv.ParseUint(info.AreaCode[1:], 10, 16); err != nil {
			return nil, errs.ErrArgs.Wrap("区号格式错误")
		}
		gender, _ := strconv.Atoi(info.Gender)
		chatUsers[i] = &chat.RegisterUserInfo{
			UserID:      info.UserID,
			Nickname:    info.Nickname,
			FaceURL:     info.FaceURL,
			Birth:       o.xlsxBirth(info.Birth).UnixMilli(),
			Gender:      int32(gender),
			AreaCode:    info.AreaCode,
			PhoneNumber: info.PhoneNumber,
			Email:       info.Email,
			Account:     info.Account,
			Password:    utils.Md5(info.Password),
		}
	}
	return chatUsers, nil
}

func (o *AdminApi) xlsxBirth(s string) time.Time {
	if s == "" {
		return time.Now()
	}
	var separator byte
	for _, b := range []byte(s) {
		if b < '0' || b > '9' {
			separator = b
		}
	}
	arr := strings.Split(s, string([]byte{separator}))
	if len(arr) != 3 {
		return time.Now()
	}
	year, _ := strconv.Atoi(arr[0])
	month, _ := strconv.Atoi(arr[1])
	day, _ := strconv.Atoi(arr[2])
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.Local)
	if t.Before(time.Date(1900, 0, 0, 0, 0, 0, 0, time.Local)) {
		return time.Now()
	}
	return t
}

type adminImportResult struct {
	AdminRequestID      string                   `json:"adminRequestID"`
	Results             []*chat.RegisterUserResp `json:"results"`
	Complete            bool                     `json:"complete"`
	ProvisioningPending bool                     `json:"provisioningPending"`
	FailedIndex         *int                     `json:"failedIndex,omitempty"`
	FailureState        string                   `json:"failureState,omitempty"`
}

func (o *AdminApi) registerChatUser(ctx context.Context, ip string, users []*chat.RegisterUserInfo, batchID string) (*adminImportResult, error) {
	if len(users) == 0 || len(users) > 1000 {
		return nil, errs.ErrArgs.Wrap("批量开户数量须为1至1000")
	}
	if batchID == "" {
		batchID = uuid.NewString()
	}
	if !adminprovision.ValidID(batchID) {
		return nil, errs.ErrArgs.Wrap("批量开户意图标识无效")
	}
	report := &adminImportResult{AdminRequestID: batchID, Results: []*chat.RegisterUserResp{}, Complete: true}
	for index, info := range users {
		requestID, _ := adminprovision.BatchRequestID(batchID, index)
		resp, err := o.chatClient.RegisterUser(ctx, &chat.RegisterUserReq{Ip: ip, User: info, Platform: constant.AdminPlatformID, AdminDefaultsAll: true, AdminRequestID: requestID})
		if err != nil || resp == nil || resp.ProvisioningReceiptID == "" {
			// Earlier rows may already be committed. A partial/unknown batch is a
			// reviewable result, never a blanket failure encouraging a new import ID.
			report.Complete = false
			report.ProvisioningPending = true
			report.FailedIndex = &index
			report.FailureState = "unconfirmed"
			report.Results = append(report.Results, &chat.RegisterUserResp{AdminRequestID: requestID, ProvisioningState: "unconfirmed", ProvisioningPending: true})
			return report, nil
		}
		report.Results = append(report.Results, resp)
		if resp.ProvisioningPending || resp.ProvisioningState == "conflict" {
			report.Complete = false
			report.ProvisioningPending = true
		}
	}
	return report, nil
}

func (o *AdminApi) BatchImportTemplate(c *gin.Context) {
	md5Sum := md5.Sum(config.ImportTemplate)
	md5Val := hex.EncodeToString(md5Sum[:])
	if c.GetHeader("If-None-Match") == md5Val {
		c.Status(http.StatusNotModified)
		return
	}
	c.Header("Content-Disposition", "attachment; filename=template.xlsx")
	c.Header("Content-Transfer-Encoding", "binary")
	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Length", strconv.Itoa(len(config.ImportTemplate)))
	c.Header("ETag", md5Val)
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", config.ImportTemplate)
}

// GetAdminGoogleAuthQRCode 获取管理员谷歌验证码二维码
func (o *AdminApi) GetAdminGoogleAuthQRCode(c *gin.Context) {
	a2r.Call(admin.AdminClient.GetAdminGoogleAuthQRCode, o.adminClient, c)
}

// BindAdminGoogleAuth 绑定管理员谷歌验证码
func (o *AdminApi) BindAdminGoogleAuth(c *gin.Context) {
	var req admin.BindAdminGoogleAuthReq
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, err)
		return
	}
	resp, err := o.adminClient.BindAdminGoogleAuth(c, &req)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, resp)
}

// ResetAdminGoogleAuth 重置管理员谷歌验证码
func (o *AdminApi) ResetAdminGoogleAuth(c *gin.Context) {
	a2r.Call(admin.AdminClient.ResetAdminGoogleAuth, o.adminClient, c)
}

// GetAdminGoogleAuthStatus 获取管理员谷歌验证码状态
func (o *AdminApi) GetAdminGoogleAuthStatus(c *gin.Context) {
	a2r.Call(admin.AdminClient.GetAdminGoogleAuthStatus, o.adminClient, c)
}
