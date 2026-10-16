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

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/common/totp"
	"github.com/OpenIMSDK/chat/pkg/proto/chat"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
)

func checkGoogleAuthOwner(ctx context.Context, targetUserID string) error {
	opUserID, userType, err := mctx.Check(ctx)
	if err != nil {
		return err
	}
	if userType == constant.AdminUser {
		return nil
	}
	if targetUserID != opUserID {
		return errs.ErrNoPermission.Wrap("只能操作自己的谷歌验证码")
	}
	return nil
}

// BindGoogleAuth 绑定谷歌验证码
func (o *chatSvr) BindGoogleAuth(ctx context.Context, req *chat.BindGoogleAuthReq) (*chat.BindGoogleAuthResp, error) {
	defer log.ZDebug(ctx, "return")
	if err := checkGoogleAuthOwner(ctx, req.UserID); err != nil {
		return nil, err
	}
	attribute, err := o.Database.GetAttribute(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if attribute.GoogleAuthSecret == "" {
		return nil, errs.ErrArgs.Wrap("请先获取二维码")
	}
	if !totp.ValidateCode(attribute.GoogleAuthSecret, req.Code, 1) {
		return nil, errs.ErrArgs.Wrap("验证码错误")
	}
	updateData := map[string]any{
		"google_auth_enabled": true,
	}
	if err := o.Database.UpdateUseInfo(ctx, req.UserID, updateData); err != nil {
		log.ZError(ctx, "更新用户谷歌验证码失败", err, "userID", req.UserID)
		return nil, err
	}
	log.ZInfo(ctx, "绑定谷歌验证码成功", "userID", req.UserID)
	return &chat.BindGoogleAuthResp{}, nil
}

// GetGoogleAuthQRCode 获取谷歌验证码二维码
func (o *chatSvr) GetGoogleAuthQRCode(ctx context.Context, req *chat.GetGoogleAuthQRCodeReq) (*chat.GetGoogleAuthQRCodeResp, error) {
	defer log.ZDebug(ctx, "return")
	if err := checkGoogleAuthOwner(ctx, req.UserID); err != nil {
		return nil, err
	}
	attribute, err := o.Database.GetAttribute(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if attribute.GoogleAuthEnabled {
		return nil, errs.ErrArgs.Wrap("已绑定谷歌验证码，请先重置")
	}
	secret := totp.GenerateSecret()
	if err := o.Database.UpdateUseInfo(ctx, req.UserID, map[string]any{
		"google_auth_secret":  secret,
		"google_auth_enabled": false,
	}); err != nil {
		return nil, err
	}
	qrCodeURL := totp.GenerateQRCodeURL("OpenIM", req.UserID, secret)
	log.ZInfo(ctx, "生成谷歌验证码二维码", "userID", req.UserID)
	return &chat.GetGoogleAuthQRCodeResp{
		QrCodeURL: qrCodeURL,
	}, nil
}

// ResetGoogleAuth 重置谷歌验证码
func (o *chatSvr) ResetGoogleAuth(ctx context.Context, req *chat.ResetGoogleAuthReq) (*chat.ResetGoogleAuthResp, error) {
	defer log.ZDebug(ctx, "return")
	if err := checkGoogleAuthOwner(ctx, req.UserID); err != nil {
		return nil, err
	}
	updateData := map[string]any{
		"google_auth_secret":  "",
		"google_auth_enabled": false,
	}
	if err := o.Database.UpdateUseInfo(ctx, req.UserID, updateData); err != nil {
		log.ZError(ctx, "重置用户谷歌验证码失败", err, "userID", req.UserID)
		return nil, err
	}
	log.ZInfo(ctx, "重置谷歌验证码成功", "userID", req.UserID)
	return &chat.ResetGoogleAuthResp{}, nil
}

// GetUserGoogleAuthStatus 获取用户谷歌验证码状态
func (o *chatSvr) GetUserGoogleAuthStatus(ctx context.Context, req *chat.GetUserGoogleAuthStatusReq) (*chat.GetUserGoogleAuthStatusResp, error) {
	defer log.ZDebug(ctx, "return")
	if err := checkGoogleAuthOwner(ctx, req.UserID); err != nil {
		return nil, err
	}
	attribute, err := o.Database.GetAttribute(ctx, req.UserID)
	if err != nil {
		if o.Database.IsNotFound(err) {
			return &chat.GetUserGoogleAuthStatusResp{Enabled: false}, nil
		}
		log.ZError(ctx, "获取用户属性失败", err, "userID", req.UserID)
		return nil, err
	}
	return &chat.GetUserGoogleAuthStatusResp{
		Enabled: attribute.GoogleAuthEnabled,
	}, nil
}
