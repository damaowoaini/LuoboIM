package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/OpenIMSDK/protocol/conversation"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
	"github.com/openimsdk/open-im-server/v3/internal/adminpreview"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/adminauth"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"io"
	"net/http"
	"time"
)

const previewChartTokenHeader = "X-Luobo-Chart-Admin-Token"

type adminPreviewAPI struct {
	service *adminpreview.Service
	redis   redis.UniversalClient
}

func registerAdminPreviewRoutes(router *gin.Engine, authenticate gin.HandlerFunc, db *mongo.Database, rdb redis.UniversalClient, conversations conversation.ConversationClient) {
	registerAdminPreviewRoutesWithContext(context.Background(), router, authenticate, db, rdb, conversations)
}
func registerAdminPreviewRoutesWithContext(parent context.Context, router *gin.Engine, authenticate gin.HandlerFunc, db *mongo.Database, rdb redis.UniversalClient, conversations conversation.ConversationClient) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	audit, err := adminpreview.NewMongoAudit(ctx, db)
	if err != nil {
		panic(err)
	}
	historyReader, err := messagedelivery.NewHistoryWindowReaderFromEnvironment(db)
	if err != nil {
		panic(err)
	}
	if err := historyReader.CheckReady(ctx); err != nil {
		panic(err)
	}
	key := sha256.Sum256([]byte("luobo-admin-preview-cursor-v1\n" + config.Config.Secret))
	api := &adminPreviewAPI{redis: rdb, service: &adminpreview.Service{Source: &adminpreview.DataSource{Database: db, Redis: rdb, Reader: controller.NewReadOnlyMessageReader(rdb, db, historyReader), Conversations: conversations}, Audit: audit, CursorKey: key[:]}}
	group := router.Group("/admin/message_preview", authenticate)
	group.POST("/conversations", api.handle("conversations"))
	group.POST("/search", api.handle("search"))
}
func (a *adminPreviewAPI) handle(operation string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := authverify.CheckAdmin(c); err != nil {
			apiresp.GinError(c, err)
			return
		}
		actor, err := adminauth.VerifyChartAdmin(c, a.redis, c.GetHeader(previewChartTokenHeader), "")
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		if !adminauth.PreviewAllowed(actor) {
			apiresp.GinError(c, errs.ErrNoPermission.Wrap("管理员未获message_preview只读权限"))
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		var req adminpreview.Request
		if decoder.Decode(&req) != nil {
			apiresp.GinError(c, errs.ErrArgs.Wrap("预览请求无效或过长"))
			return
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			apiresp.GinError(c, errs.ErrArgs.Wrap("预览请求只能包含一个JSON对象"))
			return
		}
		ctx, cancel := context.WithTimeout(c, 15*time.Second)
		defer cancel()
		var result any
		if operation == "conversations" {
			result, err = a.service.Conversations(ctx, actor, req)
		} else {
			result, err = a.service.Search(ctx, actor, req)
		}
		if err != nil {
			if _, ok := errs.Unwrap(err).(interface{ Code() int }); !ok {
				err = errs.ErrInternalServer.Wrap("消息预览暂不可用")
			}
			apiresp.GinError(c, err)
			return
		}
		apiresp.GinSuccess(c, result)
	}
}
