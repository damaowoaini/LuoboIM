package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/serviceauth"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"io"
	"net/http"
	"time"
)

func (a *AdminApi) validateDelegation(rdb redis.UniversalClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		deny := func() { apiresp.GinError(c, errs.ErrNoPermission.Wrap("管理能力验证失败")); c.Abort() }
		if c.Request.URL.RawPath != "" || c.Request.URL.RawQuery != "" || c.Request.URL.Path != "/internal/admin_delegation/validate" {
			deny()
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1024))
		if err != nil {
			deny()
			return
		}
		token := c.GetHeader("token")
		key, err := serviceauth.Verify([]byte(*config.Config.Secret), serviceauth.Validation, http.MethodPost, c.Request.URL.Path, token, body, c.Request.Header, time.Now())
		if err != nil {
			deny()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 1500*time.Millisecond)
		defer cancel()
		ok, err := rdb.SetNX(ctx, key, "1", serviceauth.ReplayTTL).Result()
		if err != nil || !ok {
			deny()
			return
		}
		var req struct {
			Method   string `json:"method"`
			Path     string `json:"path"`
			BodyHash string `json:"bodyHash"`
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&req) != nil || req.Method != "POST" || !serviceauth.CanonicalPath(req.Path) || len(req.Path) > 128 {
			deny()
			return
		}
		decoded, e := hex.DecodeString(req.BodyHash)
		if e != nil || len(decoded) != 32 {
			deny()
			return
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			deny()
			return
		}
		actor, level, err := a.delegation.Validate(ctx, token)
		if err != nil {
			deny()
			return
		}
		if a.delegation.Audit(ctx, token, actor, req.Method, req.Path, req.BodyHash) != nil {
			deny()
			return
		}
		apiresp.GinSuccess(c, gin.H{"adminID": actor, "level": level})
	}
}

func (a *AdminApi) AdminLogout(c *gin.Context) {
	if a.delegation == nil || strictJSON(c, &struct{}{}, 1024) != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("注销请求无效"))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 1500*time.Millisecond)
	defer cancel()
	if a.delegation.RevokeSession(ctx, c.GetHeader("token")) != nil {
		apiresp.GinError(c, errs.ErrNoPermission.Wrap("管理员会话撤销未确认"))
		return
	}
	apiresp.GinSuccess(c, gin.H{"revoked": true})
}
