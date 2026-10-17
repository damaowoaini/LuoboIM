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

	"github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/tools/errs"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func NewUserVerification(db *gorm.DB) chat.UserVerificationInterface {
	return &UserVerification{db: db}
}

type UserVerification struct {
	db *gorm.DB
}

func (u *UserVerification) CreateOrUpdate(ctx context.Context, verification *chat.UserVerification) error {
	if verification.SubmitTime.IsZero() {
		verification.SubmitTime = time.Now()
	}
	return errs.Wrap(u.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		UpdateAll: true,
	}).Create(verification).Error)
}

func (u *UserVerification) Take(ctx context.Context, userID string) (*chat.UserVerification, error) {
	var verification chat.UserVerification
	if err := u.db.WithContext(ctx).Where("user_id = ?", userID).First(&verification).Error; err != nil {
		return nil, errs.Wrap(err)
	}
	return &verification, nil
}

func (u *UserVerification) UpdateStatus(ctx context.Context, userID string, status int32, rejectReason string) error {
	return errs.Wrap(u.db.WithContext(ctx).Model(&chat.UserVerification{}).Where("user_id = ?", userID).Updates(map[string]any{
		"status":        status,
		"verify_time":   time.Now(),
		"reject_reason": rejectReason,
	}).Error)
}
