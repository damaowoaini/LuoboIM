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

func NewComplaint(db *gorm.DB) chat.ComplaintInterface {
	return &Complaint{db: db}
}

type Complaint struct {
	db *gorm.DB
}

func (c *Complaint) Create(ctx context.Context, complaint *chat.Complaint) error {
	if complaint.ClientSubmissionID == nil {
		return errs.Wrap(c.db.WithContext(ctx).Create(complaint).Error)
	}
	if complaint.ComplaintUserID == "" || complaint.PayloadHash == "" {
		return errs.ErrArgs.Wrap("投诉回执归属无效")
	}
	return errs.Wrap(c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A concurrent request with the same owner/key waits for the original
		// transaction. Never replace its ID, content, result or creation time.
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(complaint).Error; err != nil {
			return err
		}
		var existing chat.Complaint
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"complaint_user_id = ? AND client_submission_id = ?", complaint.ComplaintUserID, *complaint.ClientSubmissionID,
		).Take(&existing).Error; err != nil {
			return err
		}
		if existing.PayloadHash != complaint.PayloadHash {
			return chat.ErrComplaintSubmissionConflict
		}
		*complaint = existing
		return nil
	}))
}

func (c *Complaint) FindSubmission(ctx context.Context, owner, submissionID string) (*chat.Complaint, error) {
	var record chat.Complaint
	err := c.db.WithContext(ctx).Where("complaint_user_id = ? AND client_submission_id = ?", owner, submissionID).Take(&record).Error
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return &record, nil
}

func (c *Complaint) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return errs.Wrap(c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Legacy clients had no idempotency key. Keyed submissions keep only a
		// minimal receipt so a late retry cannot recreate removed content.
		if err := tx.Where("id IN ? AND client_submission_id IS NULL", ids).Delete(&chat.Complaint{}).Error; err != nil {
			return err
		}
		now := time.Now()
		return tx.Model(&chat.Complaint{}).
			Where("id IN ? AND client_submission_id IS NOT NULL AND removed_at IS NULL", ids).
			Updates(map[string]any{
				"removed_at": now, "update_time": now,
				"complaint_type": 0, "target_id": "", "target_name": "", "reason": "",
				"description": "", "evidence": "", "handler_id": "", "handle_result": "", "handle_time": nil,
			}).Error
	}))
}

func (c *Complaint) Update(ctx context.Context, id string, data map[string]any) error {
	if len(data) == 0 {
		return nil
	}
	data["update_time"] = time.Now()
	return errs.Wrap(c.db.WithContext(ctx).Model(&chat.Complaint{}).Where("id = ? AND removed_at IS NULL", id).Updates(data).Error)
}

func (c *Complaint) Take(ctx context.Context, id string) (*chat.Complaint, error) {
	var complaint chat.Complaint
	if err := c.db.WithContext(ctx).Where("id = ? AND removed_at IS NULL", id).First(&complaint).Error; err != nil {
		return nil, errs.Wrap(err)
	}
	return &complaint, nil
}

func (c *Complaint) Search(ctx context.Context, complaintUserID, targetID string, complaintType, status *int32, pageNumber, showNumber int32) (int64, []*chat.Complaint, error) {
	db := c.db.WithContext(ctx).Model(&chat.Complaint{}).Where("removed_at IS NULL")
	if complaintUserID != "" {
		db = db.Where("complaint_user_id = ?", complaintUserID)
	}
	if targetID != "" {
		db = db.Where("target_id = ?", targetID)
	}
	if complaintType != nil {
		db = db.Where("complaint_type = ?", *complaintType)
	}
	if status != nil {
		db = db.Where("status = ?", *status)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return 0, nil, errs.Wrap(err)
	}

	var complaints []*chat.Complaint
	offset := (pageNumber - 1) * showNumber
	if err := db.Order("create_time DESC").Offset(int(offset)).Limit(int(showNumber)).Find(&complaints).Error; err != nil {
		return 0, nil, errs.Wrap(err)
	}

	return total, complaints, nil
}
