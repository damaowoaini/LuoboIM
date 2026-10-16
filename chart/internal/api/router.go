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
	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	"github.com/OpenIMSDK/chat/pkg/common/admindelegation"
	"github.com/OpenIMSDK/chat/pkg/common/adminprovision"
	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/credentialmigration"
	"github.com/OpenIMSDK/chat/pkg/common/registration"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/db/cache"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	admintable "github.com/OpenIMSDK/chat/pkg/common/db/table/admin"
	"github.com/OpenIMSDK/chat/pkg/common/dbconn"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/gin-gonic/gin"
)

func NewChatRoute(router gin.IRouter, discov discoveryregistry.SvcDiscoveryRegistry) {
	mustInitIPResolver()
	chatConn, err := discov.GetConn(context.Background(), config.Config.RpcRegisterName.OpenImChatName)
	if err != nil {
		panic(err)
	}
	adminConn, err := discov.GetConn(context.Background(), config.Config.RpcRegisterName.OpenImAdminName)
	if err != nil {
		panic(err)
	}
	officeConn, err := discov.GetConn(context.Background(), config.Config.RpcRegisterName.OpenImOfficeName)
	if err != nil {
		panic(err)
	}
	mw := NewMW(adminConn)
	chat := NewChat(chatConn, adminConn)
	account := router.Group("/account")
	account.POST("/code/send", chat.SendVerifyCode)                      // Send verification code
	account.POST("/code/verify", chat.VerifyCode)                        // Verify the verification code
	account.POST("/login", chat.Login)                                   // Login
	account.POST("/password/reset", chat.ResetPassword)                  // Forgot password
	account.POST("/password/change", mw.CheckToken, chat.ChangePassword) // Change password
	db, err := dbconn.NewGormDB()
	if err != nil {
		panic(err)
	}
	if err := dbconn.AutoMigrate(context.Background(), db, &accountstate.Cancellation{}, &accountstate.CredentialRevocation{}, &accountstate.SessionRevocation{}, &registration.Receipt{}, &adminprovision.Intent{}, &adminprovision.Job{}, &adminprovision.RecoveryAudit{}); err != nil {
		panic(err)
	}
	rdb, err := cache.NewRedis()
	if err != nil {
		panic(err)
	}
	chat.registrations = &registrationAPI{db: db, caller: chat.imApiCaller}
	chat.adminSessions = &admindelegation.Service{DB: db, Redis: rdb, Secret: []byte(*config.Config.Secret), Issuer: config.Config.AdminDelegation.Issuer}
	account.POST("/logout", newSessionLogout(db, rdb).Logout)
	account.POST("/register", routeRegistrationAuthentication(rdb, false, "/account/register"), mw.CheckAdminOrNil, chat.RegisterUser)
	router.POST("/internal/registration/receipt", routeRegistrationAuthentication(rdb, true, "/internal/registration/receipt"), chat.registrations.Query)
	cancellation := &AccountCancellationApi{db: database.NewChatDatabase(db), service: &accountstate.Service{
		Ownership: func(ctx context.Context, id string) error { return adminprovision.RequireReady(ctx, db, id) }, Store: accountstate.NewSQLStore(db), Sessions: accountstate.NewRedisSessions(rdb, apicall.NewCallerInterface()),
	}}
	account.POST("/cancel", mw.CheckUser, cancellation.Cancel)

	user := router.Group("/user", mw.CheckToken)
	user.POST("/update", chat.UpdateUserInfo)                        // Edit personal information
	user.POST("/find/public", chat.FindUserPublicInfo)               // Get user's public information
	user.POST("/find/full", chat.FindUserFullInfo)                   // Get all information of the user
	user.POST("/search/full", chat.SearchUserFullInfo)               // Search user's public information
	user.POST("/search/public", chat.SearchUserPublicInfo)           // Search all information of the user
	user.POST("/login_record/latest", chat.GetUserLatestLoginRecord) // Get user's latest login record
	user.POST("/login_record/list", chat.GetUserLoginRecords)        // Get user's login records list

	googleAuth := router.Group("/google_auth", mw.CheckToken)
	googleAuth.POST("/qrcode", chat.GetGoogleAuthQRCode)     // 获取谷歌验证码二维码
	googleAuth.POST("/bind", chat.BindGoogleAuth)            // 绑定谷歌验证码
	googleAuth.POST("/reset", chat.ResetGoogleAuth)          // 重置谷歌验证码
	googleAuth.POST("/status", chat.GetUserGoogleAuthStatus) // 获取用户谷歌验证码状态

	router.POST("/friend/search", mw.CheckToken, chat.SearchFriend)

	router.Group("/applet").POST("/find", mw.CheckToken, chat.FindApplet) // Applet list

	router.Group("/client_config").POST("/get", chat.GetClientConfig) // Get client initialization configuration

	router.Group("/callback").POST("/open_im/*command", callbackAuthentication(rdb), chat.OpenIMCallback) // Callback

	logs := router.Group("/logs", mw.CheckToken)
	logs.POST("/upload", chat.UploadLogs)
	logs.POST("/delete", chat.DeleteLogs)

	office := NewOffice(officeConn)
	officeRouter := router.Group("/office", mw.CheckToken)
	officeRouter.POST("/tag/add", office.CreateTag)
	officeRouter.POST("/tag/del", office.DeleteTag)
	officeRouter.POST("/tag/set", office.SetTag)
	officeRouter.POST("/tag/get", office.GetUserTagByID)
	officeRouter.POST("/tag/find/user", office.GetUserTags)
	officeRouter.POST("/tag/send", office.SendMsg2Tag)
	officeRouter.POST("/tag/send/log", office.GetTagSendLogs)
	officeRouter.POST("/tag/send/log/del", office.DelTagSendLogs)

	officeRouter.POST("/work_moment/add", office.CreateOneWorkMoment)
	officeRouter.POST("/work_moment/del", office.DeleteOneWorkMoment)
	officeRouter.POST("/work_moment/like", office.LikeOneWorkMoment)
	officeRouter.POST("/work_moment/comment/add", office.CommentOneWorkMoment)
	officeRouter.POST("/work_moment/comment/del", office.DeleteComment)
	officeRouter.POST("/work_moment/get", office.GetWorkMomentByID)
	officeRouter.POST("/work_moment/find/send", office.GetUserSendWorkMoments)
	officeRouter.POST("/work_moment/find/recv", office.GetUserRecvWorkMoments)

	officeRouter.POST("/work_moment/logs", office.GetUnreadWorkMoments)
	officeRouter.POST("/work_moment/unread/count", office.GetUnreadWorkMomentsCount)
	officeRouter.POST("/work_moment/unread/clear", office.ReadWorkMoments)

	// 投诉管理
	cp, err := NewComplaintApi()
	if err != nil {
		panic(err)
	}
	complaintGroup := router.Group("/complaint", mw.CheckToken)
	complaintGroup.POST("/submit", cp.SubmitComplaint)
	complaintGroup.POST("/receipt", cp.Receipt)
	complaintGroup.POST("/search", cp.SearchComplaint)
	complaintGroup.POST("/handle", cp.HandleComplaint)
	complaintGroup.POST("/delete", cp.DeleteComplaint)
	complaintGroup.POST("/detail", cp.GetComplaintDetail)

	// 实名认证
	vf, err := NewUserVerificationApi()
	if err != nil {
		panic(err)
	}
	verificationGroup := router.Group("/verification", mw.CheckToken)
	verificationGroup.POST("/submit", vf.SubmitVerification)
	verificationGroup.POST("/status", vf.GetVerificationStatus)
}

