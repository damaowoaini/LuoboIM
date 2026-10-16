package api

import (
	"encoding/hex"
	"strings"

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	admintable "github.com/OpenIMSDK/chat/pkg/common/db/table/admin"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AdminAccountStatusApi struct {
	admins database.AdminDatabaseInterface
	db     *gorm.DB
}

func NewAdminAccountStatusApi(db *gorm.DB, admins database.AdminDatabaseInterface) *AdminAccountStatusApi {
	return &AdminAccountStatusApi{db: db, admins: admins}
}

type listAdminAccountsReq struct {
	Pagination struct {
		PageNumber int32 `json:"pageNumber"`
		ShowNumber int32 `json:"showNumber"`
	} `json:"pagination"`
}

type adminAccountItem struct {
	Account    string `json:"account"`
	FaceURL    string `json:"faceURL"`
	Nickname   string `json:"nickname"`
	UserID     string `json:"userID"`
	Level      int32  `json:"level"`
	Disabled   bool   `json:"disabled"`
	CreateTime int64  `json:"createTime"`
}

func (a *AdminAccountStatusApi) List(c *gin.Context) {
	var req listAdminAccountsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.GinError(c, errs.Wrap(err))
		return
	}
	if err := a.checkSuperAdmin(c); err != nil {
		apiresp.GinError(c, err)
		return
	}
	page, size := req.Pagination.PageNumber, req.Pagination.ShowNumber
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	var total int64
	if err := a.db.WithContext(c).Model(&admintable.Admin{}).Count(&total).Error; err != nil {
		apiresp.GinError(c, errs.Wrap(err))
		return
	}
	var rows []*admintable.Admin
	if err := a.db.WithContext(c).
		Select("account", "face_url", "nickname", "user_id", "level", "disabled", "create_time").
		Order("level DESC, create_time DESC").
		Limit(int(size)).Offset(int((page - 1) * size)).Find(&rows).Error; err != nil {
		apiresp.GinError(c, errs.Wrap(err))
		return
	}
	list := make([]adminAccountItem, 0, len(rows))
	for _, row := range rows {
		list = append(list, adminAccountItem{
			Account: row.Account, FaceURL: row.FaceURL, Nickname: row.Nickname,
			UserID: row.UserID, Level: row.Level, Disabled: row.Disabled,
			CreateTime: row.CreateTime.UnixMilli(),
		})
	}
	apiresp.GinSuccess(c, gin.H{"total": total, "adminAccounts": list})
}

type setAdminAccountStatusReq struct {
	UserID  string `json:"userID" binding:"required"`
	Enabled bool   `json:"enabled"`
}

func (a *AdminAccountStatusApi) SetStatus(c *gin.Context) {
	var req setAdminAccountStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.GinError(c, errs.Wrap(err))
		return
	}
	if err := a.checkSuperAdmin(c); err != nil {
		apiresp.GinError(c, err)
		return
	}
	opUserID, _, err := ginOpUser(c)
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	if !req.Enabled && req.UserID == opUserID {
		apiresp.GinError(c, errs.ErrArgs.Wrap("不能禁用当前登录账号"))
		return
	}
	if _, err := a.admins.GetAdminUserID(c, req.UserID); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := a.admins.UpdateAdmin(c, req.UserID, map[string]any{"disabled": !req.Enabled}); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if !req.Enabled {
		if err := a.admins.DeleteUserToken(c, req.UserID); err != nil {
			apiresp.GinError(c, err)
			return
		}
	}
	apiresp.GinSuccess(c, gin.H{"enabled": req.Enabled})
}

type resetAdminPasswordReq struct {
	UserID      string `json:"userID" binding:"required"`
	NewPassword string `json:"newPassword" binding:"required"`
}

func (a *AdminAccountStatusApi) ResetPassword(c *gin.Context) {
	var req resetAdminPasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.GinError(c, errs.Wrap(err))
		return
	}
	if err := a.checkSuperAdmin(c); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if len(req.NewPassword) != 32 {
		apiresp.GinError(c, errs.ErrArgs.Wrap("密码格式错误"))
		return
	}
	if _, err := hex.DecodeString(req.NewPassword); err != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("密码格式错误"))
		return
	}
	if _, err := a.admins.GetAdminUserID(c, req.UserID); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := a.admins.ChangePassword(c, req.UserID, strings.ToLower(req.NewPassword)); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if err := a.admins.DeleteUserToken(c, req.UserID); err != nil {
		apiresp.GinError(c, err)
		return
	}
	apiresp.GinSuccess(c, nil)
}

func (a *AdminAccountStatusApi) checkSuperAdmin(c *gin.Context) error {
	opUserID, _, err := ginOpUser(c)
	if err != nil {
		return err
	}
	operator, err := a.admins.GetAdminUserID(c, opUserID)
	if err != nil {
		return err
	}
	if operator.Level != constant.AdvancedUserLevel || operator.Disabled {
		return errs.ErrNoPermission.Wrap("仅超级管理员可以管理后台账号")
	}
	return nil
}
