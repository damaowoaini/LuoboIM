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

package eerrs

import "github.com/OpenIMSDK/tools/errs"

var (
	ErrPassword                 = errs.NewCodeError(20001, "账号或密码错误") // 登录失败统一文案，避免泄露账号是否存在
	ErrAccountNotFound          = errs.NewCodeError(20002, "账号不存在")          // 账号不存在（非登录接口内部仍可用）
	ErrPhoneAlreadyRegister     = errs.NewCodeError(20003, "手机号已注册")     // 手机号已经注册
	ErrAccountAlreadyRegister   = errs.NewCodeError(20004, "账号已注册")   // 账号已经注册
	ErrVerifyCodeSendFrequently = errs.NewCodeError(20005, "验证码发送过于频繁") // 频繁获取验证码
	ErrVerifyCodeNotMatch       = errs.NewCodeError(20006, "验证码错误")       // 验证码错误
	ErrVerifyCodeExpired        = errs.NewCodeError(20007, "验证码已过期")        // 验证码过期
	ErrVerifyCodeMaxCount       = errs.NewCodeError(20008, "验证码错误次数过多")       // 验证码失败次数过多
	ErrVerifyCodeUsed           = errs.NewCodeError(20009, "验证码已使用")           // 已经使用
	ErrInvitationCodeUsed       = errs.NewCodeError(20010, "邀请码已使用")       // 邀请码已经使用
	ErrInvitationNotFound       = errs.NewCodeError(20011, "邀请码不存在")       // 邀请码不存在
	ErrForbidden                = errs.NewCodeError(20012, "禁止登录或注册")                // 限制登录注册
	ErrRefuseFriend             = errs.NewCodeError(20013, "拒绝添加好友")             // 拒绝添加好友
	ErrEmailAlreadyRegister     = errs.NewCodeError(20014, "邮箱已注册")     // 邮箱已经注册
	ErrDeviceNotBound           = errs.NewCodeError(20015, "该账号已绑定其他设备，禁止登录") // 设备不匹配
	ErrDailyAddFriendLimit      = errs.NewCodeError(20016, "今日加好友申请人数已达上限") // 每日加好友申请人数限制
	ErrVerifyCodeIPLimit        = errs.NewCodeError(20017, "同一IP一小时内登录/注册操作次数已达上限") // 同一 IP 每小时登录失败或发验证码限制
	ErrLoginLocked              = errs.NewCodeError(20018, "登录失败次数过多，请稍后再试")         // 账号登录失败锁定
)
