package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"openim-route-service/model"
	"strings"
	"time"
)

type RouteResolver interface {
	GetServerByInviteCode(string) (*model.RouteResponse, error)
	GetServerByUserID(string) (*model.RouteResponse, error)
	BindUserToServer(string, string, string) error
	GetAllServers() ([]model.ServerConfig, error)
}
type Forwarder interface {
	Post(context.Context, *model.RouteResponse, string, any, string, string) (map[string]interface{}, error)
}
type HTTPForwarder struct {
	Client  *http.Client
	Secrets map[string]string
}

func NewHTTPForwarder(keys ...map[string]string) *HTTPForwarder {
	var secrets map[string]string
	if len(keys) > 0 {
		secrets = keys[0]
	}
	return &HTTPForwarder{Secrets: secrets, Client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("upstream redirects are disabled") }}}
}
func domainURL(domain, path string) (string, error) {
	domain = strings.TrimSpace(domain)
	domain = strings.TrimPrefix(strings.TrimPrefix(domain, "https://"), "http://")
	domain = strings.TrimSuffix(domain, "/")
	u, err := url.Parse("https://" + domain)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(domain, "\\\r\n\t ") {
		return "", errors.New("服务器域名配置错误")
	}
	if port := u.Port(); port != "" && port != "443" {
		return "", errors.New("生产服务器仅允许HTTPS 443端口")
	}
	return u.String() + path, nil
}
func (f *HTTPForwarder) Post(ctx context.Context, server *model.RouteResponse, path string, payload any, ip, operationID string) (map[string]interface{}, error) {
	if server == nil || len(server.Domains) == 0 {
		return nil, errors.New("服务器线路不可用")
	}
	endpoint, err := domainURL(server.Domains[0], path)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("请求序列化失败")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("创建上游请求失败")
	}
	if id, _ := ctx.Value(registrationContextKey{}).(string); id != "" {
		if err := f.signRegistration(req, body, server.ServerID, id); err != nil {
			return nil, err
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Real-IP", ip)
	req.Header.Set("X-Forwarded-For", ip)
	if operationID == "" {
		operationID = fmt.Sprint(time.Now().UnixMilli())
	}
	req.Header.Set("operationID", operationID)
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, errors.New("目标服务器暂时不可用")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("目标服务器响应异常")
	}
	const maxResponse = 2 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse {
		return nil, errors.New("目标服务器响应异常")
	}
	var result map[string]interface{}
	if json.Unmarshal(raw, &result) != nil {
		return nil, errors.New("目标服务器响应格式错误")
	}
	if _, ok := result["errCode"].(float64); !ok {
		return nil, errors.New("目标服务器响应缺少状态码")
	}
	return result, nil
}
func successful(result map[string]interface{}) bool {
	code, ok := result["errCode"].(float64)
	return ok && code == 0
}
func augmentServer(result map[string]interface{}, server *model.RouteResponse) error {
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return errors.New("目标服务器认证响应不完整")
	}
	data["serverInfo"] = map[string]interface{}{"serverID": server.ServerID, "serverName": server.ServerName, "domains": server.Domains}
	return nil
}
