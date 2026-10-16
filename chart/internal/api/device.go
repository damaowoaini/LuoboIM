package api

import (
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
)

// DeviceApi 管理员设备绑定管理（不走 gRPC，直接访问 Chat 数据库）
type DeviceApi struct {
	chatDB database.ChatDatabaseInterface
}

func NewDeviceApi(chatDB database.ChatDatabaseInterface) *DeviceApi {
	return &DeviceApi{chatDB: chatDB}
}

type unbindUserDeviceReq struct {
	UserID string `json:"userID" binding:"required"`
}

// UnbindUserDevice 清除用户设备绑定，下次登录可在任意设备重新绑定。
// POST /user/device/unbind  (需要 mw.CheckAdmin)
func (o *DeviceApi) UnbindUserDevice(c *gin.Context) {
	var req unbindUserDeviceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap(err.Error()))
		return
	}
	_, err := mctx.CheckAdmin(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := o.chatDB.DeleteLoginRecordsByUserID(c, req.UserID); err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, nil)
}
