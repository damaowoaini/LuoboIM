package sms

import (
	"errors"
	"fmt"
	"strings"
)

// SMS 短信发送通道
type SMS interface {
	Name() string
	SendCode(areaCode string, phoneNumber string, verifyCode string) error
}

// Config 短信通道配置，Use 决定实际启用哪一个
type Config struct {
	Use       string
	Smsbao    SmsbaoConfig
	Accessyou AccessyouConfig
}

// New 按 Use 选择短信通道，空字符串禁用短信发送；公开认证不接受超级验证码
func New(config Config) (SMS, error) {
	switch strings.ToLower(config.Use) {
	case "":
		return emptySMS{}, nil
	case "smsbao":
		return NewSmsbaoSMS(config.Smsbao), nil
	case "accessyou":
		return NewAccessyouSMS(config.Accessyou), nil
	default:
		return nil, fmt.Errorf("不支持的短信通道: %s", config.Use)
	}
}

// emptySMS 空实现，用于关闭短信发送
type emptySMS struct{}

func (emptySMS) Name() string {
	return "empty-sms"
}

func (emptySMS) SendCode(areaCode string, phoneNumber string, verifyCode string) error {
	return errors.New("短信通道未启用")
}
