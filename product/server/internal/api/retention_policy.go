package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/gin-gonic/gin"
	"github.com/openimsdk/open-im-server/v3/internal/retention"
	"go.mongodb.org/mongo-driver/mongo"
)

type retentionReadSource interface {
	Readback(context.Context, string, string) (retention.Readback, error)
}

func registerRetentionPolicyReadback(r *gin.Engine, authenticate gin.HandlerFunc, db *mongo.Database) {
	r.POST("/conversation/get_retention_policy", authenticate, retentionPolicyReadback(retention.NewMongoStore(db)))
}

func retentionPolicyReadback(source retentionReadSource) gin.HandlerFunc {
	return func(c *gin.Context) {
		owner := mcontext.GetOpUserID(c)
		if owner == "" {
			apiresp.GinError(c, errs.ErrNoPermission.Wrap("authenticated owner required"))
			return
		}
		var req struct {
			Conversation string `json:"conversationID"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil || req.Conversation == "" || len(req.Conversation) > 2048 {
			apiresp.GinError(c, errs.ErrArgs.Wrap("invalid retention readback request"))
			return
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			apiresp.GinError(c, errs.ErrArgs.Wrap("single retention readback request required"))
			return
		}
		ctx, cancel := context.WithTimeout(c, 5*time.Second)
		defer cancel()
		value, err := source.Readback(ctx, owner, req.Conversation)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				apiresp.GinError(c, errs.ErrRecordNotFound.Wrap("conversation not found"))
			} else {
				apiresp.GinError(c, errs.ErrInternalServer.Wrap("retention state unavailable"))
			}
			return
		}
		if value.Owner != owner || value.Conversation != req.Conversation {
			apiresp.GinError(c, errs.ErrInternalServer.Wrap("retention state unavailable"))
			return
		}
		apiresp.GinSuccess(c, value)
	}
}
