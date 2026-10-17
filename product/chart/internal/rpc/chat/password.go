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
	"github.com/OpenIMSDK/tools/log"

	"github.com/OpenIMSDK/tools/errs"

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/common/passwordstore"
	"github.com/OpenIMSDK/chat/pkg/proto/chat"
)

func (o *chatSvr) ResetPassword(ctx context.Context, req *chat.ResetPasswordReq) (*chat.ResetPasswordResp, error) {
	defer log.ZDebug(ctx, "return")
	if req.Password == "" {
		return nil, errs.ErrArgs.Wrap("密码必须设置")
	}

	// 【修改】找回密码不允许使用万能验证码，使用专用的验证方法
	var verifyCodeID uint
	var err error
	key := verifyCodeKey(req.Email, req.AreaCode, req.PhoneNumber)
	if key == "" {
		return nil, errs.ErrArgs.Wrap("手机号或邮箱不能为空")
	}
	verifyCodeID, err = o.verifyCodeStrict(ctx, key, req.VerifyCode, constant.VerificationCodeForResetPassword)

	if err != nil {
		return nil, err
	}

	var targetUserID string
	if req.Email == "" {
		attribute, err := o.Database.GetAttributeByPhone(ctx, req.AreaCode, req.PhoneNumber)
		if err != nil {
			return nil, err
		}
		targetUserID = attribute.UserID
		if o.Provisioning != nil {
			if err := o.Provisioning.RequireReady(ctx, targetUserID); err != nil {
				return nil, err
			}
		}
		err = o.Database.UpdatePasswordAndDeleteVerifyCode(ctx, attribute.UserID, req.Password, verifyCodeID)
	} else {
		attribute, err := o.Database.GetAttributeByEmail(ctx, req.Email)
		if err != nil {
			return nil, err
		}
		targetUserID = attribute.UserID
		if o.Provisioning != nil {
			if err := o.Provisioning.RequireReady(ctx, targetUserID); err != nil {
				return nil, err
			}
		}
		err = o.Database.UpdatePasswordAndDeleteVerifyCode(ctx, attribute.UserID, req.Password, verifyCodeID)
	}

	if err != nil {
		return nil, err
	}
	if err := o.Credentials.ProcessUser(ctx, targetUserID); err != nil {
		// The password and outbox are already committed together. The worker
		// retries, and authentication refuses tokens until revocation completes.
		log.ZWarn(ctx, "password reset committed; session revocation pending", err, "userID", targetUserID)
	}
	return &chat.ResetPasswordResp{}, nil
}

func (o *chatSvr) ChangePassword(ctx context.Context, req *chat.ChangePasswordReq) (*chat.ChangePasswordResp, error) {
	defer log.ZDebug(ctx, "return")
	if req.NewPassword == "" {
		return nil, errs.ErrArgs.Wrap("新密码必须设置")
	}
	if req.NewPassword == req.CurrentPassword {
		return nil, errs.ErrArgs.Wrap("新密码不能与当前密码相同")
	}
	opUserID, userType, err := mctx.Check(ctx)
	if err != nil {
		return nil, err
	}
	switch userType {
	case constant.NormalUser:
		if req.UserID == "" {
			req.UserID = opUserID
		}
		if req.UserID != opUserID {
			return nil, errs.ErrNoPermission.Wrap("无权限修改其他用户密码")
		}
	case constant.AdminUser:
		if req.UserID == "" {
			return nil, errs.ErrArgs.Wrap("用户ID必须设置")
		}
	default:
		return nil, errs.ErrInternalServer.Wrap("无效的用户类型")
	}
	if o.Provisioning != nil {
		if err := o.Provisioning.RequireReady(ctx, req.UserID); err != nil {
			return nil, err
		}
	}
	user, err := o.Database.GetUser(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if userType != constant.AdminUser {
		valid, _, err := passwordstore.Verify(ctx, user.Password, req.CurrentPassword)
		if err != nil {
			return nil, err
		}
		if !valid && !(user.Password == "" && req.CurrentPassword == "") {
			return nil, errs.ErrNoPermission.Wrap("当前密码错误")
		}
	}
	if req.NewPassword != "" {
		if err := o.Database.UpdatePassword(ctx, req.UserID, req.NewPassword); err != nil {
			return nil, err
		}
		if err := o.Credentials.ProcessUser(ctx, req.UserID); err != nil {
			log.ZWarn(ctx, "password change committed; session revocation pending", err, "userID", req.UserID)
		}
	}
	return &chat.ChangePasswordResp{}, nil
}
