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

package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/model/chat"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/dbconn"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ComplaintApi struct {
	db         table.ComplaintInterface
	references complaintReferences
}

func NewComplaintApi() (*ComplaintApi, error) {
	db, err := dbconn.NewGormDB()
	if err != nil {
		return nil, err
	}
	if err := dbconn.AutoMigrate(context.Background(), db, &table.Complaint{}); err != nil {
		return nil, err
	}
	return &ComplaintApi{db: chat.NewComplaint(db), references: newComplaintReferences()}, nil
}

// SubmitComplaint 提交投诉
func (a *ComplaintApi) SubmitComplaint(c *gin.Context) {
	var req struct {
		ComplaintUserID    string   `json:"complaintUserID"`
		ClientSubmissionID string   `json:"clientSubmissionID"`
		ComplaintType      int32    `json:"complaintType" binding:"required"`
		TargetID           string   `json:"targetID" binding:"required"`
		TargetName         string   `json:"targetName"`
		Reason             string   `json:"reason" binding:"required"`
		Description        string   `json:"description"`
		Evidence           []string `json:"evidence"`
	}
	if !bindComplaint(c, &req) {
		return
	}
	if !complaintInput(req.ComplaintType, req.TargetID, req.TargetName, req.Reason, req.Description, req.Evidence) {
		apiresp.GinError(c, errs.ErrArgs.Wrap("投诉内容或证据格式无效"))
		return
	}
	if req.ClientSubmissionID != "" && !complaintSubmissionID(req.ClientSubmissionID) {
		apiresp.GinError(c, errs.ErrArgs.Wrap("投诉提交标识无效"))
		return
	}
	opUserID, _, err := ginOpUser(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}

	evidenceJSON, _ := json.Marshal(req.Evidence)
	now := time.Now()
	complaint := &table.Complaint{
		ID:              uuid.New().String(),
		ComplaintUserID: opUserID,
		ComplaintType:   req.ComplaintType,
		TargetID:        req.TargetID,
		TargetName:      req.TargetName,
		Reason:          req.Reason,
		Description:     req.Description,
		Evidence:        string(evidenceJSON),
		Status:          table.ComplaintStatusPending,
		CreateTime:      now,
		UpdateTime:      now,
	}
	if req.ClientSubmissionID != "" {
		complaint.ClientSubmissionID = &req.ClientSubmissionID
		complaint.PayloadHash = complaintPayloadHash(req.ComplaintType, req.TargetID, req.TargetName, req.Reason, req.Description, req.Evidence)
		// Recover an already committed intent before consulting mutable targets or
		// object metadata. Removal or a dependency outage must not recreate it.
		existing, lookupErr := a.db.FindSubmission(c, opUserID, req.ClientSubmissionID)
		if lookupErr == nil {
			if existing == nil || existing.ComplaintUserID != opUserID || existing.ClientSubmissionID == nil || *existing.ClientSubmissionID != req.ClientSubmissionID {
				apiresp.GinError(c, errs.ErrInternalServer.Wrap("投诉回执暂不可用"))
				return
			}
			if existing.PayloadHash != complaint.PayloadHash {
				apiresp.GinError(c, errs.ErrArgs.Wrap("投诉提交标识与原内容冲突，请查看原投诉"))
				return
			}
			apiresp.GinSuccess(c, gin.H{"complaintID": existing.ID})
			return
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			apiresp.GinError(c, errs.ErrInternalServer.Wrap("投诉回执暂不可用"))
			return
		}
	}
	if a.references == nil {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("投诉校验暂不可用，请稍后重试"))
		return
	}
	checkCtx, cancel := context.WithTimeout(c, 8*time.Second)
	defer cancel()
	targetName, checkErr := a.references.TargetName(checkCtx, req.ComplaintType, req.TargetID)
	if checkErr == nil {
		checkErr = a.references.VerifyEvidence(checkCtx, opUserID, req.Evidence)
	}
	if checkErr != nil {
		if errors.Is(checkErr, errComplaintReferenceInvalid) {
			apiresp.GinError(c, errs.ErrArgs.Wrap("投诉目标或图片证据无效，请重新选择"))
		} else {
			apiresp.GinError(c, errs.ErrInternalServer.Wrap("投诉校验暂不可用，请稍后重试"))
		}
		return
	}
	complaint.TargetName = targetName

	if err := a.db.Create(c, complaint); err != nil {
		if errors.Is(err, table.ErrComplaintSubmissionConflict) {
			apiresp.GinError(c, errs.ErrArgs.Wrap("投诉提交标识与原内容冲突，请查看原投诉"))
			return
		}
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("投诉提交结果暂未确认，请先查看记录"))
		return
	}

	apiresp.GinSuccess(c, gin.H{"complaintID": complaint.ID})
}

