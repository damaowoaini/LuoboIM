package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"openim-route-service/api"
	"openim-route-service/db"
	"openim-route-service/service"
)

type Config struct {
	Address       string
	MySQL         db.MySQLConfig
	Router        api.RouterConfig
	RedisAddress  string
	RedisPassword string
	RedisDB       int
}

func envInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return n, nil
}
func envString(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
func listEnv(name string) []string {
	var result []string
	for _, v := range strings.Split(os.Getenv(name), ",") {
		if s := strings.TrimSpace(v); s != "" {
			result = append(result, s)
		}
	}
	return result
}
func loadConfig() (*Config, error) {
	port, err := envInt("ROUTE_PORT", 10010)
	if err != nil {
		return nil, err
	}
	if port < 1 || port > 65535 {
		return nil, errors.New("ROUTE_PORT must be between 1 and 65535")
	}
	listenHost := os.Getenv("ROUTE_LISTEN_HOST")
	if listenHost != "" && listenHost != "localhost" && net.ParseIP(listenHost) == nil {
		return nil, errors.New("ROUTE_LISTEN_HOST must be an IP address, localhost, or empty")
	}
	mysqlPort, err := envInt("ROUTE_MYSQL_PORT", 3306)
	if err != nil {
		return nil, err
	}
	redisDB, err := envInt("ROUTE_REDIS_DB", 0)
	if err != nil {
		return nil, err
	}
	authLimit, err := envInt("ROUTE_AUTH_PER_MINUTE", 60)
	if err != nil {
		return nil, err
	}
	verifyLimit, err := envInt("ROUTE_VERIFY_PER_MINUTE", 6)
	if err != nil {
		return nil, err
	}
	adminToken := os.Getenv("ROUTE_ADMIN_TOKEN")
	if len(adminToken) < 32 {
		return nil, errors.New("ROUTE_ADMIN_TOKEN must contain at least 32 characters")
	}
	password := os.Getenv("ROUTE_MYSQL_PASSWORD")
	if password == "" {
		return nil, errors.New("ROUTE_MYSQL_PASSWORD is required")
	}
	secrets, err := service.ParseUpstreamSecrets(os.Getenv("ROUTE_UPSTREAM_SECRETS"), os.Getenv("LUOBO_ROUTE_SHARED_SECRET"))
	if err != nil {
		return nil, err
	}
	origins := listEnv("ROUTE_ALLOWED_ORIGINS")
	for _, origin := range origins {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("ROUTE_ALLOWED_ORIGINS must contain exact HTTPS origins")
		}
	}
	return &Config{Address: net.JoinHostPort(listenHost, strconv.Itoa(port)), MySQL: db.MySQLConfig{Host: envString("ROUTE_MYSQL_HOST", "127.0.0.1"), Port: mysqlPort, Username: envString("ROUTE_MYSQL_USER", "luobo_route"), Password: password, Database: envString("ROUTE_MYSQL_DATABASE", "openim_route"), MaxOpenConn: 20, MaxIdleConn: 5, MaxLifeTime: 300}, Router: api.RouterConfig{UpstreamSecrets: secrets, AdminToken: adminToken, TrustedProxies: listEnv("ROUTE_TRUSTED_PROXIES"), AllowedOrigins: origins, AuthRequestsPerMinute: authLimit, VerifyRequestsPerMinute: verifyLimit}, RedisAddress: os.Getenv("ROUTE_REDIS_ADDRESS"), RedisPassword: os.Getenv("ROUTE_REDIS_PASSWORD"), RedisDB: redisDB}, nil
}
