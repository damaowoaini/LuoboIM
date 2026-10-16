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

package chat

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/adminprovision"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/common/passwordstore"
	"github.com/OpenIMSDK/chat/pkg/common/registration"

	constant2 "github.com/OpenIMSDK/protocol/constant"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/OpenIMSDK/tools/utils"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/dbutil"
	chat2 "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/iputil"
	"github.com/OpenIMSDK/chat/pkg/common/totp"
	"github.com/OpenIMSDK/chat/pkg/eerrs"
	"github.com/OpenIMSDK/chat/pkg/proto/chat"
)

func (o *chatSvr) verifyCodeJoin(areaCode, phoneNumber string) string {
	return areaCode + " " + phoneNumber
}

// verifyCodeKey 验证码存储/校验用的标识（邮箱 / 区号+手机号）
func verifyCodeKey(email, areaCode, phoneNumber string) string {
	if email != "" {
		return email
	}
	if phoneNumber == "" {
		return ""
	}
	if areaCode != "" {
		return areaCode + " " + phoneNumber
	}
	return phoneNumber
}

func isClientConfigEnabled(val string) bool {
	return utils.Contain(strings.ToLower(val), "1", "true", "yes")
}

func isClientConfigDisabled(val string) bool {
	return utils.Contain(strings.ToLower(val), "0", "false", "no")
}

// needVerificationCodeRegister 未配置时默认需要验证码，显式关闭才跳过。
func needVerificationCodeRegister(conf map[string]string) bool {
	if conf == nil {
		return true
	}
	val, ok := conf[constant.NeedVerificationCodeRegisterConfigKey]
	if !ok || val == "" {
		return true
	}
	if isClientConfigDisabled(val) {
		return false
	}
	return true
}

