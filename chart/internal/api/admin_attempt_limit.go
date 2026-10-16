package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/db/cache"
	"github.com/OpenIMSDK/chat/pkg/eerrs"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/gin-gonic/gin"
)

// adminAttemptLimitFromConfig 后台登录、登录前谷歌验证码接口的尝试次数限制。
func adminAttemptLimitFromConfig(lock cache.LoginFailLockInterface) gin.HandlerFunc {
	c := config.Config.LoginLock
	perAccountIP, perIP := int64(10), int64(30)
	window := 15 * time.Minute
	if c.AdminMaxPerAccountIP > 0 {
		perAccountIP = c.AdminMaxPerAccountIP
	}
	if c.AdminMaxPerIP > 0 {
		perIP = c.AdminMaxPerIP
	}
	if c.WindowMinutes > 0 {
		window = time.Duration(c.WindowMinutes) * time.Minute
	}
	return adminAttemptLimit(lock, perAccountIP, perIP, window)
}

// adminAttemptLimit 按「账号+IP」和「IP」统计尝试次数（不论成败），超限直接拒绝。
// 管理员登录频率很低，按尝试计数足够，也不用等下游返回再判断成败。
func adminAttemptLimit(lock cache.LoginFailLockInterface, perAccountIP, perIP int64, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, 64<<10))
		if err != nil {
			apiresp.GinError(c, errs.ErrArgs.Wrap("读取请求失败"))
			c.Abort()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		var p struct {
			Account string `json:"account"`
		}
		_ = json.Unmarshal(body, &p)
		account := strings.TrimSpace(p.Account)
		ip := resolveClientIP(c, "")

		type rule struct {
			key   string
			limit int64
		}
		rules := []rule{{"adminip:" + ip, perIP}}
		if account != "" {
			rules = append(rules, rule{"admin:" + account + ":" + ip, perAccountIP})
		}
		for _, r := range rules {
			n, err := lock.Count(c, r.key)
			if err != nil {
				log.ZError(c, "adminAttemptLimit: 读取计数失败，放行", err)
				continue
			}
			if n >= r.limit {
				log.ZWarn(c, "admin login attempts exceeded", nil, "ip", ip, "account", account)
				apiresp.GinError(c, eerrs.ErrLoginLocked.Wrap(fmt.Sprintf("尝试次数过多，请 %d 分钟后再试", int(window.Minutes()))))
				c.Abort()
				return
			}
		}
		for _, r := range rules {
			if _, err := lock.Incr(c, r.key, window); err != nil {
				log.ZError(c, "adminAttemptLimit: 记录计数失败", err)
			}
		}
		c.Next()
	}
}
