package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/conversation"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
	"github.com/openimsdk/open-im-server/v3/internal/favorite"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
)

func registerFavoriteRoutes(router *gin.Engine, authenticate gin.HandlerFunc, rdb redis.UniversalClient, conversations conversation.ConversationClient) *mongo.Database {
	connection, err := unrelation.NewMongo()
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := favorite.NewMongoStore(ctx, connection.GetDatabase())
	if err != nil {
		panic(err)
	}
	api := &favoriteAPI{service: &favorite.Service{Store: store}, retention: &favorite.RetentionGate{Source: &favoriteMessageSource{
		conversations: conversations, messages: controller.NewReadOnlyMessageIdentityReader(rdb, connection.GetDatabase()),
	}}}
	group := router.Group("/favorite", authenticate)
	for _, operation := range []string{"add", "list", "search", "remove", "update", "info", "batch_operate", "stats", "category/create", "category/list", "category/update", "category/delete"} {
		group.POST("/"+operation, api.handle(operation))
	}
	return connection.GetDatabase()
}

type favoriteAPI struct {
	service   *favorite.Service
	retention *favorite.RetentionGate
}

func (a *favoriteAPI) handle(operation string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req favorite.Request
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024)
		decoder := json.NewDecoder(c.Request.Body)
		if err := decoder.Decode(&req); err != nil {
			apiresp.GinError(c, errs.ErrArgs.Wrap("收藏请求无效或超过1MiB"))
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			apiresp.GinError(c, errs.ErrArgs.Wrap("收藏请求只能包含一个JSON对象"))
			return
		}
		actor, _ := c.Value(constant.OpUserID).(string)
		ctx, cancel := context.WithTimeout(c, 15*time.Second)
		defer cancel()
		if operation == "add" {
			if err := a.retention.Authorize(ctx, actor, req); err != nil {
				apiresp.GinError(c, err)
				return
			}
		}
		result, err := a.service.Execute(ctx, actor, operation, req)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		apiresp.GinSuccess(c, favorite.PublicRetentionResult(result))
	}
}