func NewAdminRoute(router gin.IRouter, discov discoveryregistry.SvcDiscoveryRegistry) {
	mustInitIPResolver()
	adminConn, err := discov.GetConn(context.Background(), config.Config.RpcRegisterName.OpenImAdminName)
	if err != nil {
		panic(err)
	}
	chatConn, err := discov.GetConn(context.Background(), config.Config.RpcRegisterName.OpenImChatName)
	if err != nil {
		panic(err)
	}
	rtcConn, err := discov.GetConn(context.Background(), *config.Config.RpcRegisterName.OpenImRtcName)
	if err != nil {
		panic(err)
	}
	db, err := dbconn.NewGormDB()
	if err != nil {
		panic(err)
	}
	mw := NewMW(adminConn)
	admin := NewAdmin(chatConn, adminConn, rtcConn)
	if err := dbconn.AutoMigrate(context.Background(), db, &adminCredentialAudit{}, &admindelegation.Record{}, &admindelegation.RequestAudit{}, &adminMessageSearchAudit{}, &adminprovision.Intent{}, &adminprovision.Job{}, &adminprovision.RecoveryAudit{}); err != nil {
		panic(err)
	}
	admin.credentialAudit = &sqlCredentialAudit{db: db}
	preview := router.Group("/admin/message_preview", mw.CheckAdmin)
	preview.POST("/conversations", admin.adminPreview("conversations"))
	preview.POST("/search", admin.adminPreview("search"))
	device := NewDeviceApi(database.NewChatDatabase(db))
	rdb, rerr := cache.NewRedis()
	if rerr != nil {
		panic(rerr)
	}
	admin.delegation = &admindelegation.Service{DB: db, Redis: rdb, Secret: []byte(*config.Config.Secret), Issuer: config.Config.AdminDelegation.Issuer}
	admin.provisioning, err = newProvisioningView(db, admin.imApiCaller)
	if err != nil {
		panic(err)
	}
	router.POST("/internal/admin_delegation/validate", admin.validateDelegation(rdb))
	router.POST("/admin/message_search", mw.CheckAdmin, admin.adminMessageSearch)
	adminLoginLimit := adminAttemptLimitFromConfig(cache.NewLoginFailLock(rdb))
	adminRouterGroup := router.Group("/account")
	adminRouterGroup.POST("/logout", admin.AdminLogout)
	adminRouterGroup.POST("/login", adminLoginLimit, admin.AdminLogin)                  // Login
	adminRouterGroup.POST("/update", mw.CheckAdmin, admin.AdminUpdateInfo)              // Modify information
	adminRouterGroup.POST("/info", mw.CheckAdmin, admin.AdminInfo)                      // Get information
	adminRouterGroup.POST("/change_password", mw.CheckAdmin, admin.ChangeAdminPassword) // Change admin account's password
	adminRouterGroup.POST("/add_admin", mw.CheckAdmin, admin.AddAdminAccount)           // Add admin account
	adminRouterGroup.POST("/provisioning/status", mw.CheckAdmin, admin.AdminProvisioningStatus)
	adminRouterGroup.POST("/provisioning/resolve", mw.CheckAdmin, admin.ResolveAdminProvisioning)
	adminRouterGroup.POST("/provisioning/recover", mw.CheckAdmin, admin.RecoverAdminProvisioning)
	adminRouterGroup.POST("/add_user", mw.CheckAdmin, admin.AddUserAccount)   // Add user account
	adminRouterGroup.POST("/del_admin", mw.CheckAdmin, admin.DelAdminAccount) // Delete admin
	adminRouterGroup.POST("/search", mw.CheckAdmin, admin.SearchAdminAccount) // Get admin list
	if err := credentialmigration.MigrateSchema(context.Background(), db, "admins", &admintable.Admin{}, &admintable.AdminMetadataMigration{}); err != nil {
		panic(err)
	}
	adminAccountStatusApi := NewAdminAccountStatusApi(db, database.NewAdminDatabase(db, rdb))
	adminRouterGroup.POST("/list", mw.CheckAdmin, adminAccountStatusApi.List)
	adminRouterGroup.POST("/set_status", mw.CheckAdmin, adminAccountStatusApi.SetStatus)
	adminRouterGroup.POST("/reset_password", mw.CheckAdmin, adminAccountStatusApi.ResetPassword)

	// 管理员谷歌验证码
	googleAuthAdminGroup := router.Group("/google_auth_manager")
	googleAuthAdminGroup.POST("/qrcode", adminLoginLimit, admin.GetAdminGoogleAuthQRCode) // Get QR code (登录前不需要 token)
	googleAuthAdminGroup.POST("/bind", adminLoginLimit, admin.BindAdminGoogleAuth)        // Bind Google Auth (登录前不需要 token，通过账号密码验证)
	googleAuthAdminGroup.POST("/reset", mw.CheckAdmin, admin.ResetAdminGoogleAuth)        // Reset Google Auth (super admin only)
	googleAuthAdminGroup.POST("/status", adminLoginLimit, admin.GetAdminGoogleAuthStatus) // Get Google Auth status (登录前不需要 token)

	importGroup := router.Group("/user/import")
	importGroup.POST("/json", mw.CheckAdminOrNil, admin.ImportUserByJson)
	importGroup.POST("/xlsx", mw.CheckAdminOrNil, admin.ImportUserByXlsx)
	importGroup.GET("/xlsx", admin.BatchImportTemplate)

	defaultRouter := router.Group("/default", mw.CheckAdmin)
	defaultUserRouter := defaultRouter.Group("/user")
	defaultUserRouter.POST("/add", admin.AddDefaultFriend)       // Add default friend at registration
	defaultUserRouter.POST("/del", admin.DelDefaultFriend)       // Delete default friend at registration
	defaultUserRouter.POST("/find", admin.FindDefaultFriend)     // Default friend list
	defaultUserRouter.POST("/search", admin.SearchDefaultFriend) // Search default friend list at registration
	defaultGroupRouter := defaultRouter.Group("/group")
	defaultGroupRouter.POST("/add", admin.AddDefaultGroup)       // Add default group at registration
	defaultGroupRouter.POST("/del", admin.DelDefaultGroup)       // Delete default group at registration
	defaultGroupRouter.POST("/find", admin.FindDefaultGroup)     // Get default group list at registration
	defaultGroupRouter.POST("/search", admin.SearchDefaultGroup) // Search default group list at registration

	invitationCodeRouter := router.Group("/invitation_code", mw.CheckAdmin)
	invitationCodeRouter.POST("/add", admin.AddInvitationCode)       // Add invitation code
	invitationCodeRouter.POST("/gen", admin.GenInvitationCode)       // Generate invitation code
	invitationCodeRouter.POST("/del", admin.DelInvitationCode)       // Delete invitation code
	invitationCodeRouter.POST("/search", admin.SearchInvitationCode) // Search invitation code

	forbiddenRouter := router.Group("/forbidden", mw.CheckAdmin)
	ipForbiddenRouter := forbiddenRouter.Group("/ip")
	ipForbiddenRouter.POST("/add", admin.AddIPForbidden)       // Add forbidden IP for registration/login
	ipForbiddenRouter.POST("/del", admin.DelIPForbidden)       // Delete forbidden IP for registration/login
	ipForbiddenRouter.POST("/search", admin.SearchIPForbidden) // Search forbidden IPs for registration/login
	userForbiddenRouter := forbiddenRouter.Group("/user")
	userForbiddenRouter.POST("/add", admin.AddUserIPLimitLogin)       // Add limit for user login on specific IP
	userForbiddenRouter.POST("/del", admin.DelUserIPLimitLogin)       // Delete user limit on specific IP for login
	userForbiddenRouter.POST("/search", admin.SearchUserIPLimitLogin) // Search limit for user login on specific IP

	appletRouterGroup := router.Group("/applet", mw.CheckAdmin)
	appletRouterGroup.POST("/add", admin.AddApplet)       // Add applet
	appletRouterGroup.POST("/del", admin.DelApplet)       // Delete applet
	appletRouterGroup.POST("/update", admin.UpdateApplet) // Modify applet
	appletRouterGroup.POST("/search", admin.SearchApplet) // Search applet

	blockRouter := router.Group("/block", mw.CheckAdmin)
	blockRouter.POST("/add", admin.BlockUser)          // Block user
	blockRouter.POST("/del", admin.UnblockUser)        // Unblock user
	blockRouter.POST("/search", admin.SearchBlockUser) // Search blocked users

	userRouter := router.Group("/user", mw.CheckAdmin)
	userRouter.POST("/password/reset", admin.ResetUserPassword) // Reset user password
	userRouter.POST("/im_token", admin.GetIMUserToken)          // 服务端签发 IM token，secret 不进前端
	userRouter.POST("/device/unbind", device.UnbindUserDevice)  // 解绑用户设备（清除登录记录，下次登录重新绑定）

	initGroup := router.Group("/client_config", mw.CheckAdmin)
	initGroup.POST("/get", admin.GetClientConfig) // Get client initialization configuration
	initGroup.POST("/set", admin.SetClientConfig) // Set client initialization configuration
	initGroup.POST("/del", admin.DelClientConfig) // Delete client initialization configuration

	statistic := router.Group("/statistic", mw.CheckAdmin)
	statistic.POST("/new_user_count", admin.NewUserCount)
	statistic.POST("/login_user_count", admin.LoginUserCount)

	rtc := router.Group("/rtc", mw.CheckAdmin)
	rtc.POST("/get_signal_invitation_records", admin.GetSignalInvitationRecords)
	rtc.POST("/delete_signal_records", admin.DeleteSignalRecords)
	rtc.POST("/get_meeting_records", admin.GetMeetingRecords)
	rtc.POST("/delete_meeting_records", admin.DeleteMeetingRecords)

	logs := router.Group("/logs", mw.CheckAdmin)
	logs.POST("/search", admin.SearchLogs)
	logs.POST("/delete", admin.DeleteLogs)
}
