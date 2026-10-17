package admin

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/totp"
	"github.com/OpenIMSDK/chat/pkg/eerrs"
	"github.com/OpenIMSDK/chat/pkg/proto/admin"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
)

// GetAdminGoogleAuthQRCode 获取管理员谷歌验证码二维码（通过账号密码验证）
func (o *adminServer) GetAdminGoogleAuthQRCode(ctx context.Context, req *admin.GetAdminGoogleAuthQRCodeReq) (*admin.GetAdminGoogleAuthQRCodeResp, error) {
	defer log.ZDebug(ctx, "return")
	
	// 通过账号密码验证身份
	adminInfo, err := o.Database.GetAdmin(ctx, req.Account)
	if err != nil {
		return nil, eerrs.ErrPassword.Wrap()
	}
	
	if !passwordEqual(adminInfo.Password, req.Password) {
		return nil, eerrs.ErrPassword.Wrap()
	}
	
	// 如果已经绑定，返回错误
	if adminInfo.GoogleAuthEnabled {
		return nil, errs.ErrArgs.Wrap("已绑定谷歌验证码，请先解绑")
	}
	
	secret := totp.GenerateSecret()
	if err := o.Database.UpdateAdmin(ctx, adminInfo.UserID, map[string]any{
		"google_auth_secret":  secret,
		"google_auth_enabled": false,
	}); err != nil {
		return nil, err
	}
	qrCodeURL := totp.GenerateQRCodeURL("OpenIM Admin", adminInfo.Account, secret)
	return &admin.GetAdminGoogleAuthQRCodeResp{
		QrCodeUrl: qrCodeURL,
	}, nil
}

// BindAdminGoogleAuth 绑定管理员谷歌验证码（通过账号密码验证）
func (o *adminServer) BindAdminGoogleAuth(ctx context.Context, req *admin.BindAdminGoogleAuthReq) (*admin.BindAdminGoogleAuthResp, error) {
	defer log.ZDebug(ctx, "return")
	
	// 通过账号密码验证身份
	adminInfo, err := o.Database.GetAdmin(ctx, req.Account)
	if err != nil {
		return nil, eerrs.ErrPassword.Wrap()
	}
	
	if !passwordEqual(adminInfo.Password, req.Password) {
		return nil, eerrs.ErrPassword.Wrap()
	}
	
	// 如果已经绑定，返回错误
	if adminInfo.GoogleAuthEnabled {
		return nil, errs.ErrArgs.Wrap("已绑定谷歌验证码")
	}
	
	if adminInfo.GoogleAuthSecret == "" {
		return nil, errs.ErrArgs.Wrap("请先获取二维码")
	}
	if !totp.ValidateCode(adminInfo.GoogleAuthSecret, req.Code, 1) {
		return nil, errs.ErrArgs.Wrap("验证码错误")
	}
	update := map[string]any{
		"google_auth_enabled": true,
	}
	
	if err := o.Database.UpdateAdmin(ctx, adminInfo.UserID, update); err != nil {
		return nil, err
	}
	
	log.ZInfo(ctx, "Admin google auth bound successfully", "userID", adminInfo.UserID)
	
	// 绑定成功，但不返回 token，用户需要重新登录并输入验证码
	return &admin.BindAdminGoogleAuthResp{}, nil
}

// ResetAdminGoogleAuth 重置管理员谷歌验证码（需要超级管理员权限）
func (o *adminServer) ResetAdminGoogleAuth(ctx context.Context, req *admin.ResetAdminGoogleAuthReq) (*admin.ResetAdminGoogleAuthResp, error) {
	defer log.ZDebug(ctx, "return")
	
	// 检查是否是超级管理员
	if err := o.CheckSuperAdmin(ctx); err != nil {
		return nil, err
	}
	
	// 更新数据库，清除谷歌验证码
	update := map[string]any{
		"google_auth_enabled": false,
		"google_auth_secret":  "",
	}
	
	if err := o.Database.UpdateAdmin(ctx, req.UserID, update); err != nil {
		return nil, err
	}
	
	log.ZInfo(ctx, "Admin google auth reset successfully", "targetUserID", req.UserID)
	
	return &admin.ResetAdminGoogleAuthResp{}, nil
}

// GetAdminGoogleAuthStatus 获取管理员谷歌验证码状态（通过账号密码验证）
func (o *adminServer) GetAdminGoogleAuthStatus(ctx context.Context, req *admin.GetAdminGoogleAuthStatusReq) (*admin.GetAdminGoogleAuthStatusResp, error) {
	defer log.ZDebug(ctx, "return")
	
	// 通过账号密码验证身份
	adminInfo, err := o.Database.GetAdmin(ctx, req.Account)
	if err != nil {
		return nil, eerrs.ErrPassword.Wrap()
	}
	
	if !passwordEqual(adminInfo.Password, req.Password) {
		return nil, eerrs.ErrPassword.Wrap()
	}
	
	return &admin.GetAdminGoogleAuthStatusResp{
		Enabled: adminInfo.GoogleAuthEnabled,
	}, nil
}
