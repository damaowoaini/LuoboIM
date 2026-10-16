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
	"errors"
	"time"
)

var ErrComplaintSubmissionConflict = errors.New("complaint submission payload conflict")

// ComplaintType 投诉类型
const (
	ComplaintTypeUser  = 1 // 用户投诉
	ComplaintTypeGroup = 2 // 群组投诉
)

// ComplaintStatus 投诉状态
const (
	ComplaintStatusPending   = 0 // 待处理
	ComplaintStatusProcessed = 1 // 已处理
	ComplaintStatusRejected  = 2 // 已驳回
)

type Complaint struct {
	ID                 string     `gorm:"column:id;primary_key;type:char(64)" json:"id"`
	ComplaintUserID    string     `gorm:"column:complaint_user_id;type:char(64);index;uniqueIndex:uniq_complaint_submission,priority:1" json:"complaintUserID"`
	ClientSubmissionID *string    `gorm:"column:client_submission_id;type:char(36);uniqueIndex:uniq_complaint_submission,priority:2" json:"clientSubmissionID,omitempty"`
	PayloadHash        string     `gorm:"column:payload_hash;type:char(64)" json:"-"`
	RemovedAt          *time.Time `gorm:"column:removed_at" json:"-"`
	ComplaintType      int32      `gorm:"column:complaint_type" json:"complaintType"`
	TargetID           string     `gorm:"column:target_id;type:char(64);index" json:"targetID"`
	TargetName         string     `gorm:"column:target_name;type:varchar(255)" json:"targetName"`
	Reason             string     `gorm:"column:reason;type:varchar(500)" json:"reason"`
	Description        string     `gorm:"column:description;type:text" json:"description"`
	Evidence           string     `gorm:"column:evidence;type:text" json:"evidence"` // JSON 数组
	Status             int32      `gorm:"column:status;index" json:"status"`
	HandlerID          string     `gorm:"column:handler_id;type:char(64)" json:"handlerID"`
	HandleResult       string     `gorm:"column:handle_result;type:text" json:"handleResult"`
	CreateTime         time.Time  `gorm:"column:create_time" json:"createTime"`
	UpdateTime         time.Time  `gorm:"column:update_time" json:"updateTime"`
	HandleTime         *time.Time `gorm:"column:handle_time" json:"handleTime"` // 使用指针类型，未处理时为 NULL
}

func (Complaint) TableName() string {
	return "complaints"
}

type ComplaintInterface interface {
	Create(ctx context.Context, complaint *Complaint) error
	Delete(ctx context.Context, ids []string) error
	Update(ctx context.Context, id string, data map[string]any) error
	Take(ctx context.Context, id string) (*Complaint, error)
	FindSubmission(ctx context.Context, owner, submissionID string) (*Complaint, error)
	Search(ctx context.Context, complaintUserID, targetID string, complaintType, status *int32, pageNumber, showNumber int32) (int64, []*Complaint, error)
}
