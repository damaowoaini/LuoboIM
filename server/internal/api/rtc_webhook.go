package api

import (
	"context"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	rtcservice "github.com/openimsdk/open-im-server/v3/internal/rpc/rtc"
	"go.mongodb.org/mongo-driver/mongo"
)

func registerRTCWebhook(router *gin.Engine, db *mongo.Database) {
	// RTC credentials are shared only between API/RTC workers and the configured
	// media server. An unconfigured deployment has no unauthenticated fallback.
	if os.Getenv("LUOBO_RTC_API_KEY") == "" {
		return
	}
	media, err := rtcservice.NewLiveKitFromEnv()
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	service, err := rtcservice.New(ctx, db, nil, media, nil)
	if err != nil {
		panic(err)
	}
	router.POST("/signal/livekit_webhook", func(c *gin.Context) {
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024))
		if err != nil {
			c.Status(413)
			return
		}
		if err := media.VerifyWebhook(body, c.GetHeader("Authorization")); err != nil {
			c.Status(403)
			return
		}
		request, cancel := context.WithTimeout(c, 15*time.Second)
		defer cancel()
		if err := service.HandleWebhook(request, body, c.GetHeader("Authorization")); err != nil {
			c.Status(500)
			return
		}
		c.Status(200)
	})
}