func (o *chatSvr) SendVerifyCode(ctx context.Context, req *chat.SendVerifyCodeReq) (*chat.SendVerifyCodeResp, error) {
	defer log.ZDebug(ctx, "return")
	switch int(req.UsedFor) {
	case constant.VerificationCodeForRegister:
		if err := o.ensureLoginRegisterIPNotLimited(ctx, req.Ip); err != nil {
			return nil, err
		}
		if err := o.Admin.CheckRegister(ctx, req.Ip); err != nil {
			return nil, err
		}
		if req.Email == "" {
			if req.AreaCode == "" || req.PhoneNumber == "" {
				return nil, errs.ErrArgs.Wrap("区号或手机号不能为空")
			}
			if req.AreaCode[0] != '+' {
				req.AreaCode = "+" + req.AreaCode
			}
			if _, err := strconv.ParseUint(req.AreaCode[1:], 10, 64); err != nil {
				return nil, errs.ErrArgs.Wrap("区号必须是数字")
			}
			if err := chat.PhoneNumberCheck(req.PhoneNumber); err != nil {
				return nil, err
			}
			_, err := o.Database.TakeAttributeByPhone(ctx, req.AreaCode, req.PhoneNumber)
			if err == nil {
				return nil, eerrs.ErrPhoneAlreadyRegister.Wrap("手机号已注册")
			} else if !o.Database.IsNotFound(err) {
				return nil, err
			}
		} else {
			if err := chat.EmailCheck(req.Email); err != nil {
				return nil, errs.ErrArgs.Wrap("邮箱格式必须正确")
			}
			_, err := o.Database.TakeAttributeByEmail(ctx, req.Email)
			if err == nil {
				return nil, eerrs.ErrEmailAlreadyRegister.Wrap("邮箱已注册")
			} else if !o.Database.IsNotFound(err) {
				return nil, err
			}
		}
		conf, err := o.Admin.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		if val := conf[constant.NeedInvitationCodeRegisterConfigKey]; isClientConfigEnabled(val) {
			if req.InvitationCode == "" {
				return nil, errs.ErrArgs.Wrap("邀请码不能为空")
			}
			if err := o.Admin.CheckInvitationCode(ctx, req.InvitationCode); err != nil {
				return nil, err
			}
		}
		if !needVerificationCodeRegister(conf) {
			return &chat.SendVerifyCodeResp{}, nil
		}
	case constant.VerificationCodeForLogin, constant.VerificationCodeForResetPassword:
		if int(req.UsedFor) == constant.VerificationCodeForLogin {
			if err := o.ensureLoginRegisterIPNotLimited(ctx, req.Ip); err != nil {
				return nil, err
			}
		}
		if req.Email == "" {
			_, err := o.Database.TakeAttributeByPhone(ctx, req.AreaCode, req.PhoneNumber)
			if o.Database.IsNotFound(err) {
				if int(req.UsedFor) == constant.VerificationCodeForLogin {
					o.recordLoginRegisterIPFailure(ctx, req.Ip)
					log.ZInfo(ctx, "login send code: phone not registered, return fake success")
					return &chat.SendVerifyCodeResp{}, nil
				}
				return nil, eerrs.ErrAccountNotFound.Wrap("手机号未注册")
			} else if err != nil {
				return nil, err
			}
		} else {
			_, err := o.Database.TakeAttributeByEmail(ctx, req.Email)
			if o.Database.IsNotFound(err) {
				if int(req.UsedFor) == constant.VerificationCodeForLogin {
					o.recordLoginRegisterIPFailure(ctx, req.Ip)
					log.ZInfo(ctx, "login send code: email not registered, return fake success",
						"email", req.Email, "ip", req.Ip)
					return &chat.SendVerifyCodeResp{}, nil
				}
				return nil, eerrs.ErrAccountNotFound.Wrap("邮箱未注册")
			} else if err != nil {
				return nil, err
			}
		}

	default:
		return nil, errs.ErrArgs.Wrap("未知的使用场景")
	}
	var account string
	var isEmail bool
	if req.Email != "" {
		isEmail = true
		account = req.Email
	} else {
		account = verifyCodeKey("", req.AreaCode, req.PhoneNumber)
	}

	verifyCode := config.Config.VerifyCode
	if verifyCode.UintTime == 0 || verifyCode.MaxCount == 0 {
		return nil, errs.ErrNoPermission.Wrap("验证码功能已禁用")
	}
	if verifyCode.Len < 4 || verifyCode.Len > 16 || verifyCode.ValidTime <= 0 {
		return nil, errs.ErrInternalServer.Wrap("验证码长度或有效期配置无效")
	}
	if verifyCode.Use == "" {
		return nil, errs.ErrNoPermission.Wrap("验证码发送服务未配置")
	}
	now := time.Now()

	var count uint32
	var err error
	if !isEmail {
		count, err = o.Database.CountVerifyCodeRange(ctx, o.verifyCodeJoin(req.AreaCode, req.PhoneNumber), now.Add(-time.Duration(verifyCode.UintTime)*time.Second), now)
		if err != nil {
			return nil, err
		}
	} else {
		count, err = o.Database.CountVerifyCodeRange(ctx, req.Email, now.Add(-time.Duration(verifyCode.UintTime)*time.Second), now)
	}
	if err != nil {
		return nil, err
	}
	if int(count) >= verifyCode.MaxCount {
		return nil, eerrs.ErrVerifyCodeSendFrequently.Wrap()
	}

	needIPLimit := int(req.UsedFor) == constant.VerificationCodeForRegister ||
		int(req.UsedFor) == constant.VerificationCodeForLogin
	ipLimited := false
	if needIPLimit {
		ipLimited, err = o.checkAndIncrLoginRegisterIPLimit(ctx, req.Ip)
		if err != nil {
			return nil, err
		}
	}

	t := &chat2.VerifyCode{
		Account:    account,
		Code:       o.genVerifyCode(),
		Duration:   uint(config.Config.VerifyCode.ValidTime),
		CreateTime: time.Now(),
		UsedFor:    req.UsedFor,
	}
	if !isEmail {
		err = o.Database.AddVerifyCode(ctx, t, func() error {
			return o.SMS.SendCode(ctx, req.AreaCode, req.PhoneNumber, t.Code)
		})
	} else {
		// 发送邮件验证码
		err = o.Database.AddVerifyCode(ctx, t, func() error {
			return o.Mail.SendMail(ctx, req.Email, t.Code)
		})
	}
	if err != nil {
		if ipLimited {
			_ = o.LoginRegisterIPLimit.Decr(ctx, req.Ip)
		}
		return nil, err
	}
	return &chat.SendVerifyCodeResp{}, nil
}

