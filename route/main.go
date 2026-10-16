package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"openim-route-service/api"
	"openim-route-service/cache"
	"openim-route-service/db"
	"openim-route-service/model"
)

func main() {
	migrate := flag.Bool("migrate", false, "create missing route tables; take a database backup first")
	check := flag.Bool("check-config", false, "validate environment configuration and exit")
	flag.Parse()
	config, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	if *check {
		log.Print("route configuration is valid")
		return
	}
	if err := db.InitMySQL(config.MySQL); err != nil {
		log.Fatal("route database initialization failed")
	}
	defer db.Close()
	if *migrate {
		if err := db.DB.AutoMigrate(&model.ServerConfig{}, &model.InviteCode{}, &model.UserServerBinding{}, &model.RegistrationIntent{}, &model.AccountRouteLock{}, &model.AdminProvisionReceipt{}, &model.AdminProvisionOwner{}, &model.AdminProvisionNonce{}); err != nil {
			log.Fatal("route schema initialization failed")
		}
		log.Print("route schema initialization completed")
		return
	}
	// Retain optional Redis connectivity for existing operations. Public OTPs
	// are stored and consumed only in chart, never in this Redis namespace.
	if config.RedisAddress != "" {
		if err := cache.InitRedis(cache.RedisConfig{Address: config.RedisAddress, Password: config.RedisPassword, DB: config.RedisDB}); err != nil {
			log.Fatal("route Redis initialization failed")
		}
		defer cache.Close()
	}
	gin.SetMode(gin.ReleaseMode)
	server := &http.Server{Addr: config.Address, Handler: api.SetupRouterWithConfig(config.Router), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("route listener failed")
		}
	}()
	log.Printf("萝卜对话 route service listening on %s", config.Address)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Print("route graceful shutdown timed out")
	}
}
