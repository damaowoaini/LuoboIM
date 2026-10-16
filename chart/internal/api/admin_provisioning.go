package api

import (
	"context"
	"os"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/adminprovision"
	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func newProvisioningView(db *gorm.DB, caller apicall.CallerInterface) (*adminprovision.Service, error) {
	cfg := config.Config.AdminProvisioning
	s := &adminprovision.Service{DB: db, ServerID: cfg.ServerID, RouteEnabled: cfg.Enabled}
	if cfg.Enabled {
		route := &adminprovision.RouteClient{ServerID: cfg.ServerID, Base: cfg.RouteURL, Secret: []byte(os.Getenv("LUOBO_ROUTE_SHARED_SECRET")), AllowLoopbackHTTP: cfg.AllowLoopbackHTTP}
		if e := route.Validate(); e != nil {
			return nil, e
		}
		s.Route = route
	}
	s.FindExisting = func(ctx context.Context, id string) (*adminprovision.User, error) {
		finder, ok := caller.(interface {
			FindRegisteredUsers(context.Context, []string) ([]*sdkws.UserInfo, error)
		})
		if !ok {
			return nil, errs.ErrInternalServer.Wrap("IM归属查询不可用")
		}
		ctx = context.WithValue(ctx, constant.OperationID, "admin-provision-recovery")
		ctx = mctx.WithAdminUser(ctx)
		users, e := finder.FindRegisteredUsers(ctx, []string{id})
		if e != nil {
			return nil, e
		}
		if len(users) != 1 || users[0].UserID != id {
			return nil, errs.ErrArgs.Wrap("IM归属无法确认")
		}
		return &adminprovision.User{ID: id, CreateTime: users[0].CreateTime}, nil
	}
	return s, nil
}
func (a *AdminApi) provisioningActor(c *gin.Context) (string, int32, error) {
	if a.delegation == nil || a.provisioning == nil {
		return "", 0, errs.ErrNoPermission.Wrap("开户服务不可用")
	}
	ctx, cancel := context.WithTimeout(c, 1500*time.Millisecond)
	defer cancel()
	actor, level, e := a.delegation.AuthorizeSession(ctx, c.GetHeader("token"))
	expected, _ := mctx.CheckAdmin(c)
	if e != nil || actor == "" || actor != expected {
		return "", 0, errs.ErrNoPermission.Wrap("管理员会话不可用")
	}
	return actor, level, nil
}
func (a *AdminApi) AdminProvisioningStatus(c *gin.Context) {
	var req struct {
		AdminRequestID string `json:"adminRequestID"`
		ReceiptID      string `json:"receiptID"`
	}
	if strictJSON(c, &req, 1024) != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("开户状态查询无效"))
		return
	}
	actor, level, e := a.provisioningActor(c)
	if e != nil {
		apiresp.GinError(c, e)
		return
	}
	ctx, cancel := context.WithTimeout(c, 2*time.Second)
	defer cancel()
	status, e := a.provisioning.Lookup(ctx, actor, req.AdminRequestID, req.ReceiptID, level == 100)
	if e != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("开户状态无法确认，请保留原意图"))
		return
	}
	apiresp.GinSuccess(c, status)
}
func (a *AdminApi) RecoverAdminProvisioning(c *gin.Context) {
	var req struct {
		UserID string `json:"userID"`
		Reason string `json:"reason"`
	}
	if strictJSON(c, &req, 2048) != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("开户恢复请求无效"))
		return
	}
	actor, level, e := a.provisioningActor(c)
	if e != nil || level != 100 {
		apiresp.GinError(c, errs.ErrNoPermission.Wrap("仅当前超级管理员可授权恢复"))
		return
	}
	ctx, cancel := context.WithTimeout(c, 5*time.Second)
	defer cancel()
	status, e := a.provisioning.Recover(ctx, actor, req.UserID, req.Reason)
	if e != nil {
		apiresp.GinError(c, errs.ErrArgs.Wrap("开户恢复未受理，请核查账号归属或服务配置"))
		return
	}
	apiresp.GinSuccess(c, status)
}

func (a *AdminApi) ResolveAdminProvisioning(c *gin.Context) {
	var req struct {
		AdminRequestID    string `json:"adminRequestID"`
		CloseIfNotCreated bool   `json:"closeIfNotCreated"`
	}
	if strictJSON(c, &req, 1024) != nil || !req.CloseIfNotCreated {
		apiresp.GinError(c, errs.ErrArgs.Wrap("须明确停止尚未创建的原开户意图"))
		return
	}
	actor, _, e := a.provisioningActor(c)
	if e != nil {
		apiresp.GinError(c, e)
		return
	}
	ctx, cancel := context.WithTimeout(c, 3*time.Second)
	defer cancel()
	status, e := a.provisioning.ResolveUncreated(ctx, actor, req.AdminRequestID)
	if e != nil {
		apiresp.GinError(c, errs.ErrInternalServer.Wrap("停止意图尚未确认，请保留原标识查询"))
		return
	}
	apiresp.GinSuccess(c, status)
}