// getLoginRegisterIPLimit 从后台 client_config 读取同一 IP 小时上限，0 表示不限制。
func (o *chatSvr) getLoginRegisterIPLimit(ctx context.Context) int64 {
	conf, err := o.Admin.GetConfig(ctx)
	if err != nil {
		log.ZError(ctx, "getLoginRegisterIPLimit: 获取后台配置失败，降级不限制", err)
		return 0
	}
	limitStr, ok := conf[constant.HourlyLoginRegisterIPLimitConfigKey]
	if !ok || limitStr == "" || limitStr == "0" {
		return 0
	}
	limit, err := strconv.ParseInt(limitStr, 10, 64)
	if err != nil || limit <= 0 {
		return 0
	}
	return limit
}

// ensureLoginRegisterIPNotLimited 若该 IP 已达后台配置上限则直接拒绝。
func (o *chatSvr) ensureLoginRegisterIPNotLimited(ctx context.Context, ip string) error {
	if ip == "" || o.LoginRegisterIPLimit == nil {
		return nil
	}
	limit := o.getLoginRegisterIPLimit(ctx)
	if limit <= 0 || ip == "" || o.LoginRegisterIPLimit == nil {
		return nil
	}
	count, err := o.LoginRegisterIPLimit.GetCount(ctx, ip)
	if err != nil {
		log.ZError(ctx, "ensureLoginRegisterIPNotLimited: Redis 读取失败，降级放行", err, "ip", ip)
		return nil
	}
	if count >= limit {
		return eerrs.ErrVerifyCodeIPLimit.Wrap(fmt.Sprintf("同一IP一小时内登录/注册操作次数已达上限（%d次/小时）", limit))
	}
	return nil
}

// checkAndIncrLoginRegisterIPLimit 发送验证码前占用配额；返回 occupied 表示需在失败时回滚。
func (o *chatSvr) checkAndIncrLoginRegisterIPLimit(ctx context.Context, ip string) (bool, error) {
	limit := o.getLoginRegisterIPLimit(ctx)
	if limit <= 0 || ip == "" || o.LoginRegisterIPLimit == nil {
		return false, nil
	}
	limited, err := o.LoginRegisterIPLimit.CheckAndIncr(ctx, ip, limit)
	if err != nil {
		log.ZError(ctx, "checkAndIncrLoginRegisterIPLimit: Redis 记录失败，降级放行", err, "ip", ip)
		return false, nil
	}
	if limited {
		return false, eerrs.ErrVerifyCodeIPLimit.Wrap(fmt.Sprintf("同一IP一小时内登录/注册操作次数已达上限（%d次/小时）", limit))
	}
	log.ZInfo(ctx, "login/register ip limit recorded", "ip", ip, "limit", limit)
	return true, nil
}

// recordLoginRegisterIPFailure 登录返回账号不存在/密码错误时计入 IP 小时限。
func (o *chatSvr) recordLoginRegisterIPFailure(ctx context.Context, ip string) {
	limit := o.getLoginRegisterIPLimit(ctx)
	if limit <= 0 || ip == "" || o.LoginRegisterIPLimit == nil {
		return
	}
	if err := o.LoginRegisterIPLimit.Incr(ctx, ip); err != nil {
		log.ZError(ctx, "recordLoginRegisterIPFailure: Redis 记录失败", err, "ip", ip)
	}
}

func (o *chatSvr) verifyCodeStrict(ctx context.Context, account string, verifyCode string, usedFor ...int32) (uint, error) {
	defer log.ZDebug(ctx, "return")
	if verifyCode == "" {
		return 0, errs.ErrArgs.Wrap("验证码不能为空")
	}
	// Public authentication always requires a code issued to this account.
	// Administrative registration already has a separate authorized path.
	if config.Config.VerifyCode.Use == "" {
		return 0, eerrs.ErrVerifyCodeNotMatch.Wrap("验证服务未启用")
	}

	// 正常验证码验证流程
	last, err := o.Database.TakeLastVerifyCode(ctx, account)
	if err != nil {
		if dbutil.IsGormNotFound(err) {
			return 0, eerrs.ErrVerifyCodeExpired.Wrap()
		}
		return 0, err
	}
	if len(usedFor) > 0 && last.UsedFor != usedFor[0] {
		return last.ID, eerrs.ErrVerifyCodeNotMatch.Wrap("验证码用途不匹配，请重新获取")
	}
	if last.CreateTime.Unix()+int64(last.Duration) < time.Now().Unix() {
		return last.ID, eerrs.ErrVerifyCodeExpired.Wrap()
	}
	if last.Used {
		return last.ID, eerrs.ErrVerifyCodeUsed.Wrap()
	}
	if config.Config.VerifyCode.MaxCount > 0 {
		if last.Count >= config.Config.VerifyCode.MaxCount {
			return last.ID, eerrs.ErrVerifyCodeMaxCount.Wrap()
		}
		if last.Code != verifyCode {
			if err := o.Database.UpdateVerifyCodeIncrCount(ctx, last.ID); err != nil {
				return last.ID, err
			}
		}
	}
	if last.Code != verifyCode {
		return last.ID, eerrs.ErrVerifyCodeNotMatch.Wrap()
	}
	return last.ID, nil
}

