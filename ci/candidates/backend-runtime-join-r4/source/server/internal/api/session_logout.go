package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/msggateway"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"github.com/openimsdk/open-im-server/v3/internal/sessionlogout"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
)

func registerSessionLogoutRoutes(router gin.IRouter, db *mongo.Database, rdb redis.UniversalClient, discov discoveryregistry.SvcDiscoveryRegistry) {
	service, err := registerSessionLogoutRoutesWithContext(context.Background(), router, db, rdb, discov)
	if err != nil {
		panic(err)
	}
	go service.Run(context.Background())
}
func registerSessionLogoutRoutesWithContext(parent context.Context, router gin.IRouter, db *mongo.Database, rdb redis.UniversalClient, discov discoveryregistry.SvcDiscoveryRegistry) (*sessionlogout.Service, error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	service, err := sessionlogout.New(ctx, db, rdb, func(ctx context.Context, user string, platform int, digest string) error {
		conns, err := discov.GetConns(ctx, config.Config.RpcRegisterName.OpenImMessageGatewayName)
		if err != nil {
			return err
		}
		if len(conns) == 0 {
			return errs.ErrInternalServer.Wrap("网关连接暂不可用")
		}
		ctx = context.WithValue(ctx, constant.OperationID, "logout-session-"+digest[:16])
		ctx = context.WithValue(ctx, constant.OpUserID, user)
		for _, conn := range conns {
			if _, err := msggateway.NewMsgGatewayClient(conn).KickTokenOffline(ctx, &msggateway.KickTokenOfflineReq{UserID: user, PlatformID: int32(platform), TokenHash: digest}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	router.POST("/auth/logout", sessionLogoutHandler(service))
	router.POST("/auth/logout_session", GinParseToken(rdb), sessionDigestLogoutHandler(service))
	return service, nil
}
func sessionLogoutHandler(service *sessionlogout.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Verify signature and expiry even for an already removed status entry, so
		// retry is possible without reopening arbitrary revoked credentials.
		token := c.GetHeader("token")
		claims := &tokenverify.Claims{}
		parsed, err := jwt.ParseWithClaims(token, claims, authverify.Secret(), jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
		if err != nil || parsed == nil || !parsed.Valid || claims.UserID == "" || constant.PlatformIDToName(claims.PlatformID) == "" {
			apiresp.GinError(c, errs.ErrTokenInvalid.Wrap())
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		var request struct {
			UserID string `json:"userID,omitempty"`
		}
		if decoder.Decode(&request) != nil {
			apiresp.GinError(c, errs.ErrArgs.Wrap("注销请求无效"))
			return
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			apiresp.GinError(c, errs.ErrArgs.Wrap("注销请求只能包含一个对象"))
			return
		}
		if request.UserID != "" && request.UserID != claims.UserID {
			apiresp.GinError(c, errs.ErrNoPermission.Wrap("注销账号不匹配"))
			return
		}
		job, err := service.Begin(c, claims.UserID, claims.PlatformID, token)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		if err := service.Process(c, job); err != nil {
			apiresp.GinError(c, errs.ErrInternalServer.Wrap("会话注销已排队，等待重试"))
			return
		}
		apiresp.GinSuccess(c, gin.H{"revoked": true, "sessionClosed": true})
	}
}

func sessionDigestLogoutHandler(service *sessionlogout.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := authverify.CheckAdmin(c); err != nil {
			apiresp.GinError(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		var req struct {
			UserID    string `json:"userID"`
			Platform  int    `json:"platform"`
			TokenHash string `json:"tokenHash"`
		}
		if decoder.Decode(&req) != nil {
			apiresp.GinError(c, errs.ErrArgs.Wrap("注销摘要请求无效"))
			return
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			apiresp.GinError(c, errs.ErrArgs.Wrap("退出请求只能包含一个对象"))
			return
		}
		job, err := service.BeginDigest(c, req.UserID, req.Platform, req.TokenHash)
		if err != nil {
			apiresp.GinError(c, errs.ErrArgs.Wrap("会话摘要无效"))
			return
		}
		if err := service.Process(c, job); err != nil {
			apiresp.GinError(c, errs.ErrInternalServer.Wrap("会话注销已排队，等待重试"))
			return
		}
		apiresp.GinSuccess(c, gin.H{"revoked": true, "sessionClosed": true})
	}
}
