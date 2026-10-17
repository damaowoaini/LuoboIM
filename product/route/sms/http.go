package sms

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var numericPhone = regexp.MustCompile(`^[0-9]{6,24}$`)
var numericCode = regexp.MustCompile(`^[0-9]{4,10}$`)

func providerClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("SMS redirects disabled") }}
}
func providerGET(client *http.Client, endpoint string, params url.Values) ([]byte, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("短信通道必须配置HTTPS地址")
	}
	u.RawQuery = params.Encode()
	resp, err := client.Get(u.String())
	if err != nil {
		return nil, errors.New("短信通道请求失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("短信通道返回异常状态")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		return nil, errors.New("短信通道响应异常")
	}
	return body, nil
}
func validateMessage(phone, code string) error {
	if !numericPhone.MatchString(phone) || !numericCode.MatchString(code) {
		return errors.New("短信参数格式错误")
	}
	return nil
}
func normalizedArea(area string) string {
	if area == "" {
		return "86"
	}
	return strings.TrimPrefix(area, "+")
}
