package api

import (
	"net/http"

	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/gin-gonic/gin"
)

const registrationPendingMessage = "注册已保存，正在恢复账号，请稍后使用原密码登录"

// This response is used only after a trusted RPC/SQL receipt established that
// the account committed. It never infers rollback or commitment from a numeric
// error code and never returns a partially issued credential pair.
func registrationCommittedFailure(c *gin.Context, err error) {
	response := apiresp.ParseError(err)
	response.ErrMsg, response.ErrDlt = registrationPendingMessage, ""
	response.Data = gin.H{"registrationPending": true, "requiresLogin": true}
	c.JSON(http.StatusOK, response)
}
