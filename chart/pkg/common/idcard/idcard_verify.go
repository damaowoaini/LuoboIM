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

package idcard

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/OpenIMSDK/tools/errs"
)

// IDCardVerifyConfig 身份证验证配置
type IDCardVerifyConfig struct {
	APIUrl string
	APIKey string
}

// IDCardVerifyResponse 身份证验证响应
type IDCardVerifyResponse struct {
	ErrorCode int    `json:"error_code"`
	Reason    string `json:"reason"`
	Result    struct {
		RealName string `json:"realname"`
		IDCard   string `json:"idcard"`
		Res      int    `json:"res"` // 1：匹配，2：不匹配
	} `json:"result"`
}

// IDCardVerifier 身份证验证器
type IDCardVerifier struct {
	config IDCardVerifyConfig
	client *http.Client
}

// NewIDCardVerifier 创建身份证验证器
func NewIDCardVerifier(config IDCardVerifyConfig) *IDCardVerifier {
	return &IDCardVerifier{
		config: config,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Verify 验证身份证信息
func (v *IDCardVerifier) Verify(realName, idCard string) (bool, error) {
	if v.config.APIUrl == "" || v.config.APIKey == "" {
		return false, errs.ErrInternalServer.Wrap("实名认证服务尚未配置")
	}
	endpoint, err := url.Parse(v.config.APIUrl)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
		return false, errs.ErrInternalServer.Wrap("实名认证服务需要有效的 HTTPS 地址")
	}

	params := endpoint.Query()
	params.Set("key", v.config.APIKey)
	params.Set("idcard", idCard)
	params.Set("realname", realName)

	endpoint.RawQuery = params.Encode()
	resp, err := v.client.Get(endpoint.String())
	if err != nil {
		// net/url errors include the request URL, which contains identity data
		// and the provider API key. Never return that URL into API logs.
		return false, errs.ErrInternalServer.Wrap("实名认证服务请求失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, errs.ErrInternalServer.Wrap("实名认证服务响应异常")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return false, errs.Wrap(err, "failed to read response body")
	}

	var result IDCardVerifyResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return false, errs.Wrap(err, "failed to parse response")
	}

	if result.ErrorCode != 0 {
		return false, errs.ErrInternalServer.Wrap("实名认证服务未能完成验证")
	}

	return result.Result.Res == 1, nil
}

// ValidateIDCardFormat 验证身份证号格式
func ValidateIDCardFormat(idCard string) bool {
	if len(idCard) != 18 {
		return false
	}
	for i := 0; i < 17; i++ {
		if idCard[i] < '0' || idCard[i] > '9' {
			return false
		}
	}
	lastChar := idCard[17]
	return (lastChar >= '0' && lastChar <= '9') || lastChar == 'X' || lastChar == 'x'
}