// Receipt is strictly owner scoped. Not found does not prove that another
// request is not still committing; callers must retain the same submission ID.
func (a *ComplaintApi) Receipt(c *gin.Context) {
	var req struct {
		ClientSubmissionID string `json:"clientSubmissionID" binding:"required"`
	}
	if !bindComplaint(c, &req) {
		return
	}
	if !complaintSubmissionID(req.ClientSubmissionID) {
		apiresp.GinError(c, errs.ErrArgs.Wrap("投诉提交标识无效"))
		return
	}
	owner, _, err := ginOpUser(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	record, err := a.db.FindSubmission(c, owner, req.ClientSubmissionID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		apiresp.GinSuccess(c, gin.H{"found": false, "replaySafe": true})
		return
	}
	if err != nil {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("投诉回执暂不可用"))
		return
	}
	if record == nil || record.ComplaintUserID != owner || record.ClientSubmissionID == nil || *record.ClientSubmissionID != req.ClientSubmissionID {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("投诉回执暂不可用"))
		return
	}
	apiresp.GinSuccess(c, gin.H{"found": true, "replaySafe": true, "complaintID": record.ID, "status": record.Status, "clientSubmissionID": req.ClientSubmissionID})
}

// SearchComplaint 搜索投诉
func (a *ComplaintApi) SearchComplaint(c *gin.Context) {
	var req struct {
		ComplaintUserID string `json:"complaintUserID"`
		TargetID        string `json:"targetID"`
		ComplaintType   *int32 `json:"complaintType"`
		Status          *int32 `json:"status"`
		PageNumber      int32  `json:"pageNumber" binding:"required"`
		ShowNumber      int32  `json:"showNumber" binding:"required"`
	}
	if !bindComplaint(c, &req) {
		return
	}
	if !complaintPage(req.PageNumber, req.ShowNumber, req.ComplaintType, req.Status) ||
		!complaintID(req.TargetID, false) || !complaintID(req.ComplaintUserID, false) {
		apiresp.GinError(c, errs.ErrArgs.Wrap("投诉筛选或分页无效"))
		return
	}
	opUserID, userType, err := ginOpUser(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if userType != constant.AdminUser {
		req.ComplaintUserID = opUserID
	}

	total, complaints, err := a.db.Search(c, req.ComplaintUserID, req.TargetID, req.ComplaintType, req.Status, req.PageNumber, req.ShowNumber)
	if err != nil {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("投诉记录暂不可用"))
		return
	}

	apiresp.GinSuccess(c, gin.H{"total": total, "complaints": complaints})
}

// HandleComplaint 处理投诉
func (a *ComplaintApi) HandleComplaint(c *gin.Context) {
	var req struct {
		ID           string `json:"id" binding:"required"`
		Status       int32  `json:"status" binding:"required"`
		HandlerID    string `json:"handlerID" binding:"required"`
		HandleResult string `json:"handleResult"`
	}
	if !bindComplaint(c, &req) {
		return
	}
	if !complaintID(req.ID, true) || (req.Status != 1 && req.Status != 2) || !complaintText(req.HandleResult, 4096, false, true) {
		apiresp.GinError(c, errs.ErrArgs.Wrap("投诉处理参数无效"))
		return
	}
	if !ginRequireAdmin(c) {
		return
	}
	opUserID, _, _ := ginOpUser(c)

	data := map[string]any{
		"status":        req.Status,
		"handler_id":    opUserID,
		"handle_result": req.HandleResult,
		"handle_time":   time.Now(),
	}

	if err := a.db.Update(c, req.ID, data); err != nil {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("投诉处理暂不可用"))
		return
	}

	apiresp.GinSuccess(c, nil)
}

// DeleteComplaint 删除投诉
func (a *ComplaintApi) DeleteComplaint(c *gin.Context) {
	var req struct {
		IDs []string `json:"ids" binding:"required"`
	}
	if !bindComplaint(c, &req) {
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > 100 {
		apiresp.GinError(c, errs.ErrArgs.Wrap("投诉删除数量无效"))
		return
	}
	for _, id := range req.IDs {
		if !complaintID(id, true) {
			apiresp.GinError(c, errs.ErrArgs.Wrap("投诉编号无效"))
			return
		}
	}
	if !ginRequireAdmin(c) {
		return
	}

	if err := a.db.Delete(c, req.IDs); err != nil {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("投诉删除暂不可用"))
		return
	}

	apiresp.GinSuccess(c, nil)
}

// GetComplaintDetail 获取投诉详情
func (a *ComplaintApi) GetComplaintDetail(c *gin.Context) {
	var req struct {
		ID string `json:"id" binding:"required"`
	}
	if !bindComplaint(c, &req) {
		return
	}
	if !complaintID(req.ID, true) {
		apiresp.GinError(c, errs.ErrArgs.Wrap("投诉编号无效"))
		return
	}

	complaint, err := a.db.Take(c, req.ID)
	if err != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("投诉记录不存在"))
		return
	}
	opUserID, userType, err := ginOpUser(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if userType != constant.AdminUser && complaint.ComplaintUserID != opUserID {
		apiresp.GinError(c, errs.ErrNoPermission.Wrap("无权查看该投诉"))
		return
	}

	apiresp.GinSuccess(c, complaint)
}
