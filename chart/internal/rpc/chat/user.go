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
	"strings"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	chatmodel "github.com/OpenIMSDK/chat/pkg/common/db/model/chat"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/eerrs"
	"github.com/OpenIMSDK/chat/pkg/proto/chat"
	"github.com/OpenIMSDK/chat/pkg/proto/common"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
)

func (o *chatSvr) UpdateUserInfo(ctx context.Context, req *chat.UpdateUserInfoReq) (*chat.UpdateUserInfoResp, error) {
	defer log.ZDebug(ctx, "return")
	resp := &chat.UpdateUserInfoResp{}
	if err := req.Check(); err != nil {
		return nil, err
	}
	if o.Provisioning != nil {
		if err := o.Provisioning.RequireReady(ctx, req.UserID); err != nil {
			return nil, err
		}
	}
	opUserID, userType, err := mctx.Check(ctx)
	if err != nil {
		return nil, err
	}
	if req.UserID == "" {
		return nil, errs.ErrArgs.Wrap("用户ID不能为空")
	}
	switch userType {
	case constant.NormalUser:
		//if req.UserID == "" {
		//	req.UserID = opUserID
		//}
		if req.UserID != opUserID {
			return nil, errs.ErrNoPermission.Wrap("只有管理员可以修改其他用户信息")
		}
		if req.AreaCode != nil {
			return nil, errs.ErrNoPermission.Wrap("区号不能修改")
		}
		if req.PhoneNumber != nil {
			return nil, errs.ErrNoPermission.Wrap("手机号不能修改")
		}
		if req.Email != nil {
			return nil, errs.ErrNoPermission.Wrap("邮箱绑定需要独立验证，个人资料接口不可修改")
		}
		if req.Account != nil {
			return nil, errs.ErrNoPermission.Wrap("账号不能修改")
		}
		if req.Level != nil {
			return nil, errs.ErrNoPermission.Wrap("等级不能修改")
		}
		if req.RegisterType != nil {
			return nil, errs.ErrNoPermission.Wrap("注册类型不能修改")
		}
	case constant.AdminUser:
	default:
		return nil, errs.ErrNoPermission.Wrap("用户类型错误")
	}
	update, err := ToDBAttributeUpdate(req)
	if err != nil {
		return nil, err
	}
	attribute, err := o.Database.TakeAttributeByUserID(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if req.Account != nil && req.Account.Value != attribute.Account {
		_, err := o.Database.TakeAttributeByAccount(ctx, req.Account.Value)
		if err == nil {
			return nil, eerrs.ErrAccountAlreadyRegister.Wrap()
		} else if !o.Database.IsNotFound(err) {
			return nil, err
		}
	}
	if req.AreaCode != nil || req.PhoneNumber != nil {
		areaCode := attribute.AreaCode
		phoneNumber := attribute.PhoneNumber
		if req.AreaCode != nil {
			areaCode = req.AreaCode.Value
		}
		if req.PhoneNumber != nil {
			phoneNumber = req.PhoneNumber.Value
		}
		if attribute.AreaCode != areaCode || attribute.PhoneNumber != phoneNumber {
			_, err := o.Database.TakeAttributeByPhone(ctx, areaCode, phoneNumber)
			if err == nil {
				return nil, eerrs.ErrAccountAlreadyRegister.Wrap()
			} else if !o.Database.IsNotFound(err) {
				return nil, err
			}
		}
	}
	if err := o.Database.UpdateUseInfo(ctx, req.UserID, update); err != nil {
		return nil, err
	}
	current, err := o.Database.TakeAttributeByUserID(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	resp.NickName = current.Nickname
	resp.FaceUrl = current.FaceURL
	resp.SyncPending = req.Nickname != nil || req.FaceURL != nil
	if o.ProfileSync != nil {
		if req.Nickname != nil || req.FaceURL != nil {
			syncCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_ = o.ProfileSync.ProcessUser(syncCtx, req.UserID)
			cancel()
		}
		pending, err := o.ProfileSync.Pending(ctx, req.UserID)
		if err != nil {
			return nil, err
		}
		resp.SyncPending = pending
	}
	return resp, nil
}

func (o *chatSvr) FindUserPublicInfo(ctx context.Context, req *chat.FindUserPublicInfoReq) (*chat.FindUserPublicInfoResp, error) {
	defer log.ZDebug(ctx, "return")
	if len(req.UserIDs) == 0 {
		return nil, errs.ErrArgs.Wrap("用户ID列表不能为空")
	}
	attributes, err := o.Database.FindAttribute(ctx, req.UserIDs)
	if err != nil {
		return nil, err
	}
	return &chat.FindUserPublicInfoResp{
		Users: DbToPbAttributes(attributes),
	}, nil
}

func (o *chatSvr) AddUserAccount(ctx context.Context, req *chat.AddUserAccountReq) (*chat.AddUserAccountResp, error) {
	if _, err := mctx.CheckAdmin(ctx); err != nil {
		return nil, err
	}
	resp, err := o.RegisterUser(ctx, &chat.RegisterUserReq{Ip: req.Ip, DeviceID: req.DeviceID, Platform: req.Platform, User: req.User, AdminRequestID: req.AdminRequestID})
	if err != nil {
		return nil, err
	}
	return &chat.AddUserAccountResp{UserID: resp.UserID, AdminRequestID: resp.AdminRequestID, ProvisioningReceiptID: resp.ProvisioningReceiptID, ProvisioningState: resp.ProvisioningState, ProvisioningPending: resp.ProvisioningPending, Recovered: resp.Recovered}, nil
}

func (o *chatSvr) SearchUserPublicInfo(ctx context.Context, req *chat.SearchUserPublicInfoReq) (*chat.SearchUserPublicInfoResp, error) {
	defer log.ZDebug(ctx, "return")
	_, userType, err := mctx.Check(ctx)
	if err != nil {
		return nil, err
	}
	isAdmin := userType == constant.AdminUser
	if !isAdmin && strings.TrimSpace(req.Keyword) == "" {
		return nil, errs.ErrArgs.Wrap("搜索关键字不能为空")
	}
	if chatmodel.IsInvalidSearchKeyword(req.Keyword) {
		return nil, errs.ErrArgs.Wrap("搜索关键字无效")
	}
	if !isAdmin && !isValidPublicSearchUserID(req.Keyword) {
		return &chat.SearchUserPublicInfoResp{Total: 0, Users: nil}, nil
	}
	total, list, err := o.Database.Search(ctx, constant.FinDAllUser, req.Keyword, req.Genders, req.Pagination.PageNumber, req.Pagination.ShowNumber, isAdmin)
	if err != nil {
		return nil, err
	}
	return &chat.SearchUserPublicInfoResp{
		Total: total,
		Users: DbToPbAttributes(list),
	}, nil
}

func (o *chatSvr) FindUserFullInfo(ctx context.Context, req *chat.FindUserFullInfoReq) (*chat.FindUserFullInfoResp, error) {
	defer log.ZDebug(ctx, "return")
	opUserID, userType, err := mctx.Check(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.UserIDs) == 0 {
		return nil, errs.ErrArgs.Wrap("用户ID列表不能为空")
	}
	attributes, err := o.Database.FindAttribute(ctx, req.UserIDs)
	if err != nil {
		return nil, err
	}
	isAdmin := userType == constant.AdminUser
	users := DbToPbUserFullInfos(attributes)
	for _, u := range users {
		maskUserFullInfoPhone(isAdmin, opUserID, u)
	}
	return &chat.FindUserFullInfoResp{Users: users}, nil
}

func (o *chatSvr) SearchUserFullInfo(ctx context.Context, req *chat.SearchUserFullInfoReq) (*chat.SearchUserFullInfoResp, error) {
	opUserID, userType, err := mctx.Check(ctx)
	if err != nil {
		return nil, err
	}
	isAdmin := userType == constant.AdminUser
	if !isAdmin && strings.TrimSpace(req.Keyword) == "" {
		return nil, errs.ErrArgs.Wrap("搜索关键字不能为空")
	}
	if chatmodel.IsInvalidSearchKeyword(req.Keyword) {
		return nil, errs.ErrArgs.Wrap("搜索关键字无效")
	}
	if !isAdmin && !isValidPublicSearchUserID(req.Keyword) {
		return &chat.SearchUserFullInfoResp{Total: 0, Users: nil}, nil
	}
	total, list, err := o.Database.Search(ctx, req.Normal, req.Keyword, req.Genders, req.Pagination.PageNumber, req.Pagination.ShowNumber, isAdmin)
	if err != nil {
		return nil, err
	}
	users := DbToPbUserFullInfos(list)
	for _, u := range users {
		maskUserFullInfoPhone(isAdmin, opUserID, u)
	}
	return &chat.SearchUserFullInfoResp{
		Total: total,
		Users: users,
	}, nil
}

func (o *chatSvr) FindUserAccount(ctx context.Context, req *chat.FindUserAccountReq) (*chat.FindUserAccountResp, error) {
	defer log.ZDebug(ctx, "return")
	if len(req.UserIDs) == 0 {
		return nil, errs.ErrArgs.Wrap("用户ID列表必须设置")
	}
	if _, _, err := mctx.CheckAdminOrUser(ctx); err != nil {
		return nil, err
	}
	attributes, err := o.Database.FindAttribute(ctx, req.UserIDs)
	if err != nil {
		return nil, err
	}
	userAccountMap := make(map[string]string)
	for _, attribute := range attributes {
		userAccountMap[attribute.UserID] = attribute.Account
	}
	return &chat.FindUserAccountResp{UserAccountMap: userAccountMap}, nil
}

func (o *chatSvr) FindAccountUser(ctx context.Context, req *chat.FindAccountUserReq) (*chat.FindAccountUserResp, error) {
	defer log.ZDebug(ctx, "return")
	if len(req.Accounts) == 0 {
		return nil, errs.ErrArgs.Wrap("账号列表必须设置")
	}
	if _, _, err := mctx.CheckAdminOrUser(ctx); err != nil {
		return nil, err
	}
	attributes, err := o.Database.FindAttribute(ctx, req.Accounts)
	if err != nil {
		return nil, err
	}
	accountUserMap := make(map[string]string)
	for _, attribute := range attributes {
		accountUserMap[attribute.Account] = attribute.UserID
	}
	return &chat.FindAccountUserResp{AccountUserMap: accountUserMap}, nil
}

func (o *chatSvr) SearchUserInfo(ctx context.Context, req *chat.SearchUserInfoReq) (*chat.SearchUserInfoResp, error) {
	opUserID, userType, err := mctx.Check(ctx)
	if err != nil {
		return nil, err
	}
	if chatmodel.IsInvalidSearchKeyword(req.Keyword) {
		return nil, errs.ErrArgs.Wrap("搜索关键字无效")
	}
	total, list, err := o.Database.SearchUser(ctx, req.Keyword, req.UserIDs, req.Genders, req.Pagination.PageNumber, req.Pagination.ShowNumber)
	if err != nil {
		return nil, err
	}
	isAdmin := userType == constant.AdminUser
	users := make([]*common.UserFullInfo, 0, len(list))
	for _, attr := range list {
		info := DbToPbUserFullInfo(attr)
		maskUserFullInfoPhone(isAdmin, opUserID, info)
		users = append(users, info)
	}
	return &chat.SearchUserInfoResp{
		Total: total,
		Users: users,
	}, nil
}

func (o *chatSvr) checkTheUniqueness(ctx context.Context, req *chat.AddUserAccountReq) error {
	if req.User.PhoneNumber != "" {
		_, err := o.Database.TakeAttributeByPhone(ctx, req.User.AreaCode, req.User.PhoneNumber)
		if err == nil {
			return eerrs.ErrPhoneAlreadyRegister.Wrap()
		} else if !o.Database.IsNotFound(err) {
			return err
		}
	} else {
		_, err := o.Database.TakeAttributeByEmail(ctx, req.User.Email)
		if err == nil {
			return eerrs.ErrEmailAlreadyRegister.Wrap()
		} else if !o.Database.IsNotFound(err) {
			return err
		}
	}
	return nil
}
