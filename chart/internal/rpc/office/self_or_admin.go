package office

import (
	"context"

	"github.com/OpenIMSDK/chat/pkg/common/mctx"
)

// selfOrAdmin 请求里的 userID 为空时取当前登录用户；不为空时只允许本人或管理员。
// 之前很多接口只做了「为空取本人」，填别人的 userID 就能冒充别人点赞评论、读别人的朋友圈。
func selfOrAdmin(ctx context.Context, userID string) (string, error) {
	opUserID, _, err := mctx.Check(ctx)
	if err != nil {
		return "", err
	}
	if userID == "" {
		return opUserID, nil
	}
	if err := mctx.CheckAdminOr(ctx, userID); err != nil {
		return "", err
	}
	return userID, nil
}
