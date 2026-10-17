package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"openim-route-service/model"
	"openim-route-service/service"
)

type LoginHandler interface {
	Login(context.Context, *service.LoginRequest, string, string) (map[string]interface{}, error)
}
type RegisterHandler interface {
	Register(context.Context, *service.RegisterRequest, string, string) (map[string]interface{}, error)
}
type PasswordHandler interface {
	ResetPassword(context.Context, *service.ResetPasswordRequest, string, string) (map[string]interface{}, error)
}
type VerifyHandler interface {
	Verify(context.Context, *service.VerifyRequest, bool, string, string) (map[string]interface{}, error)
}
type Dependencies struct {
	Route    service.RouteResolver
	Forward  service.Forwarder
	Login    LoginHandler
	Register RegisterHandler
	Password PasswordHandler
	Verify   VerifyHandler
}
type RouterConfig struct {
	UpstreamSecrets         map[string]string
	AdminToken              string
	TrustedProxies          []string
	AllowedOrigins          []string
	AuthRequestsPerMinute   int
	VerifyRequestsPerMinute int
}

func SetupRouter(_ service.VerifyConfig) *gin.Engine { return SetupRouterWithConfig(RouterConfig{}) }
func SetupRouterWithConfig(config RouterConfig) *gin.Engine {
	register := service.NewRegisterServiceWithSecrets(config.UpstreamSecrets)
	return SetupRouterWithDependencies(config, Dependencies{Route: service.NewRouteService(), Forward: service.NewHTTPForwarder(), Login: service.NewLoginService(), Register: register, Password: service.NewPasswordService(), Verify: service.NewVerifyService(service.VerifyConfig{})})
}
func envelope(c *gin.Context, code int, detail string, data any) {
	c.JSON(http.StatusOK, gin.H{"errCode": code, "errMsg": "", "errDlt": detail, "data": data})
}
func respond(c *gin.Context, result map[string]interface{}, err error) {
	if err != nil {
		code := 500
		if errors.Is(err, service.ErrCredentials) {
			code = 20001
		}
		var data any
		// These states are decided by signed receipt recovery and local binding,
		// never by an upstream boolean or a substring of a translated message.
		if errors.Is(err, service.ErrRegistrationPending) {
			data = gin.H{"registrationPending": true}
		}
		if errors.Is(err, service.ErrRegistrationRecovered) {
			data = gin.H{"registrationRecovered": true, "requiresLogin": true}
		}
		envelope(c, code, err.Error(), data)
		return
	}
	c.JSON(http.StatusOK, result)
}
func decode(c *gin.Context, v any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if err := c.ShouldBindJSON(v); err != nil {
		envelope(c, 400, "参数错误", nil)
		return false
	}
	return true
}
func SetupRouterWithDependencies(config RouterConfig, deps Dependencies) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	// No proxy is trusted by default. Gin otherwise trusts every proxy.
	if err := r.SetTrustedProxies(config.TrustedProxies); err != nil {
		panic("invalid trusted proxy configuration")
	}
	r.Use(corsMiddleware(config.AllowedOrigins))
	if config.AuthRequestsPerMinute <= 0 {
		config.AuthRequestsPerMinute = 60
	}
	if config.VerifyRequestsPerMinute <= 0 {
		config.VerifyRequestsPerMinute = 6
	}
	authLimit := newLimiter(config.AuthRequestsPerMinute)
	verifyLimit := newLimiter(config.VerifyRequestsPerMinute)
	api := r.Group("/api")
	registerAdminProvisioning(api, config.UpstreamSecrets)
	api.GET("/health", func(c *gin.Context) { envelope(c, 0, "", gin.H{"status": "ok"}) })
	verify := api.Group("/verify", verifyLimit.middleware())
	for _, item := range []struct {
		path  string
		check bool
	}{{"/send", false}, {"/check", true}} {
		check := item.check
		verify.POST(item.path, func(c *gin.Context) {
			var req service.VerifyRequest
			if !decode(c, &req) {
				return
			}
			result, err := deps.Verify.Verify(c.Request.Context(), &req, check, c.ClientIP(), c.GetHeader("operationID"))
			respond(c, result, err)
		})
	}
	auth := api.Group("/auth", authLimit.middleware())
	auth.POST("/login", func(c *gin.Context) {
		var req service.LoginRequest
		if !decode(c, &req) {
			return
		}
		result, err := deps.Login.Login(c.Request.Context(), &req, c.ClientIP(), c.GetHeader("operationID"))
		respond(c, result, err)
	})
	auth.POST("/register", func(c *gin.Context) {
		var req service.RegisterRequest
		if !decode(c, &req) {
			return
		}
		result, err := deps.Register.Register(c.Request.Context(), &req, c.ClientIP(), c.GetHeader("operationID"))
		respond(c, result, err)
	})
	auth.POST("/reset-password", func(c *gin.Context) {
		var req service.ResetPasswordRequest
		if !decode(c, &req) {
			return
		}
		result, err := deps.Password.ResetPassword(c.Request.Context(), &req, c.ClientIP(), c.GetHeader("operationID"))
		respond(c, result, err)
	})
	api.POST("/client_config/get", authLimit.middleware(), func(c *gin.Context) {
		var req struct {
			InvitationCode string `json:"invitationCode"`
		}
		if !decode(c, &req) {
			return
		}
		server, err := deps.Route.GetServerByInviteCode(req.InvitationCode)
		if err != nil {
			respond(c, nil, err)
			return
		}
		result, err := deps.Forward.Post(c.Request.Context(), server, "/chat/client_config/get", map[string]interface{}{}, c.ClientIP(), c.GetHeader("operationID"))
		respond(c, result, err)
	})
	route := api.Group("/route")
	route.GET("/server", authLimit.middleware(), func(c *gin.Context) {
		var server *model.RouteResponse
		var err error
		if code := c.Query("code"); code != "" {
			server, err = deps.Route.GetServerByInviteCode(code)
		} else if userID := c.Query("user_id"); userID != "" {
			if !requireAdmin(c, config.AdminToken) {
				return
			}
			server, err = deps.Route.GetServerByUserID(userID)
		} else {
			envelope(c, 400, "请提供邀请码或用户ID", nil)
			return
		}
		if err != nil {
			respond(c, nil, err)
			return
		}
		envelope(c, 0, "", server)
	})
	route.POST("/bind", func(c *gin.Context) {
		if !requireAdmin(c, config.AdminToken) {
			return
		}
		var req struct {
			UserID     string `json:"user_id" binding:"required"`
			ServerID   string `json:"server_id" binding:"required"`
			InviteCode string `json:"invite_code"`
		}
		if !decode(c, &req) {
			return
		}
		if len(req.UserID) > 50 || len(req.ServerID) > 50 || len(req.InviteCode) > 50 {
			envelope(c, 400, "参数过长", nil)
			return
		}
		if err := deps.Route.BindUserToServer(req.UserID, req.ServerID, req.InviteCode); err != nil {
			respond(c, nil, err)
			return
		}
		envelope(c, 0, "", nil)
	})
	route.GET("/servers", func(c *gin.Context) {
		if !requireAdmin(c, config.AdminToken) {
			return
		}
		servers, err := deps.Route.GetAllServers()
		if err != nil {
			respond(c, nil, err)
			return
		}
		envelope(c, 0, "", servers)
	})
	return r
}
func requireAdmin(c *gin.Context, expected string) bool {
	actual := c.GetHeader("X-Route-Admin-Token")
	if actual == "" {
		actual = strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	}
	if len(expected) < 32 || subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"errCode": 401, "errMsg": "unauthorized", "errDlt": "需要路由管理凭证"})
		return false
	}
	return true
}
func corsMiddleware(origins []string) gin.HandlerFunc {
	allowed := map[string]bool{}
	for _, origin := range origins {
		if origin != "" && origin != "*" {
			allowed[origin] = true
		}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && allowed[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, token, operationID, X-Route-Admin-Token")
			c.Header("Access-Control-Allow-Methods", "POST, OPTIONS, GET")
		}
		if c.Request.Method == http.MethodOptions {
			if origin != "" && !allowed[origin] {
				c.AbortWithStatus(http.StatusForbidden)
			} else {
				c.AbortWithStatus(http.StatusNoContent)
			}
			return
		}
		c.Next()
	}
}

type limitEntry struct {
	start time.Time
	count int
}
type limiter struct {
	mu      sync.Mutex
	entries map[string]limitEntry
	limit   int
}

func newLimiter(n int) *limiter { return &limiter{entries: make(map[string]limitEntry), limit: n} }
func (l *limiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, v := range l.entries {
		if now.Sub(v.start) >= time.Minute {
			delete(l.entries, key)
		}
	}
	entry := l.entries[ip]
	if entry.start.IsZero() {
		if len(l.entries) >= 10000 {
			return false
		}
		entry.start = now
	}
	entry.count++
	l.entries[ip] = entry
	return entry.count <= l.limit
}
func (l *limiter) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !l.allow(c.ClientIP(), time.Now()) {
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"errCode": 429, "errMsg": "rate_limited", "errDlt": "操作频繁，请稍后重试"})
			return
		}
		c.Next()
	}
}
