package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"io"
	"net/http"
	"time"
)

type adminMessageSearchAudit struct {
	ID         string    `gorm:"primaryKey;type:varchar(36)"`
	Actor      string    `gorm:"type:varchar(64);not null;index"`
	FilterHash string    `gorm:"type:char(64);not null"`
	SendID     string    `gorm:"type:varchar(64)"`
	RecvID     string    `gorm:"type:varchar(64)"`
	State      string    `gorm:"type:varchar(16);not null"`
	CreatedAt  time.Time `gorm:"not null;index"`
	FinishedAt *time.Time
}

func (adminMessageSearchAudit) TableName() string { return "admin_message_search_audit" }

type messageSearchRequest struct {
	SendID      string `json:"sendID"`
	RecvID      string `json:"recvID"`
	MsgType     int32  `json:"msgType"`
	SendTime    string `json:"sendTime"`
	SessionType int32  `json:"sessionType"`
	Pagination  struct {
		PageNumber int `json:"pageNumber"`
		ShowNumber int `json:"showNumber"`
	} `json:"pagination"`
}

func (a *AdminApi) adminMessageSearch(c *gin.Context) {
	if err := a.previewPermission(c, false); err != nil {
		apiresp.GinError(c, err)
		return
	}
	if a.delegation == nil {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("消息检索审计未配置"))
		return
	}
	var req messageSearchRequest
	if strictJSON(c, &req, 16384) != nil || len(req.SendID) > 64 || len(req.RecvID) > 64 || (req.SendID == "" && req.RecvID == "") || req.Pagination.PageNumber < 1 || req.Pagination.PageNumber > 100000 || req.Pagination.ShowNumber < 1 || req.Pagination.ShowNumber > 100 || req.MsgType < 0 || req.SessionType < 0 || req.SessionType > 4 {
		apiresp.GinError(c, errs.ErrArgs.Wrap("消息检索参数无效；必须限定发送者或接收者"))
		return
	}
	if req.SendTime != "" {
		if _, e := time.Parse("2006-01-02", req.SendTime); e != nil {
			apiresp.GinError(c, errs.ErrArgs.Wrap("日期必须为YYYY-MM-DD"))
			return
		}
	}
	actor, e := mctx.CheckAdmin(c)
	if e != nil {
		apiresp.GinError(c, e)
		return
	}
	body, _ := json.Marshal(req)
	sum := sha256.Sum256(body)
	audit := adminMessageSearchAudit{ID: uuid.NewString(), Actor: actor, FilterHash: hex.EncodeToString(sum[:]), SendID: req.SendID, RecvID: req.RecvID, State: "pending", CreatedAt: time.Now().UTC()}
	if e = a.delegation.DB.WithContext(c).Create(&audit).Error; e != nil {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("消息检索审计不可用"))
		return
	}
	token, e := a.imApiCaller.ImAdminTokenWithDefaultAdmin(c)
	var result *json.RawMessage
	if e == nil {
		result, e = apicall.NewApiCaller[messageSearchRequest, json.RawMessage]("/msg/search_msg", func() string { return config.Config.OpenIMUrl }).Call(mctx.WithApiToken(c, token), &req)
	}
	state := "failed"
	if e == nil {
		state = "completed"
	}
	now := time.Now().UTC()
	finish, cancel := context.WithTimeout(context.WithoutCancel(c), 2*time.Second)
	defer cancel()
	finished := a.delegation.DB.WithContext(finish).Model(&adminMessageSearchAudit{}).Where("id=? AND state=?", audit.ID, "pending").Updates(map[string]any{"state": state, "finished_at": now})
	if finished.Error != nil || finished.RowsAffected != 1 {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("消息检索审计未完成"))
		return
	}
	if e != nil {
		apiresp.GinError(c, e)
		return
	}
	apiresp.GinSuccess(c, result)
}

func strictJSON(c *gin.Context, value any, limit int64) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return errs.ErrArgs.Wrap("只能包含一个JSON对象")
	}
	return nil
}
