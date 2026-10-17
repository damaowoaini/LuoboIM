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


	"github.com/OpenIMSDK/tools/errs"

	"gorm.io/gorm"

	"github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
)

func NewUserLoginRecord(db *gorm.DB) chat.UserLoginRecordInterface {
	return &UserLoginRecord{
		db: db,
	}
}

type UserLoginRecord struct {
	db *gorm.DB
}

func (o *UserLoginRecord) NewTx(tx any) chat.UserLoginRecordInterface {
	return &UserLoginRecord{db: tx.(*gorm.DB)}
}

func (o *UserLoginRecord) Create(ctx context.Context, records ...*chat.UserLoginRecord) error {
	return o.db.WithContext(ctx).Create(&records).Error
}

func (o *UserLoginRecord) CountTotal(ctx context.Context, before *time.Time) (count int64, err error) {
	db := o.db.WithContext(ctx).Model(&chat.UserLoginRecord{})
	if before != nil {
		db.Where("create_time < ?", before)
	}
	if err := db.Count(&count).Error; err != nil {
		return 0, errs.Wrap(err)
	}
	return count, nil
}

func (o *UserLoginRecord) CountRangeEverydayTotal(ctx context.Context, start *time.Time, end *time.Time) (map[string]int64, int64, error) {
	var res []struct {
		Date  time.Time `gorm:"column:date"`
		Count int64     `gorm:"column:count"`
	}
	var loginCount int64
	err := o.db.WithContext(ctx).
		Model(&chat.UserLoginRecord{}).
		Select("DATE(login_time) AS date, count(distinct(user_id)) AS count").
		Where("login_time >= ? and login_time < ?", start, end).
		Group("date").
		Find(&res).
		Error
	if err != nil {
		return nil, 0, errs.Wrap(err)
	}
	v := make(map[string]int64)
	for _, r := range res {
		loginCount += r.Count
		v[r.Date.Format("2006-01-02")] = r.Count
	}
	return v, loginCount, nil
}

// 获取用户最新的登录记录
func (o *UserLoginRecord) GetLatestLoginRecord(ctx context.Context, userID string) (*chat.UserLoginRecord, error) {
	var record chat.UserLoginRecord
	err := o.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("login_time DESC").
		First(&record).
		Error
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return &record, nil
}

// 获取用户的登录记录列表（带分页）
func (o *UserLoginRecord) GetUserLoginRecords(ctx context.Context, userID string, pageNumber int32, showNumber int32) (int64, []*chat.UserLoginRecord, error) {
	var count int64
	var records []*chat.UserLoginRecord
	
	db := o.db.WithContext(ctx).Model(&chat.UserLoginRecord{}).Where("user_id = ?", userID)
	
	if err := db.Count(&count).Error; err != nil {
		return 0, nil, errs.Wrap(err)
	}
	
	err := db.Order("login_time DESC").
		Limit(int(showNumber)).
		Offset(int((pageNumber - 1) * showNumber)).
		Find(&records).
		Error
	if err != nil {
		return 0, nil, errs.Wrap(err)
	}
	
	return count, records, nil
}

// 获取使用同一设备登录的其他用户ID列表
func (o *UserLoginRecord) GetOtherUserIDsByDeviceID(ctx context.Context, deviceID string, excludeUserID string) ([]string, error) {
	if deviceID == "" {
		return []string{}, nil
	}
	
	var userIDs []string
	err := o.db.WithContext(ctx).
		Model(&chat.UserLoginRecord{}).
		Where("device_id = ? AND user_id != ?", deviceID, excludeUserID).
		Distinct("user_id").
		Pluck("user_id", &userIDs).
		Error
	
	if err != nil {
		return nil, errs.Wrap(err)
	}
	
	return userIDs, nil
}

// GetLatestAppLoginRecord 获取用户最新的 App 端登录记录（仅 iOS/Android），用于单设备绑定校验。
func (o *UserLoginRecord) GetLatestAppLoginRecord(ctx context.Context, userID string) (*chat.UserLoginRecord, error) {
	var record chat.UserLoginRecord
	err := o.db.WithContext(ctx).
		Where("user_id = ? AND platform IN ?", userID, []string{"IOS", "Android"}).
		Order("login_time DESC").
		First(&record).
		Error
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return &record, nil
}

// DeleteByUserID 删除用户的全部登录记录（管理员解绑设备用）
func (o *UserLoginRecord) DeleteByUserID(ctx context.Context, userID string) error {
	return errs.Wrap(o.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&chat.UserLoginRecord{}).Error)
}
