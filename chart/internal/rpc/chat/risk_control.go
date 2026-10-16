package chat

import (
	"context"
	"net"
	"strings"

	constant2 "github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/eerrs"
)

const (
	advancedUserIPWhitelistConfigKey = "advanced_user_ip_whitelist"
)

func parseWhitelistRules(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';'
	})
	rules := make([]string, 0, len(fields))
	for _, field := range fields {
		rule := strings.TrimSpace(field)
		if rule != "" {
			rules = append(rules, rule)
		}
	}
	return rules
}

func matchIPRule(ip string, rule string) bool {
	clientIP := net.ParseIP(strings.TrimSpace(ip))
	if clientIP == nil {
		return false
	}
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return false
	}
	if strings.Contains(rule, "/") {
		_, ipNet, err := net.ParseCIDR(rule)
		return err == nil && ipNet != nil && ipNet.Contains(clientIP)
	}
	ruleIP := net.ParseIP(rule)
	return ruleIP != nil && ruleIP.Equal(clientIP)
}

func (o *chatSvr) checkAdvancedUserLoginIPWhitelist(ctx context.Context, userID string, ip string) error {
	user, err := o.Database.GetAttribute(ctx, userID)
	if err != nil {
		return err
	}
	if user.Level != constant2.AdvancedUserLevel {
		return nil
	}
	conf, err := o.Admin.GetConfig(ctx)
	if err != nil {
		return err
	}
	rules := parseWhitelistRules(conf[advancedUserIPWhitelistConfigKey])
	if len(rules) == 0 {
		return nil
	}
	for _, rule := range rules {
		if matchIPRule(ip, rule) {
			return nil
		}
	}
	return eerrs.ErrForbidden.Wrap("当前IP不在高级用户登录白名单中")
}
