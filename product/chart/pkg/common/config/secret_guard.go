package config

import (
	"fmt"
	"strings"
)

// 启动前拒绝弱密钥：占位符、开源默认值、过短。secret 能给任意用户签发 token，必须足够强。
var weakSecrets = map[string]bool{
	"": true, "change_me": true, "changeme": true, "openim123": true,
	"secret": true, "123456": true, "openimsecret": true,
}

const minSecretLen = 16

func checkSecretStrength(name, v string) error {
	t := strings.TrimSpace(v)
	if weakSecrets[strings.ToLower(t)] || len(t) < minSecretLen {
		return fmt.Errorf("配置项 %s 不安全（为空、占位符、默认值或少于 %d 位），请换成随机强密钥后再启动", name, minSecretLen)
	}
	return nil
}

func checkSecrets() error {
	if Config.Secret == nil {
		return fmt.Errorf("配置项 secret 未设置")
	}
	if err := checkSecretStrength("secret", *Config.Secret); err != nil {
		return err
	}
	return checkSecretStrength("chatSecret", Config.ChatSecret)
}
