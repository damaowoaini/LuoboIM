package chat

import (
	"context"
	"strings"

	"github.com/OpenIMSDK/chat/pkg/common/adminprovision"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/eerrs"
	"github.com/OpenIMSDK/chat/pkg/proto/chat"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/google/uuid"
)

func (o *chatSvr) RegisterUser(ctx context.Context, req *chat.RegisterUserReq) (*chat.RegisterUserResp, error) {
	isAdmin, e := o.Admin.CheckNilOrAdmin(ctx)
	if e != nil {
		return nil, e
	}
	if !isAdmin {
		if req.AdminRequestID != "" || req.AdminDefaultsAll {
			return nil, errs.ErrNoPermission.Wrap("开户意图仅管理员可用")
		}
		return o.registerPublicUser(ctx, req)
	}
	if o.Provisioning == nil {
		return nil, errs.ErrInternalServer.Wrap("开户持久队列不可用")
	}
	actor, e := mctx.CheckAdmin(ctx)
	if e != nil {
		return nil, e
	}
	if req.User == nil {
		return nil, errs.ErrArgs.Wrap("用户信息不能为空")
	}
	req.User.Email = strings.ToLower(strings.TrimSpace(req.User.Email))
	req.User.AreaCode = strings.TrimSpace(req.User.AreaCode)
	req.User.PhoneNumber = strings.TrimSpace(req.User.PhoneNumber)
	if req.User.PhoneNumber != "" && req.User.AreaCode != "" && !strings.HasPrefix(req.User.AreaCode, "+") {
		req.User.AreaCode = "+" + req.User.AreaCode
	}
	profile := adminprovision.Profile{RequestedUserID: req.User.UserID, Account: req.User.Account, Email: req.User.Email, AreaCode: req.User.AreaCode, PhoneNumber: req.User.PhoneNumber, Nickname: req.User.Nickname, FaceURL: req.User.FaceURL, Gender: req.User.Gender, Birth: req.User.Birth, DefaultsAll: req.AdminDefaultsAll}
	fingerprint, e := profile.Fingerprint()
	if e != nil {
		return nil, errs.ErrArgs.Wrap("开户身份参数无效")
	}
	if req.AdminRequestID == "" {
		req.AdminRequestID = uuid.NewString()
	}
	if !adminprovision.ValidID(req.AdminRequestID) {
		return nil, errs.ErrArgs.Wrap("开户意图标识无效")
	}
	creation, e := o.Provisioning.Reserve(ctx, actor, req.AdminRequestID, fingerprint, req.AdminDefaultsAll)
	if e != nil {
		return nil, errs.ErrArgs.Wrap("开户意图不可复用，请查询原意图状态")
	}
	if !creation.Recovered && req.User.UserID != "" {
		im, lookupErr := o.Provisioning.IM.Find(ctx, req.User.UserID)
		if lookupErr != nil {
			return nil, errs.ErrInternalServer.Wrap("IM账号归属暂无法确认，请查询原意图")
		}
		if im != nil {
			if rejectErr := o.Provisioning.Reject(ctx, creation, "im_id_exists"); rejectErr != nil {
				return nil, errs.ErrInternalServer.Wrap("开户意图状态待确认")
			}
			if !creation.Recovered {
				return nil, errs.ErrArgs.Wrap("指定用户ID已属于IM账号，不允许重新开户")
			}
		}
	}
	if !creation.Recovered {
		_, e = o.registerUser(adminprovision.WithCreation(ctx, creation), req)
		if e != nil {
			// Only known pre-write validation failures are definitive. Commit timeout,
			// storage failure or ambiguous response always retains the original intent.
			definite := errs.ErrArgs.Is(e) || eerrs.ErrPhoneAlreadyRegister.Is(e) || eerrs.ErrEmailAlreadyRegister.Is(e) || eerrs.ErrAccountAlreadyRegister.Is(e)
			if definite && creation.UserID == "" {
				if rejectErr := o.Provisioning.Reject(ctx, creation, "validation_rejected"); rejectErr != nil {
					return nil, e
				}
			}
			if !creation.Recovered {
				return nil, e
			}
		}
	}
	processAdminProvisioning(o.Provisioning, creation.ReceiptID)
	status, e := o.Provisioning.Lookup(ctx, actor, "", creation.ReceiptID, false)
	if e != nil {
		return nil, e
	}
	return &chat.RegisterUserResp{UserID: creation.UserID, AdminRequestID: creation.RequestID, ProvisioningReceiptID: creation.ReceiptID, ProvisioningState: status.State, ProvisioningPending: status.State != "bound" && status.State != "direct_only", Recovered: creation.Recovered}, nil
}

func (o *chatSvr) registerPublicUser(ctx context.Context, req *chat.RegisterUserReq) (*chat.RegisterUserResp, error) {
	if o.Provisioning == nil || o.Provisioning.IM == nil {
		return nil, errs.ErrInternalServer.Wrap("注册持久队列不可用")
	}
	if req.User == nil {
		return nil, errs.ErrArgs.Wrap("用户信息不能为空")
	}
	if req.User.UserID != "" {
		return nil, errs.ErrNoPermission.Wrap("只有管理员可以设置用户ID")
	}
	creation := adminprovision.NewPublicCreation(ctx, o.Provisioning.ServerID)
	resp, e := o.registerUser(adminprovision.WithPublicCreation(ctx, creation), req)
	if e != nil {
		return nil, e
	}
	processAdminProvisioning(o.Provisioning, creation.ReceiptID)
	status, e := o.Provisioning.PublicStatus(ctx, creation)
	if e != nil {
		return nil, e
	}
	resp.ProvisioningReceiptID = creation.ReceiptID
	resp.ProvisioningState = status.State
	resp.ProvisioningPending = status.State != "direct_only" && status.State != "bound"
	return resp, nil
}
