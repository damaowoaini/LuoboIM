package api

import (
	"encoding/hex"

	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	"github.com/OpenIMSDK/chat/pkg/common/passwordstore"
	"github.com/OpenIMSDK/chat/pkg/common/totp"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
)

type AccountCancellationApi struct {
	db      database.ChatDatabaseInterface
	service *accountstate.Service
}

func (a *AccountCancellationApi) Cancel(c *gin.Context) {
	var req struct {
		UserID         string `json:"userID"`
		Password       string `json:"password" binding:"required"`
		GoogleAuthCode string `json:"googleAuthCode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("需要当前密码"))
		return
	}
	actor, userType, err := ginOpUser(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if userType != constant.NormalUser || (req.UserID != "" && req.UserID != actor) {
		apiresp.GinError(c, errs.ErrNoPermission.Wrap("只能注销本人账号"))
		return
	}
	password, err := hex.DecodeString(req.Password)
	if err != nil || len(password) != 16 {
		apiresp.GinError(c, errs.ErrArgs.Wrap("密码格式错误"))
		return
	}
	account, err := a.db.GetAccount(c, actor)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	valid, _, err := passwordstore.Verify(c, account.Password, req.Password)
	if err != nil {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("密码验证暂不可用"))
		return
	}
	if !valid {
		apiresp.GinError(c, errs.ErrNoPermission.Wrap("当前密码错误"))
		return
	}
	attribute, err := a.db.GetAttribute(c, actor)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if attribute.GoogleAuthEnabled && !totp.ValidateCode(attribute.GoogleAuthSecret, req.GoogleAuthCode, 1) {
		apiresp.GinError(c, errs.ErrNoPermission.Wrap("谷歌验证码错误"))
		return
	}
	record, cleanupErr := a.service.Request(c, actor, actor)
	if record == nil {
		apiresp.GinError(c, cleanupErr)
		return
	}
	status := "sessions_revoked"
	if cleanupErr != nil {
		status = "cancellation_pending"
	}
	apiresp.GinSuccess(c, gin.H{"userID": actor, "status": status, "requestedAt": record.RequestedAt.UnixMilli(), "dataErased": false})
}
