package api

import (
	"context"
	"sync"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/iptrust"
	"github.com/OpenIMSDK/tools/log"
	"github.com/gin-gonic/gin"
)

var (
	ipResolverOnce sync.Once
	ipResolver     *iptrust.Resolver
)

// mustInitIPResolver 在路由初始化时调用，配置写错直接启动失败，避免带病上线。
func mustInitIPResolver() {
	ipResolverOnce.Do(func() {
		r, err := iptrust.New(config.Config.IPTrust.TrustedProxies, config.Config.IPTrust.TrustedForwarders, config.Config.ProxyHeader)
		if err != nil {
			panic(err)
		}
		if len(config.Config.IPTrust.TrustedForwarders) == 0 {
			log.ZWarn(context.Background(), "ipTrust.trustedForwarders 为空：路由服务代报的用户 IP 不会被采信，登录记录里会显示路由服务的 IP", nil)
		}
		ipResolver = r
	})
}

// resolveClientIP claimed 为请求体里代报的 ip，只有可信上游发来时才会被采信。
func resolveClientIP(c *gin.Context, claimed string) string {
	mustInitIPResolver()
	return ipResolver.ClientIP(c.Request.RemoteAddr, c.Request.Header, claimed)
}
