// Copyright © 2023 OpenIM. All rights reserved.
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

package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

const (
	// 默认配置
	DefaultPeriod = 30 // 30秒一个周期
	DefaultDigits = 6  // 6位数字
)

// GenerateSecret 生成随机密钥
func GenerateSecret() string {
	// 生成20字节随机密钥
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		panic("cannot generate authentication secret: " + err.Error())
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
}

// GenerateQRCodeURL 生成二维码 URL
// issuer: 发行者名称（如：OpenIM）
// accountName: 账户名称（如：用户ID或邮箱）
// secret: 密钥
func GenerateQRCodeURL(issuer, accountName, secret string) string {
	values := url.Values{"secret": {secret}, "issuer": {issuer}}
	return "otpauth://totp/" + url.PathEscape(issuer+":"+accountName) + "?" + values.Encode()
}

// ValidateCode 验证 TOTP 码
// secret: 密钥
// code: 用户输入的6位数字
// allowedDrift: 允许的时间漂移（前后几个周期），默认1表示允许前后各1个周期
func ValidateCode(secret, code string, allowedDrift int) bool {
	if len(code) != DefaultDigits {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	if allowedDrift < 0 {
		allowedDrift = 1
	}

	// 标准化密钥
	secret = strings.ToUpper(strings.ReplaceAll(secret, " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) == 0 {
		return false
	}

	// 当前时间戳
	now := time.Now().Unix()

	// 检查当前时间及前后漂移范围内的时间
	for i := -allowedDrift; i <= allowedDrift; i++ {
		timestamp := now + int64(i*DefaultPeriod)
		if generateCode(secret, timestamp) == code {
			return true
		}
	}

	return false
}

// generateCode 生成指定时间戳的 TOTP 码
func generateCode(secret string, timestamp int64) string {
	// 解码密钥
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return ""
	}

	// 计算时间计数器
	counter := uint64(math.Floor(float64(timestamp) / float64(DefaultPeriod)))

	// 将计数器转换为字节数组
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	// HMAC-SHA1
	h := hmac.New(sha1.New, key)
	h.Write(buf)
	hash := h.Sum(nil)

	// 动态截断
	offset := hash[len(hash)-1] & 0x0F
	truncated := binary.BigEndian.Uint32(hash[offset:offset+4]) & 0x7FFFFFFF

	// 生成6位数字
	code := truncated % uint32(math.Pow10(DefaultDigits))

	return fmt.Sprintf("%0*d", DefaultDigits, code)
}

// GetCurrentCode 获取当前时间的 TOTP 码（用于测试）
func GetCurrentCode(secret string) string {
	return generateCode(secret, time.Now().Unix())
}
