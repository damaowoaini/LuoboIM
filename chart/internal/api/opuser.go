package api

import (
	"strconv"

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
)

func ginOpUser(c *gin.Context) (userID string, userType int32, err error) {
	v, ok := c.Get(constant.RpcOpUserID)
	if !ok {
		return "", 0, errs.ErrNoPermission.Wrap("未登录")
	}
	userID, _ = v.(string)
	if userID == "" {
		return "", 0, errs.ErrNoPermission.Wrap("未登录")
	}
	tv, ok := c.Get(constant.RpcOpUserType)
	if ok {
		if arr, ok := tv.([]string); ok && len(arr) > 0 {
			t, _ := strconv.Atoi(arr[0])
			userType = int32(t)
		}
	}
	return userID, userType, nil
}

func ginRequireAdmin(c *gin.Context) bool {
	_, userType, err := ginOpUser(c)
	if err != nil {
		apiresp.GinError(c, err)
		return false
	}
	if userType != constant.AdminUser {
		apiresp.GinError(c, errs.ErrNoPermission.Wrap("需要管理员权限"))
		return false
	}
	return true
}
