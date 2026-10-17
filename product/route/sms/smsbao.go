package sms

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type SmsbaoConfig struct {
	ApiUrl   string
	Username string
	Password string
	SignName string
}
type SmsbaoSMS struct {
	config SmsbaoConfig
	client *http.Client
}

func NewSmsbaoSMS(config SmsbaoConfig) *SmsbaoSMS { return &SmsbaoSMS{config, providerClient()} }
func (s *SmsbaoSMS) Name() string                 { return "smsbao-sms" }
func (s *SmsbaoSMS) SendCode(area, phone, code string) error {
	if err := validateMessage(phone, code); err != nil {
		return err
	}
	if normalizedArea(area) != "86" {
		return errors.New("当前短信通道不支持该区号")
	}
	if s.config.Username == "" || s.config.Password == "" || s.config.SignName == "" {
		return errors.New("短信通道凭证未配置")
	}
	endpoint := s.config.ApiUrl
	if endpoint == "" {
		endpoint = "https://api.smsbao.com/sms"
	}
	params := url.Values{"u": {s.config.Username}, "p": {s.config.Password}, "m": {phone}, "c": {fmt.Sprintf("【%s】您的验证码是：%s", s.config.SignName, code)}}
	body, err := providerGET(s.client, endpoint, params)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(body)) != "0" {
		return errors.New("短信通道发送未成功")
	}
	return nil
}
