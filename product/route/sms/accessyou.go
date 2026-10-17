package sms

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
)

type AccessyouConfig struct {
	ApiUrl     string
	AccountNo  string
	Username   string
	Password   string
	TemplateId string
}
type AccessyouSMS struct {
	config AccessyouConfig
	client *http.Client
}

func NewAccessyouSMS(config AccessyouConfig) *AccessyouSMS {
	return &AccessyouSMS{config, providerClient()}
}
func (s *AccessyouSMS) Name() string { return "accessyou-sms" }

type accessyouResponse struct {
	MsgStatus     string `json:"msg_status"`
	MsgStatusDesc string `json:"msg_status_desc"`
	MsgID         int64  `json:"msg_id"`
	PhoneNo       string `json:"phoneno"`
}

func (s *AccessyouSMS) SendCode(area, phone, code string) error {
	if err := validateMessage(phone, code); err != nil {
		return err
	}
	if s.config.AccountNo == "" || s.config.Username == "" || s.config.Password == "" || s.config.TemplateId == "" {
		return errors.New("短信通道凭证未配置")
	}
	endpoint := s.config.ApiUrl
	if endpoint == "" {
		endpoint = "https://otp.accessyou-api.com/sendsms-otp.php"
	}
	params := url.Values{"accountno": {s.config.AccountNo}, "user": {s.config.Username}, "pwd": {s.config.Password}, "tid": {s.config.TemplateId}, "a": {code}, "phone": {normalizedArea(area) + phone}}
	body, err := providerGET(s.client, endpoint, params)
	if err != nil {
		return err
	}
	body = bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF})
	var result accessyouResponse
	if json.Unmarshal(body, &result) != nil {
		return errors.New("短信通道响应格式错误")
	}
	if result.MsgStatus != "100" {
		return errors.New("短信通道发送未成功")
	}
	return nil
}
