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
	"io"
	"net/http"
	"net/url"

	"github.com/OpenIMSDK/tools/errs"

	"github.com/OpenIMSDK/chat/pkg/common/config"
)

func newSmsbao() (SMS, error) {
	return &smsbao{
		apiUrl:   config.Config.VerifyCode.Smsbao.ApiUrl,
		username: config.Config.VerifyCode.Smsbao.Username,
		password: config.Config.VerifyCode.Smsbao.Password,
		signName: config.Config.VerifyCode.Smsbao.SignName,
	}, nil
}

type smsbao struct {
	apiUrl   string
	username string
	password string
	signName string
}

func (s *smsbao) Name() string {
	return "smsbao-sms"
}

func (s *smsbao) SendCode(ctx context.Context, areaCode string, phoneNumber string, verifyCode string) error {
	// 短信宝API地址
	apiUrl := s.apiUrl
	if apiUrl == "" {
		apiUrl = "https://api.smsbao.com/sms" // 默认短信宝API地址
	}

	// 构建短信内容
	content := fmt.Sprintf("【%s】您的验证码是：%s", s.signName, verifyCode)

	// 构建请求参数
	params := url.Values{}
	params.Set("u", s.username)     // 短信宝用户名
	params.Set("p", s.password)     // 短信宝密码（建议使用MD5加密后的密码）
	params.Set("m", phoneNumber)    // 手机号
	params.Set("c", content)        // 短信内容

	// 发送HTTP GET请求
	fullUrl := fmt.Sprintf("%s?%s", apiUrl, params.Encode())
	resp, err := http.Get(fullUrl)
	if err != nil {
		return errs.Wrap(err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errs.Wrap(err)
	}

	// 短信宝返回码说明：
	// 0	发送成功
	// 30	密码错误
	// 40	账号不存在
	// 41	余额不足
	// 42	账户已过期
	// 43	IP地址限制
	// 50	内容含有敏感词
	// 51	手机号码不正确
	statusCode := string(body)
	if statusCode != "0" {
		return errs.Wrap(fmt.Errorf("短信宝发送失败，错误码: %s", statusCode))
	}

	return nil
}

