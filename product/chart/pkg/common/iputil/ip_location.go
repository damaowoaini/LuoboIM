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

package iputil

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// IPLocation IP地址位置信息
type IPLocation struct {
	Country  string `json:"country"`
	Province string `json:"region"`
	City     string `json:"city"`
}

// GetLocationByIP 根据IP地址获取地区信息
func GetLocationByIP(ip string) string {
	// 如果是本地IP，返回"本地"
	if isLocalIP(ip) {
		return "本地"
	}

	// 使用 ipgeolocation.io 获取位置信息
	apiKey := "11546a7b01384cfda595945b5440504b"
	location := getLocationFromIPGeolocation(ip, apiKey)
	if location != "" {
		return location
	}

	// 如果API失败，返回默认值
	return "未知地区"
}

// isLocalIP 判断是否为本地IP
func isLocalIP(ip string) bool {
	if ip == "" || ip == "127.0.0.1" || ip == "::1" || ip == "localhost" {
		return true
	}

	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	// 判断是否为私有IP
	return parsedIP.IsLoopback() || parsedIP.IsPrivate()
}

// getLocationFromIPGeolocation 使用 ipgeolocation.io 获取位置
func getLocationFromIPGeolocation(ip string, apiKey string) string {
	url := fmt.Sprintf("https://api.ipgeolocation.io/ipgeo?apiKey=%s&ip=%s&lang=zh-cn", apiKey, ip)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	var result struct {
		CountryName string `json:"country_name"`
		StateProv   string `json:"state_prov"`
		City        string `json:"city"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return ""
	}

	// 构建位置字符串
	var parts []string

	// 优先显示省份和城市
	if result.StateProv != "" {
		parts = append(parts, result.StateProv)
	}
	if result.City != "" && result.City != result.StateProv {
		parts = append(parts, result.City)
	}

	// 如果省份城市都为空，显示国家
	if len(parts) == 0 && result.CountryName != "" {
		parts = append(parts, result.CountryName)
	}

	if len(parts) > 0 {
		return strings.Join(parts, " ")
	}

	return ""
}

// GetLocationByIPWithFallback 带有备用方案的IP地址获取（异步获取，不阻塞登录）
func GetLocationByIPWithFallback(ip string) string {
	// 使用channel实现超时控制
	locationChan := make(chan string, 1)

	go func() {
		locationChan <- GetLocationByIP(ip)
	}()

	select {
	case location := <-locationChan:
		return location
	case <-time.After(3 * time.Second):
		// 超时后返回默认值
		return "获取中"
	}
}
