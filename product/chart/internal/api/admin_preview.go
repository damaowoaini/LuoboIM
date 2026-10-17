package api

import (
	"encoding/json"
	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/proto/admin"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"os"
)

func (a *AdminApi) previewPermission(c *gin.Context, superOnly bool) error {
	actor, err := mctx.CheckAdmin(c)
	if err != nil {
		return err
	}
	info, err := a.adminClient.GetAdminInfo(c, &admin.GetAdminInfoReq{})
	if err != nil {
		return err
	}
	if info.UserID != actor {
		return errs.ErrNoPermission.Wrap("管理员身份不匹配")
	}
	if info.Level == constant.AdvancedUserLevel {
		return nil
	}
	if superOnly {
		return errs.ErrNoPermission.Wrap("仅超级管理员可使用兼容凭证接口")
	}
	raw := os.Getenv("LUOBO_MESSAGE_PREVIEW_ADMIN_IDS")
	var ids []string
	if len(raw) <= 65536 && json.Unmarshal([]byte(raw), &ids) == nil {
		for _, id := range ids {
			if id != "" && id == actor {
				return nil
			}
		}
	}
	return errs.ErrNoPermission.Wrap("管理员未获message_preview只读权限")
}
func (a *AdminApi) adminPreview(operation string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := a.previewPermission(c, false); err != nil {
			apiresp.GinError(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		var req apicall.AdminPreviewRequest
		if decoder.Decode(&req) != nil {
			apiresp.GinError(c, errs.ErrArgs.Wrap("预览请求无效或过长"))
			return
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			apiresp.GinError(c, errs.ErrArgs.Wrap("预览请求只能包含一个JSON对象"))
			return
		}
		result, err := apicall.ReadAdminPreview(c, a.imApiCaller, c.GetHeader("token"), operation, req)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		apiresp.GinSuccess(c, result)
	}
}
