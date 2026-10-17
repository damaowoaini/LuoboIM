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

package config

import _ "embed"

var (
	//go:embed version
	Version string
	//go:embed template.xlsx
	ImportTemplate []byte
)

var Config struct {
	AdminProvisioning struct {
		Enabled           bool   `yaml:"enabled"`
		ServerID          string `yaml:"serverID"`
		RouteURL          string `yaml:"routeURL"`
		AllowLoopbackHTTP bool   `yaml:"allowLoopbackHTTP"`
	} `yaml:"adminProvisioning"`
	AdminDelegation struct {
		Issuer string `yaml:"issuer"`
	} `yaml:"adminDelegation"`
	Envs struct {
		Discovery string `yaml:"discovery"`
	} `yaml:"envs"`
	Zookeeper struct {
		Schema   string   `yaml:"schema"`
		ZkAddr   []string `yaml:"zkAddr"`
		Username string   `yaml:"username"`
		Password string   `yaml:"password"`
	} `yaml:"zookeeper"`
	ChatApi struct {
		GinPort  []int  `yaml:"openImChatApiPort"`
		ListenIP string `yaml:"listenIP"`
	} `yaml:"chatApi"`
	AdminApi struct {
		GinPort  []int  `yaml:"openImAdminApiPort"`
		ListenIP string `yaml:"listenIP"`
	} `yaml:"adminApi"`
	Rpc struct {
		RegisterIP string `yaml:"registerIP"`
		ListenIP   string `yaml:"listenIP"`
	} `yaml:"rpc"`
	Redis struct {
		Address  *[]string `yaml:"address"`
		Username string    `yaml:"username"`
		Password string    `yaml:"password"`
	} `yaml:"redis"`
	RpcPort struct {
		OpenImAdminPort  []int `yaml:"openImAdminPort"`
		OpenImChatPort   []int `yaml:"openImChatPort"`
		OpenImOfficePort []int `yaml:"openImOfficePort"`
	} `yaml:"rpcPort"`
	RpcRegisterName struct {
		OpenImAdminName  string  `yaml:"openImAdminName"`
		OpenImChatName   string  `yaml:"openImChatName"`
		OpenImOfficeName string  `yaml:"openImOfficeName"`
		OpenImRtcName    *string `yaml:"openImRtcName"`
	} `yaml:"rpcRegisterName"`
	Mysql struct {
		Address       *[]string `yaml:"address"`
		Username      *string   `yaml:"username"`
		Password      *string   `yaml:"password"`
		Database      *string   `yaml:"database"`
		MaxOpenConn   *int      `yaml:"maxOpenConn"`
		MaxIdleConn   *int      `yaml:"maxIdleConn"`
		MaxLifeTime   *int      `yaml:"maxLifeTime"`
		LogLevel      *int      `yaml:"logLevel"`
		SlowThreshold *int      `yaml:"slowThreshold"`
	} `yaml:"mysql"`
	Mongo struct {
		Uri         *string   `yaml:"uri"`
		Address     *[]string `yaml:"address"`
		Database    *string   `yaml:"database"`
		Username    *string   `yaml:"username"`
		Password    *string   `yaml:"password"`
		MaxPoolSize *int      `yaml:"maxPoolSize"`
	} `yaml:"mongo"`
	Log struct {
		StorageLocation     *string `yaml:"storageLocation"`
		RotationTime        *uint   `yaml:"rotationTime"`
		RemainRotationCount *uint   `yaml:"remainRotationCount"`
		RemainLogLevel      *int    `yaml:"remainLogLevel"`
		IsStdout            *bool   `yaml:"isStdout"`
		IsJson              *bool   `yaml:"isJson"`
		WithStack           *bool   `yaml:"withStack"`
	} `yaml:"log"`
	Secret      *string `yaml:"secret"`
	ChatSecret  string  `yaml:"chatSecret"`
	OpenIMUrl   string  `yaml:"openIMUrl"`
	TokenPolicy struct {
		Expire *int64 `yaml:"expire"`
	} `yaml:"tokenPolicy"`
	VerifyCode struct {
		ValidTime int    `yaml:"validTime"`
		UintTime  int    `yaml:"uintTime"`
		MaxCount  int    `yaml:"maxCount"`
		SuperCode string `yaml:"superCode"`
		Len       int    `yaml:"len"`
		Use       string `yaml:"use"`
		Ali       struct {
			Endpoint                     string `yaml:"endpoint"`
			AccessKeyId                  string `yaml:"accessKeyId"`
			AccessKeySecret              string `yaml:"accessKeySecret"`
			SignName                     string `yaml:"signName"`
			VerificationCodeTemplateCode string `yaml:"verificationCodeTemplateCode"`
		} `yaml:"ali"`
		Smsbao struct {
			ApiUrl   string `yaml:"apiUrl"`
			Username string `yaml:"username"`
			Password string `yaml:"password"`
			SignName string `yaml:"signName"`
		} `yaml:"smsbao"`
		Accessyou struct {
			ApiUrl     string `yaml:"apiUrl"`
			AccountNo  string `yaml:"accountNo"`
			Username   string `yaml:"username"`
			Password   string `yaml:"password"`
			TemplateId string `yaml:"templateId"`
		} `yaml:"accessyou"`
		Mail struct {
			Title                   string `yaml:"title"`
			SenderMail              string `yaml:"senderMail"`
			SenderAuthorizationCode string `yaml:"senderAuthorizationCode"`
			SmtpAddr                string `yaml:"smtpAddr"`
			SmtpPort                int    `yaml:"smtpPort"`
		} `yaml:"mail"`
	} `yaml:"verifyCode"`
	ProxyHeader string `yaml:"proxyHeader"`
	// 客户端 IP 认定（见 pkg/common/iptrust）
	IPTrust struct {
		// 可信反向代理（nginx 等），留空默认回环+内网段
		TrustedProxies []string `yaml:"trustedProxies"`
		// 可代报真实用户 IP 的上游（路由服务出口 IP），留空表示不认任何代报
		TrustedForwarders []string `yaml:"trustedForwarders"`
	} `yaml:"ipTrust"`
	// 登录失败锁定（0 使用默认值）
	LoginLock struct {
		MaxFailPerAccountIP  int64 `yaml:"maxFailPerAccountIP"`  // 默认 10
		MaxFailPerAccount    int64 `yaml:"maxFailPerAccount"`    // 默认 30
		WindowMinutes        int64 `yaml:"windowMinutes"`        // 默认 15
		AdminMaxPerAccountIP int64 `yaml:"adminMaxPerAccountIP"` // 默认 10
		AdminMaxPerIP        int64 `yaml:"adminMaxPerIP"`        // 默认 30
	} `yaml:"loginLock"`
	AdminList []Admin `yaml:"adminList"`
	// 身份证实名认证配置
	IDCardVerification struct {
		ApiUrl string `yaml:"apiUrl"`
		ApiKey string `yaml:"apiKey"`
	} `yaml:"idCardVerification"`
	// 服务器标识配置（用于多服务器部署时区分用户）
	ServerTag string `yaml:"serverTag"`
}

type Admin struct {
	AdminID   string `yaml:"adminID"`
	NickName  string `yaml:"nickname"`
	ImAdminID string `yaml:"imAdmin"`
}
