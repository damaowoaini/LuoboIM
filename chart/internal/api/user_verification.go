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
	"errors"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/model/chat"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/dbconn"
	"github.com/OpenIMSDK/chat/pkg/common/idcard"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type UserVerificationApi struct {
	db             table.UserVerificationInterface
	idCardVerifier *idcard.IDCardVerifier
}

func NewUserVerificationApi() (*UserVerificationApi, error) {
	db, err := dbconn.NewGormDB()
	if err != nil {
		return nil, err
	}
	if err := dbconn.AutoMigrate(context.Background(), db, &table.UserVerification{}); err != nil {
		return nil, err
	}

	idCardConfig := idcard.IDCardVerifyConfig{
		APIUrl: config.Config.IDCardVerification.ApiUrl,
		APIKey: config.Config.IDCardVerification.ApiKey,
	}
	idCardVerifier := idcard.NewIDCardVerifier(idCardConfig)

	return &UserVerificationApi{
		db:             chat.NewUserVerification(db),
		idCardVerifier: idCardVerifier,
	}, nil
}

// SubmitVerification 提交实名认证
func (u *UserVerificationApi) SubmitVerification(c *gin.Context) {
	var req struct {
		UserID         string `json:"userID" binding:"required"`
		RealName       string `json:"realName" binding:"required"`
		IDCardNumber   string `json:"idCardNumber" binding:"required"`
		IDCardFrontURL string `json:"idCardFrontURL" binding:"required"`
		IDCardBackURL  string `json:"idCardBackURL" binding:"required"`
	}
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("参数错误: "+err.Error()))
		return
	}
	opUserID, _, err := ginOpUser(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	req.UserID = opUserID

	if !idcard.ValidateIDCardFormat(req.IDCardNumber) {
		apiresp.GinError(c, errs.ErrArgs.Wrap("身份证号格式不正确，请输入18位有效身份证号"))
		return
	}

	isValid, err := u.idCardVerifier.Verify(req.RealName, req.IDCardNumber)
	if err != nil {
		log.ZError(c.Request.Context(), "IDCard verification failed", err)
		apiresp.GinError(c, errs.ErrArgs.Wrap("身份证验证失败: "+err.Error()))
		return
	}

	if !isValid {
		apiresp.GinError(c, errs.ErrArgs.Wrap("身份证信息不匹配，请检查姓名和身份证号是否正确"))
		return
	}

	verification := &table.UserVerification{
		UserID:         req.UserID,
		RealName:       req.RealName,
		IDCardNumber:   req.IDCardNumber,
		IDCardFrontURL: req.IDCardFrontURL,
		IDCardBackURL:  req.IDCardBackURL,
		Status:         1,
		SubmitTime:     time.Now(),
		VerifyTime:     time.Now(),
	}

	if err := u.db.CreateOrUpdate(c.Request.Context(), verification); err != nil {
		log.ZError(c.Request.Context(), "SubmitVerification failed", err)
		apiresp.GinError(c, errs.ErrArgs.Wrap("提交失败: "+err.Error()))
		return
	}

	apiresp.GinSuccess(c, gin.H{"status": verification.Status})
}

// GetVerificationStatus 获取实名认证状态
func (u *UserVerificationApi) GetVerificationStatus(c *gin.Context) {
	var req struct {
		UserID     string `json:"userID" binding:"required"`
		StatusOnly bool   `json:"statusOnly"`
	}
	if err := c.BindJSON(&req); err != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("参数错误: "+err.Error()))
		return
	}
	opUserID, userType, err := ginOpUser(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if userType != constant.AdminUser {
		req.UserID = opUserID
	}

	verification, err := u.db.Take(c.Request.Context(), req.UserID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			apiresp.GinSuccess(c, gin.H{"status": 0})
		} else {
			apiresp.GinError(c, errs.ErrInternalServer.Wrap("认证状态暂不可用，请稍后重试"))
		}
		return
	}
	if verification == nil || verification.UserID != req.UserID {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("认证状态暂不可用，请稍后重试"))
		return
	}
	// Entry preflights need no names, document numbers, images or review text.
	// Existing detail callers and authenticated administrator targets keep their
	// original response contract when this explicit option is absent.
	if req.StatusOnly {
		apiresp.GinSuccess(c, gin.H{"status": verification.Status})
		return
	}

	apiresp.GinSuccess(c, gin.H{
		"status":         verification.Status,
		"realName":       verification.RealName,
		"idCardNumber":   verification.IDCardNumber,
		"idCardFrontURL": verification.IDCardFrontURL,
		"idCardBackURL":  verification.IDCardBackURL,
		"rejectReason":   verification.RejectReason,
	})
}
