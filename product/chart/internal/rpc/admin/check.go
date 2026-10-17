// Copyright © 2023 OpenIM open source community. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package admin

import (
	"context"
	"fmt"
	"net"
	"strings"

	constant2 "github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/tools/log"

	"github.com/OpenIMSDK/chat/pkg/common/db/dbutil"
	"github.com/OpenIMSDK/chat/pkg/eerrs"
	"github.com/OpenIMSDK/chat/pkg/proto/admin"
)

const advancedUserIPWhitelistConfigKey = "advanced_user_ip_whitelist"

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

func (o *adminServer) CheckRegisterForbidden(ctx context.Context, req *admin.CheckRegisterForbiddenReq) (*admin.CheckRegisterForbiddenResp, error) {
	defer log.ZDebug(ctx, "return")
	forbiddens, err := o.Database.FindIPForbidden(ctx, []string{req.Ip})
	if err != nil {
		return nil, err
	}
	for _, forbidden := range forbiddens {
		if forbidden.LimitRegister {
			return nil, eerrs.ErrForbidden.Wrap()
		}
	}
	return &admin.CheckRegisterForbiddenResp{}, nil
}

func (o *adminServer) CheckLoginForbidden(ctx context.Context, req *admin.CheckLoginForbiddenReq) (*admin.CheckLoginForbiddenResp, error) {
	defer log.ZDebug(ctx, "return")
	if cancelled, err := o.Database.IsAccountCancelled(ctx, req.UserID); err != nil {
		return nil, err
	} else if cancelled {
		return nil, eerrs.ErrForbidden.Wrap("账号已申请注销")
	}
	forbiddens, err := o.Database.FindIPForbidden(ctx, []string{req.Ip})
	if err != nil {
		return nil, err
	}
	for _, forbidden := range forbiddens {
		if forbidden.LimitLogin {
			return nil, eerrs.ErrForbidden.Wrap("IP地址被禁止")
		}
	}
	if _, err := o.Database.GetLimitUserLoginIP(ctx, req.UserID, req.Ip); err != nil {
		if !dbutil.IsGormNotFound(err) {
			return nil, err
		}
		count, err := o.Database.CountLimitUserLoginIP(ctx, req.UserID)
		if err != nil {
			return nil, err
		}
		if count > 0 {
			return nil, eerrs.ErrForbidden.Wrap("用户IP被禁止")
		}
	}
	user, err := o.Chat.GetUserPublicInfo(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if user.Level == constant2.AdvancedUserLevel {
		conf, err := o.Database.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		rules := parseWhitelistRules(conf[advancedUserIPWhitelistConfigKey])
		if len(rules) > 0 {
			matched := false
			for _, rule := range rules {
				if matchIPRule(req.Ip, rule) {
					matched = true
					break
				}
			}
			if !matched {
				return nil, eerrs.ErrForbidden.Wrap("当前IP不在高级用户登录白名单中")
			}
		}
	}
	if forbiddenAccount, err := o.Database.GetBlockInfo(ctx, req.UserID); err == nil {
		return nil, eerrs.ErrForbidden.Wrap(fmt.Sprintf("账号被禁止: %s", forbiddenAccount.Reason))
	} else if !dbutil.IsGormNotFound(err) {
		return nil, err
	}
	return &admin.CheckLoginForbiddenResp{}, nil
}
