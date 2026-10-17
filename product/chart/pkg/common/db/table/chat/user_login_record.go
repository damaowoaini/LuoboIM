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

package chat

import (
	"context"
	"time"
)

// 用户登录信息表.
type UserLoginRecord struct {
	UserID      string    `gorm:"column:user_id;size:64"`
	LoginTime   time.Time `gorm:"column:login_time"`
	IP          string    `gorm:"column:ip;type:varchar(32)"`
	DeviceID    string    `gorm:"column:device_id;type:varchar(255)"`
	Platform    string    `gorm:"column:platform;type:varchar(32)"`
	DeviceModel string    `gorm:"column:device_model;type:varchar(255)"` // 设备型号，如 "Xiaomi Mi 11"
	Location    string    `gorm:"column:location;type:varchar(255)"`     // 登录地区，如 "中国 北京市"
}

func (UserLoginRecord) TableName() string {
	return "user_login_records"
}

type UserLoginRecordInterface interface {
	NewTx(tx any) UserLoginRecordInterface
	Create(ctx context.Context, records ...*UserLoginRecord) error
	CountTotal(ctx context.Context, before *time.Time) (int64, error)
	CountRangeEverydayTotal(ctx context.Context, start *time.Time, end *time.Time) (map[string]int64, int64, error)
	// 获取用户最新的登录记录
	GetLatestLoginRecord(ctx context.Context, userID string) (*UserLoginRecord, error)
	// 获取用户的登录记录列表（带分页）
	GetUserLoginRecords(ctx context.Context, userID string, pageNumber int32, showNumber int32) (int64, []*UserLoginRecord, error)
	// 获取使用同一设备登录的其他用户ID列表
	GetOtherUserIDsByDeviceID(ctx context.Context, deviceID string, excludeUserID string) ([]string, error)
	// 获取用户最新的 App 端登录记录（仅 iOS/Android，用于单设备绑定校验）
	GetLatestAppLoginRecord(ctx context.Context, userID string) (*UserLoginRecord, error)
	// 删除用户全部登录记录（管理员解绑设备）
	DeleteByUserID(ctx context.Context, userID string) error
}