func (o *chatSvr) VerifyCode(ctx context.Context, req *chat.VerifyCodeReq) (*chat.VerifyCodeResp, error) {
	defer log.ZDebug(ctx, "return")
	account := verifyCodeKey(req.Email, req.AreaCode, req.PhoneNumber)
	if account == "" {
		return nil, errs.ErrArgs.Wrap("手机号或邮箱不能为空")
	}
	var purpose []int32
	if req.UsedFor != 0 {
		if req.UsedFor < constant.VerificationCodeForRegister || req.UsedFor > constant.VerificationCodeForLogin {
			return nil, errs.ErrArgs.Wrap("验证码用途无效")
		}
		purpose = []int32{req.UsedFor}
	}
	if _, err := o.verifyCodeStrict(ctx, account, req.VerifyCode, purpose...); err != nil {
		return nil, err
	}
	return &chat.VerifyCodeResp{}, nil
}

func (o *chatSvr) genUserID() string {
	const l = 10
	data := make([]byte, l)
	if _, err := cryptorand.Read(data); err != nil {
		panic("cannot generate user ID: " + err.Error())
	}
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

func (o *chatSvr) genVerifyCode() string {
	data := make([]byte, config.Config.VerifyCode.Len)
	for i := 0; i < len(data); i++ {
		n, err := cryptorand.Int(cryptorand.Reader, big.NewInt(10))
		if err != nil {
			panic("cannot generate verification code: " + err.Error())
		}
		data[i] = byte('0' + n.Int64())
	}
	return string(data)
}

func (o *chatSvr) registerUser(ctx context.Context, req *chat.RegisterUserReq) (*chat.RegisterUserResp, error) {
	resp := &chat.RegisterUserResp{}

	isAdmin, err := o.Admin.CheckNilOrAdmin(ctx)
	originalActor := mcontext.GetOpUserID(ctx)
	if isAdmin {
		originalActor, _ = mctx.CheckAdmin(ctx)
	}
	ctx = mctx.WithAdminUser(ctx)
	if err != nil {
		return nil, err
	}
	if req.User == nil {
		return nil, errs.ErrArgs.Wrap("用户信息不能为空")
	}
	log.ZDebug(ctx, "email", req.User.Email)
	if req.User.Email == "" {
		if (req.User.AreaCode == "" && req.User.PhoneNumber != "") || (req.User.AreaCode != "" && req.User.PhoneNumber == "") {
			return nil, errs.ErrArgs.Wrap("区号或手机号错误，且未提供邮箱")
		}
	}
	var usedInvitationCode bool
	var consumedVerifyCodeIDs []uint
	if !isAdmin {
		if req.User.UserID != "" {
			return nil, errs.ErrNoPermission.Wrap("只有管理员可以设置用户ID")
		}
		if err := o.Admin.CheckRegister(ctx, req.Ip); err != nil {
			return nil, err
		}
		conf, err := o.Admin.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		if val := conf[constant.NeedInvitationCodeRegisterConfigKey]; isClientConfigEnabled(val) {
			usedInvitationCode = true
			if req.InvitationCode == "" {
				return nil, errs.ErrArgs.Wrap("邀请码不能为空")
			}
			if err := o.Admin.CheckInvitationCode(ctx, req.InvitationCode); err != nil {
				return nil, err
			}
		}
		if needVerificationCodeRegister(conf) {
			key := verifyCodeKey(req.User.Email, req.User.AreaCode, req.User.PhoneNumber)
			if key == "" && req.User.Account != "" {
				key = req.User.Account
			}
			if id, err := o.verifyCodeStrict(ctx, key, req.VerifyCode, constant.VerificationCodeForRegister); err != nil {
				return nil, err
			} else {
				consumedVerifyCodeIDs = []uint{id}
			}
		}
	}
	log.ZDebug(ctx, "usedInvitationCode", usedInvitationCode)
	if req.User.UserID == "" {
		for i := 0; i < 20; i++ {
			userID := o.genUserID()
			_, err := o.Database.GetUser(ctx, userID)
			if err == nil {
				continue
			} else if dbutil.IsGormNotFound(err) {
				req.User.UserID = userID
				break
			} else {
				return nil, err
			}
		}
		if req.User.UserID == "" {
			return nil, errs.ErrInternalServer.Wrap("生成用户ID失败")
		}
	} else {
		_, err := o.Database.GetUser(ctx, req.User.UserID)
		if err == nil {
			return nil, errs.ErrArgs.Wrap("指定的用户ID已被注册")
		} else if !dbutil.IsGormNotFound(err) {
			return nil, err
		}
	}
	var registerType int32
	if req.User.PhoneNumber != "" {
		if req.User.AreaCode == "" {
			return nil, errs.ErrArgs.Wrap("手机号必须提供区号")
		}
		if req.User.AreaCode[0] != '+' {
			req.User.AreaCode = "+" + req.User.AreaCode
		}
		if _, err := strconv.ParseUint(req.User.AreaCode[1:], 10, 64); err != nil {
			return nil, errs.ErrArgs.Wrap("区号必须是数字")
		}
		if err := chat.PhoneNumberCheck(req.User.PhoneNumber); err != nil {
			return nil, err
		}
		_, err := o.Database.TakeAttributeByPhone(ctx, req.User.AreaCode, req.User.PhoneNumber)
		if err == nil {
			return nil, eerrs.ErrPhoneAlreadyRegister.Wrap()
		} else if !o.Database.IsNotFound(err) {
			return nil, err
		}
		registerType = constant.PhoneRegister
	}

	if req.User.Account != "" {
		_, err := o.Database.TakeAttributeByAccount(ctx, req.User.Account)
		if err == nil {
			return nil, eerrs.ErrAccountAlreadyRegister.Wrap()
		} else if !o.Database.IsNotFound(err) {
			return nil, err
		}
	}

	if req.User.Email != "" {
		_, err := o.Database.TakeAttributeByEmail(ctx, req.User.Email)
		registerType = constant.EmailRegister
		if err == nil {
			return nil, eerrs.ErrEmailAlreadyRegister.Wrap()
		} else if !o.Database.IsNotFound(err) {
			return nil, err
		}
	}
	register := &chat2.Register{
		UserID:      req.User.UserID,
		DeviceID:    req.DeviceID,
		IP:          req.Ip,
		Platform:    constant2.PlatformID2Name[int(req.Platform)],
		AccountType: "",
		Mode:        constant.UserMode,
		CreateTime:  time.Now(),
	}
	if isAdmin {
		register.Mode = constant.AdminMode
	} else {
		originalActor = req.User.UserID
	}
	account := &chat2.Account{
		UserID:         req.User.UserID,
		Password:       req.User.Password,
		OperatorUserID: originalActor,
		ChangeTime:     register.CreateTime,
		CreateTime:     register.CreateTime,
	}
	attribute := &chat2.Attribute{
		UserID:         req.User.UserID,
		Account:        req.User.Account,
		PhoneNumber:    req.User.PhoneNumber,
		AreaCode:       req.User.AreaCode,
		Email:          req.User.Email,
		Nickname:       req.User.Nickname,
		FaceURL:        req.User.FaceURL,
		Gender:         req.User.Gender,
		BirthTime:      time.UnixMilli(req.User.Birth),
		ChangeTime:     register.CreateTime,
		CreateTime:     register.CreateTime,
		AllowVibration: constant.DefaultAllowVibration,
		AllowBeep:      constant.DefaultAllowBeep,
		AllowAddFriend: constant.DefaultAllowAddFriend,
		RegisterType:   registerType,
	}
	if err := o.Database.RegisterUser(ctx, register, account, attribute, consumedVerifyCodeIDs...); err != nil {
		return nil, err
	}
	if creation := adminprovision.CreationFrom(ctx); creation != nil {
		resp.UserID = creation.UserID
		return resp, nil
	}
	if usedInvitationCode {
		if err := o.Admin.UseInvitationCode(ctx, req.User.UserID, req.InvitationCode); err != nil {
			log.ZError(ctx, "UseInvitationCode", err, "userID", req.User.UserID, "invitationCode", req.InvitationCode)
		}
	}
	if req.AutoLogin {
		if registration.ID(ctx) == "" && adminprovision.PublicCreationFrom(ctx) == nil {
			chatToken, adminErr := o.Admin.CreateToken(ctx, req.User.UserID, constant.NormalUser)
			if adminErr != nil {
				log.ZError(ctx, "Admin CreateToken Failed", adminErr, "userID", req.User.UserID, "platform", req.Platform)
			}
			if adminErr == nil {
				resp.ChatToken = chatToken.Token
			}

		}
		// 【新增】注册并自动登录时，记录登录信息到 user_login_records 表
		// 获取登录地区信息
		location := iputil.GetLocationByIPWithFallback(req.Ip)

		log.ZInfo(ctx, "Register with auto login - recording login info",
			"userID", req.User.UserID,
			"ip", req.Ip,
			"deviceID", req.DeviceID,
			"platform", req.Platform,
			"deviceModel", req.DeviceModel,
			"location", location)

		loginRecord := &chat2.UserLoginRecord{
			UserID:      req.User.UserID,
			LoginTime:   time.Now(),
			IP:          req.Ip,
			DeviceID:    req.DeviceID,
			Platform:    constant2.PlatformIDToName(int(req.Platform)),
			DeviceModel: req.DeviceModel, // 从请求中直接获取设备型号
			Location:    location,
		}

		// 保存登录记录（注册时不需要验证码ID）
		if err := o.Database.LoginRecord(ctx, loginRecord, nil); err != nil {
			log.ZError(ctx, "Failed to save login record after registration", err,
				"userID", loginRecord.UserID,
				"deviceModel", loginRecord.DeviceModel)
			// 不影响注册流程，只记录错误日志
		} else {
			log.ZInfo(ctx, "Login record saved successfully after registration",
				"userID", loginRecord.UserID,
				"deviceModel", loginRecord.DeviceModel)
		}
	}
	resp.UserID = req.User.UserID
	return resp, nil
}

func (o *chatSvr) Login(ctx context.Context, req *chat.LoginReq) (*chat.LoginResp, error) {
	defer log.ZDebug(ctx, "return")

	// 【调试日志】RPC 函数入口，打印接收到的原始请求
	log.ZInfo(ctx, "RPC Login function ENTRY - raw request received",
		"deviceID", req.DeviceID,
		"deviceModel", req.DeviceModel,
		"GetDeviceModel()", req.GetDeviceModel(),
		"platform", req.Platform)

	resp := &chat.LoginResp{}
	if req.Password == "" && req.VerifyCode == "" {
		return nil, errs.ErrArgs.Wrap("密码或验证码必须设置其中一个")
	}
	if err := o.ensureLoginRegisterIPNotLimited(ctx, req.Ip); err != nil {
		return nil, err
	}
	var err error
	var attribute *chat2.Attribute
	if req.Account != "" {
		attribute, err = o.Database.GetAttributeByAccount(ctx, req.Account)
	} else if req.PhoneNumber != "" {
		if req.AreaCode == "" {
			return nil, errs.ErrArgs.Wrap("必须提供区号")
		}
		if req.AreaCode[0] != '+' {
			req.AreaCode = "+" + req.AreaCode
		}
		if _, err := strconv.ParseUint(req.AreaCode[1:], 10, 64); err != nil {
			return nil, errs.ErrArgs.Wrap("区号必须是数字")
		}
		attribute, err = o.Database.GetAttributeByPhone(ctx, req.AreaCode, req.PhoneNumber)
	} else if req.Email != "" {
		attribute, err = o.Database.GetAttributeByEmail(ctx, req.Email)
	} else {
		err = errs.ErrArgs.Wrap("account or phone number or email must be set")
	}
	if err != nil {
		if o.Database.IsNotFound(err) {
			o.recordLoginRegisterIPFailure(ctx, req.Ip)
			return nil, eerrs.ErrPassword.Wrap()
		}
		return nil, err
	}
	if o.Provisioning != nil {
		if err := o.Provisioning.RequireReady(ctx, attribute.UserID); err != nil {
			return nil, err
		}
	}
	if err := o.Admin.CheckLogin(ctx, attribute.UserID, req.Ip); err != nil {
		return nil, err
	}
	if err := o.ensureLoginNotLocked(ctx, attribute.UserID, req.Ip); err != nil {
		return nil, err
	}

	// 单设备登录校验：从后台配置 singleDeviceLogin=1 开启
	// 只看 iOS/Android 的 app 登录记录，web/桌面端不计入绑定设备
	if conf, confErr := o.Admin.GetConfig(ctx); confErr == nil {
		if val := conf[constant.SingleDeviceLoginConfigKey]; isClientConfigEnabled(val) {
			platform := constant2.PlatformIDToName(int(req.Platform))
			if platform == "IOS" || platform == "Android" {
				deviceID := req.DeviceID
				if deviceID == "" {
					return nil, errs.ErrArgs.Wrap("设备ID不能为空")
				}
				lastRecord, recErr := o.Database.GetLatestAppLoginRecord(ctx, attribute.UserID)
				if recErr == nil && lastRecord.DeviceID != "" && lastRecord.DeviceID != deviceID {
					return nil, eerrs.ErrDeviceNotBound.Wrap()
				}
			}
		}
	}
	var verifyCodeID *uint
	var authenticatedAccount *chat2.Account
	var upgradePassword bool
	if req.Password == "" {
		key := verifyCodeKey(req.Email, req.AreaCode, req.PhoneNumber)
		if key == "" {
			return nil, errs.ErrArgs.Wrap("手机号、邮箱或账号不能为空")
		}
		id, err := o.verifyCodeStrict(ctx, key, req.VerifyCode, constant.VerificationCodeForLogin)
		if err != nil {
			return nil, err
		}
		verifyCodeID = &id
	} else {
		account, err := o.Database.GetAccount(ctx, attribute.UserID)
		if err != nil {
			return nil, err
		}
		valid, legacy, err := passwordstore.Verify(ctx, account.Password, req.Password)
		if err != nil {
			return nil, err
		}
		if !valid {
			o.recordLoginRegisterIPFailure(ctx, req.Ip)
			o.recordLoginFailure(ctx, attribute.UserID, req.Ip)
			return nil, eerrs.ErrPassword.Wrap()
		}
		authenticatedAccount = account
		upgradePassword = legacy
	}

	// 如果用户启用了谷歌验证码，且提供了验证码，则进行验证
	if attribute.GoogleAuthEnabled {
		if req.GoogleAuthCode == "" {
			return nil, errs.ErrArgs.Wrap("该账户已启用谷歌验证码，请输入验证码")
		}
		// 验证谷歌验证码
		if !totp.ValidateCode(attribute.GoogleAuthSecret, req.GoogleAuthCode, 1) {
			o.recordLoginFailure(ctx, attribute.UserID, req.Ip)
			return nil, errs.ErrArgs.Wrap("谷歌验证码错误")
		}
		log.ZInfo(ctx, "Google auth code validated successfully", "userID", attribute.UserID)
	} else if req.GoogleAuthCode != "" {
		// 如果用户没有启用谷歌验证码，但提供了验证码，提示用户
		log.ZWarn(ctx, "User provided google auth code but not enabled", nil, "userID", attribute.UserID)
		// 不返回错误，只是忽略验证码
	}
	if upgradePassword {
		hashed, err := passwordstore.Hash(ctx, req.Password)
		if err != nil {
			return nil, err
		}
		changed, err := o.Database.UpgradePasswordHash(ctx, attribute.UserID, authenticatedAccount.Password, hashed)
		if err != nil {
			return nil, err
		}
		if !changed {
			current, err := o.Database.GetAccount(ctx, attribute.UserID)
			if err != nil {
				return nil, err
			}
			valid, _, err := passwordstore.Verify(ctx, current.Password, req.Password)
			if err != nil || !valid {
				return nil, eerrs.ErrPassword.Wrap()
			}
		}
	}
	// 获取登录地区信息
	location := iputil.GetLocationByIPWithFallback(req.Ip)

	// 【调试日志】打印登录请求信息
	log.ZInfo(ctx, "Login request details",
		"userID", attribute.UserID,
		"ip", req.Ip,
		"deviceID", req.DeviceID,
		"platform", req.Platform,
		"DeviceModel", req.DeviceModel,
		"location", location)

	record := &chat2.UserLoginRecord{
		UserID:      attribute.UserID,
		LoginTime:   time.Now(),
		IP:          req.Ip,
		DeviceID:    req.DeviceID,
		Platform:    constant2.PlatformIDToName(int(req.Platform)),
		DeviceModel: req.DeviceModel, // 设备型号
		Location:    location,        // 登录地区
	}
	if err := o.Database.LoginRecord(ctx, record, verifyCodeID); err != nil {
		log.ZError(ctx, "Failed to save login record", err,
			"userID", record.UserID,
			"DeviceModel", record.DeviceModel)
		return nil, err
	}

	log.ZInfo(ctx, "Login record saved successfully",
		"userID", record.UserID,
		"DeviceModel", record.DeviceModel)
	chatToken, err := o.Admin.CreateToken(ctx, attribute.UserID, constant.NormalUser)
	if err != nil {
		return nil, err
	}
	o.clearLoginFailures(ctx, attribute.UserID, req.Ip)
	resp.UserID = attribute.UserID
	resp.ChatToken = chatToken.Token
	return resp, nil
}

// 获取用户最新的登录记录
func (o *chatSvr) GetUserLatestLoginRecord(ctx context.Context, req *chat.GetUserLatestLoginRecordReq) (*chat.GetUserLatestLoginRecordResp, error) {
	defer log.ZDebug(ctx, "return")

	opUserID, userType, err := mctx.Check(ctx)
	if err != nil {
		return nil, err
	}
	if userType != constant.AdminUser && req.UserID != opUserID {
		return nil, errs.ErrNoPermission.Wrap("无权查看其他用户的登录记录")
	}

	record, err := o.Database.GetLatestLoginRecord(ctx, req.UserID)
	if err != nil {
		if o.Database.IsNotFound(err) {
			return &chat.GetUserLatestLoginRecordResp{}, nil
		}
		return nil, err
	}

	// 检测同一设备是否有其他账户登录
	var otherUserIDs []string
	var isMultiDevice bool
	if record.DeviceID != "" {
		otherUserIDs, err = o.Database.GetOtherUserIDsByDeviceID(ctx, record.DeviceID, req.UserID)
		if err != nil {
			log.ZWarn(ctx, "Failed to check multi-device login", err, "deviceID", record.DeviceID)
			otherUserIDs = []string{}
		}
		isMultiDevice = len(otherUserIDs) > 0
	}

	resp := &chat.GetUserLatestLoginRecordResp{
		Record: &chat.UserLoginRecord{
			UserID:        record.UserID,
			LoginTime:     record.LoginTime.UnixMilli(),
			Ip:            record.IP,
			DeviceID:      record.DeviceID,
			Platform:      record.Platform,
			DeviceModel:   record.DeviceModel,
			Location:      record.Location,
			IsMultiDevice: isMultiDevice,
			OtherUserIDs:  otherUserIDs,
		},
	}

	return resp, nil
}

// 获取用户登录记录列表
func (o *chatSvr) GetUserLoginRecords(ctx context.Context, req *chat.GetUserLoginRecordsReq) (*chat.GetUserLoginRecordsResp, error) {
	defer log.ZDebug(ctx, "return")

	opUserID, userType, err := mctx.Check(ctx)
	if err != nil {
		return nil, err
	}
	if userType != constant.AdminUser && req.UserID != opUserID {
		return nil, errs.ErrNoPermission.Wrap("无权查看其他用户的登录记录列表")
	}

	total, records, err := o.Database.GetUserLoginRecords(ctx, req.UserID, req.Pagination.PageNumber, req.Pagination.ShowNumber)
	if err != nil {
		return nil, err
	}

	resp := &chat.GetUserLoginRecordsResp{
		Total:   total,
		Records: make([]*chat.UserLoginRecord, 0, len(records)),
	}

	for _, record := range records {
		// 检测同一设备是否有其他账户登录
		var otherUserIDs []string
		var isMultiDevice bool
		if record.DeviceID != "" {
			otherUserIDs, err = o.Database.GetOtherUserIDsByDeviceID(ctx, record.DeviceID, req.UserID)
			if err != nil {
				log.ZWarn(ctx, "Failed to check multi-device login", err, "deviceID", record.DeviceID)
				otherUserIDs = []string{}
			}
			isMultiDevice = len(otherUserIDs) > 0
		}

		resp.Records = append(resp.Records, &chat.UserLoginRecord{
			UserID:        record.UserID,
			LoginTime:     record.LoginTime.UnixMilli(),
			Ip:            record.IP,
			DeviceID:      record.DeviceID,
			Platform:      record.Platform,
			DeviceModel:   record.DeviceModel,
			Location:      record.Location,
			IsMultiDevice: isMultiDevice,
			OtherUserIDs:  otherUserIDs,
		})
	}

	return resp, nil
}
