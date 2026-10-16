package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	"github.com/OpenIMSDK/chat/pkg/common/adminprovision"
	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/tokenverify"
	imconstant "github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	imtoken "github.com/OpenIMSDK/tools/tokenverify"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type sessionLogoutRequest struct {
	IMToken string `json:"imToken"`
}
type imLogoutRequest struct {
	UserID string `json:"userID"`
}
type imLogoutResponse struct {
	Revoked       bool `json:"revoked"`
	SessionClosed bool `json:"sessionClosed"`
}
type currentSessionLogout struct{ service *accountstate.SessionService }

func newSessionLogout(db *gorm.DB, rdb redis.UniversalClient) *currentSessionLogout {
	caller := apicall.NewCallerInterface()
	service := accountstate.NewSessionService(db, rdb, func(ctx context.Context, user string, platform int, digest string) error {
		adminToken, err := caller.ImAdminTokenWithDefaultAdmin(ctx)
		if err != nil {
			return err
		}
		upstream := apicall.NewApiCaller[imLogoutDigestRequest, imLogoutResponse]("/auth/logout_session", func() string { return config.Config.OpenIMUrl })
		ctx = context.WithValue(ctx, constant.CtxApiToken, adminToken)
		result, err := upstream.Call(ctx, &imLogoutDigestRequest{UserID: user, Platform: platform, TokenHash: digest})
		if err != nil {
			return err
		}
		if result == nil || !result.Revoked || !result.SessionClosed {
			return errs.ErrInternalServer.Wrap("精确IM会话撤销尚未完成")
		}
		return nil
	})
	service.Ownership = func(ctx context.Context, id string) error { return adminprovision.RequireReady(ctx, db, id) }
	go service.Run(context.Background())
	return &currentSessionLogout{service: service}
}

type imLogoutDigestRequest struct {
	UserID    string `json:"userID"`
	Platform  int    `json:"platform"`
	TokenHash string `json:"tokenHash"`
}

func (s *currentSessionLogout) Logout(c *gin.Context) {
	chatToken := c.GetHeader("token")
	user, kind, err := tokenverify.GetToken(chatToken)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if kind != constant.NormalUser {
		apiresp.GinError(c, errs.ErrNoPermission.Wrap("此接口仅退出本人用户会话"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var req sessionLogoutRequest
	if decoder.Decode(&req) != nil || req.IMToken == "" {
		apiresp.GinError(c, errs.ErrArgs.Wrap("需要当前IM会话凭证"))
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		apiresp.GinError(c, errs.ErrArgs.Wrap("退出请求只能包含一个对象"))
		return
	}
	// Verify the IM signature and owner before persisting a digest. Expired
	// credentials are safe to revoke; no authenticated reads/actions are granted.
	claims := &imtoken.Claims{}
	parsed, err := jwt.ParseWithClaims(req.IMToken, claims, func(*jwt.Token) (interface{}, error) { return []byte(*config.Config.Secret), nil }, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithoutClaimsValidation())
	if err != nil || parsed == nil || !parsed.Valid || claims.UserID != user || imconstant.PlatformIDToName(claims.PlatformID) == "" {
		apiresp.GinError(c, errs.ErrNoPermission.Wrap("IM会话签名或归属不匹配"))
		return
	}
	row, err := s.service.Request(c, user, claims.PlatformID, chatToken, req.IMToken)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := s.service.RevokeChat(c, row); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := s.service.Process(c, row); err != nil {
		apiresp.GinSuccess(c, gin.H{"revoked": true, "sessionClosed": false, "pending": true})
		return
	}
	apiresp.GinSuccess(c, gin.H{"revoked": true, "sessionClosed": true, "pending": false})
}
