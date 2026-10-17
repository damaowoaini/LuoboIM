package config

import (
	"fmt"
	"strings"
)

// 启动前拒绝弱 secret：它能给任意用户（含管理员）签发 token。
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
