package api

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
)

// Dynamic chart administrators use the configured IM service administrator for
// existing management operations. This helper accepts only an authenticated
// chart admin context. GetIMAdmin itself remains strict for user revocation.
func adminIMToken(ctx context.Context, caller apicall.CallerInterface) (string, string, error) {
	actor, err := mctx.CheckAdmin(ctx)
	if err != nil {
		return "", "", err
	}
	identity := config.GetIMAdmin(actor)
	if identity == "" {
		identity = config.GetDefaultIMAdmin()
	}
	if identity == "" {
		return "", "", errs.ErrInternalServer.Wrap("IM管理服务身份未配置")
	}
	token, err := caller.UserToken(ctx, identity, constant.AdminPlatformID)
	return identity, token, err
}
func currentAdminIMToken(ctx context.Context, caller apicall.CallerInterface) (string, error) {
	_, token, err := adminIMToken(ctx, caller)
	return token, err
}
