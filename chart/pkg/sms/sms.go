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

package sms

import (
	"context"
	"fmt"
	"strings"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/tools/errs"
)

func New() (SMS, error) {
	switch strings.ToLower(config.Config.VerifyCode.Use) {
	case "":
		return empty{}, nil
	case "mail":
		return mailOnly{}, nil
	case "ali":
		return newAli()
	case "smsbao":
		return newSmsbao()
	case "accessyou":
		return newAccessyou()
	default:
		return nil, fmt.Errorf("not support sms: %s", config.Config.VerifyCode.Use)
	}
}

type SMS interface {
	Name() string
	SendCode(ctx context.Context, areaCode string, phoneNumber string, verifyCode string) error
}

type empty struct{}

func (e empty) Name() string {
	return "empty-sms"
}

func (e empty) SendCode(ctx context.Context, areaCode string, phoneNumber string, verifyCode string) error {
	return errs.ErrNoPermission.Wrap("短信验证码发送服务未配置")
}

// Mail mode uses the independent email transport. It must still construct a
// chat service successfully, while refusing phone delivery explicitly.
type mailOnly struct{}

func (mailOnly) Name() string { return "mail-only" }

func (mailOnly) SendCode(context.Context, string, string, string) error {
	return errs.ErrNoPermission.Wrap("当前服务仅支持邮箱验证码")
}
