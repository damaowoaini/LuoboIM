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

// UserVerification 用户实名认证
type UserVerification struct {
	UserID         string    `gorm:"column:user_id;primary_key;type:char(64)" json:"userID"`
	RealName       string    `gorm:"column:real_name;type:varchar(100)" json:"realName"`
	IDCardNumber   string    `gorm:"column:id_card_number;type:varchar(20)" json:"idCardNumber"`
	IDCardFrontURL string    `gorm:"column:id_card_front_url;type:varchar(500)" json:"idCardFrontURL"`
	IDCardBackURL  string    `gorm:"column:id_card_back_url;type:varchar(500)" json:"idCardBackURL"`
	Status         int32     `gorm:"column:status" json:"status"` // 0-未认证, 1-已认证, 2-审核中, 3-审核失败
	SubmitTime     time.Time `gorm:"column:submit_time" json:"submitTime"`
	VerifyTime     time.Time `gorm:"column:verify_time" json:"verifyTime"`
	RejectReason   string    `gorm:"column:reject_reason;type:varchar(500)" json:"rejectReason"`
}

func (UserVerification) TableName() string {
	return "user_verifications"
}

type UserVerificationInterface interface {
	CreateOrUpdate(ctx context.Context, verification *UserVerification) error
	Take(ctx context.Context, userID string) (*UserVerification, error)
	UpdateStatus(ctx context.Context, userID string, status int32, rejectReason string) error
}
