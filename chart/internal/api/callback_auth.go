package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strconv"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const callbackMaxAge = 5 * time.Minute

func callbackAuthentication(rdb redis.UniversalClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		reject := func() {
			c.AbortWithStatusJSON(403, gin.H{"actionCode": 1, "errCode": 20013, "errMsg": "无效的服务回调凭证"})
		}
		if config.Config.Secret == nil || *config.Config.Secret == "" {
			reject()
			return
		}
		ts := c.GetHeader("X-OpenIM-Callback-Timestamp")
		nonce := c.GetHeader("X-OpenIM-Callback-Nonce")
		signature := c.GetHeader("X-OpenIM-Callback-Signature")
		at, err := strconv.ParseInt(ts, 10, 64)
		if err != nil || time.Since(time.Unix(at, 0)) > callbackMaxAge || time.Until(time.Unix(at, 0)) > callbackMaxAge {
			reject()
			return
		}
		nonceBytes, err := hex.DecodeString(nonce)
		if err != nil || len(nonceBytes) != 16 {
			reject()
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1024*1024+1))
		if err != nil || len(body) > 1024*1024 {
			reject()
			return
		}
		command := c.Param("command")
		if len(command) > 0 && command[0] == '/' {
			command = command[1:]
		}
		mac := hmac.New(sha256.New, []byte(*config.Config.Secret))
		mac.Write([]byte(ts + "\n" + nonce + "\n" + command + "\n"))
		mac.Write(body)
		provided, err := hex.DecodeString(signature)
		if err != nil || !hmac.Equal(provided, mac.Sum(nil)) {
			reject()
			return
		}
		// A signed callback can mutate rate limits, so reject replay as well as
		// tampering. Keep the nonce through the entire accepted clock window.
		claimed, err := rdb.SetNX(c, "CALLBACK_NONCE:"+nonce, "1", 2*callbackMaxAge).Result()
		if err != nil || !claimed {
			reject()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
	}
}
