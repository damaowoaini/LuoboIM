package adminauth

import (
	"context"
	"encoding/json"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/golang-jwt/jwt/v4"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/redis/go-redis/v9"
	"os"
)

type Claims struct {
	UserID   string
	UserType int32
	jwt.RegisteredClaims
}

func VerifyChartAdmin(ctx context.Context, rdb redis.UniversalClient, credential, expectedActor string) (string, error) {
	if credential == "" || len(credential) > 8192 || rdb == nil {
		return "", errs.ErrNoPermission.Wrap("后台认证不可用")
	}
	secret := os.Getenv("LUOBO_CHART_TOKEN_SECRET")
	if secret == "" {
		secret = config.Config.Secret
	}
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(credential, claims, func(*jwt.Token) (any, error) { return []byte(secret), nil }, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !token.Valid || claims.ExpiresAt == nil || claims.UserID == "" || claims.UserType != 2 || expectedActor != "" && expectedActor != claims.UserID {
		return "", errs.ErrNoPermission.Wrap("后台令牌无效")
	}
	disabled, err := rdb.Exists(ctx, "ACCOUNT_DISABLED:"+claims.UserID).Result()
	if err != nil || disabled != 0 {
		return "", errs.ErrNoPermission.Wrap("后台账号已禁用或认证不可用")
	}
	status, err := rdb.HGet(ctx, "CHAT_UID_TOKEN_STATUS:"+claims.UserID, credential).Int()
	if err != nil || status != 0 {
		return "", errs.ErrNoPermission.Wrap("后台会话已失效")
	}
	return claims.UserID, nil
}
func PreviewAllowed(actor string) bool {
	for _, id := range append(append([]string{}, config.Config.IMAdmin.UserID...), config.Config.Manager.UserID...) {
		if id != "" && actor == id {
			return true
		}
	}
	raw := os.Getenv("LUOBO_MESSAGE_PREVIEW_ADMIN_IDS")
	if len(raw) > 65536 {
		return false
	}
	var ids []string
	if json.Unmarshal([]byte(raw), &ids) != nil {
		return false
	}
	for _, id := range ids {
		if id != "" && id == actor {
			return true
		}
	}
	return false
}
