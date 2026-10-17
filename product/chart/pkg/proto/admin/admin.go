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

package admin

import (
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/utils"
)

func (x *LoginReq) Check() error {
	if x.Account == "" {
		return errs.ErrArgs.Wrap("账号不能为空")
	}
	if x.Password == "" {
		return errs.ErrArgs.Wrap("密码不能为空")
	}
	return nil
}

func (x *ChangePasswordReq) Check() error {
	if x.Password == "" {
		return errs.ErrArgs.Wrap("密码不能为空")
	}
	return nil
}

func (x *AddDefaultFriendReq) Check() error {
	if x.UserIDs == nil {
		return errs.ErrArgs.Wrap("用户ID列表不能为空")
	}
	if utils.Duplicate(x.UserIDs) {
		return errs.ErrArgs.Wrap("用户ID列表存在重复")
	}
	return nil
}

func (x *DelDefaultFriendReq) Check() error {
	if x.UserIDs == nil {
		return errs.ErrArgs.Wrap("用户ID列表不能为空")
	}
	return nil
}

func (x *SearchDefaultFriendReq) Check() error {
	if x.Pagination == nil {
		return errs.ErrArgs.Wrap("分页参数不能为空")
	}
	if x.Pagination.PageNumber < 1 {
		return errs.ErrArgs.Wrap("页码无效")
	}
	if x.Pagination.ShowNumber < 1 {
		return errs.ErrArgs.Wrap("每页显示数量无效")
	}
	return nil
}

func (x *AddDefaultGroupReq) Check() error {
	if x.GroupIDs == nil {
		return errs.ErrArgs.Wrap("群组ID列表不能为空")
	}
	if utils.Duplicate(x.GroupIDs) {
		return errs.ErrArgs.Wrap("群组ID列表存在重复")
	}
	return nil
}

func (x *DelDefaultGroupReq) Check() error {
	if x.GroupIDs == nil {
		return errs.ErrArgs.Wrap("群组ID列表不能为空")
	}
	return nil
}

func (x *SearchDefaultGroupReq) Check() error {
	if x.Pagination == nil {
		return errs.ErrArgs.Wrap("分页参数不能为空")
	}
	if x.Pagination.PageNumber < 1 {
		return errs.ErrArgs.Wrap("页码无效")
	}
	if x.Pagination.ShowNumber < 1 {
		return errs.ErrArgs.Wrap("每页显示数量无效")
	}
	return nil
}

func (x *AddInvitationCodeReq) Check() error {
	if x.Codes == nil {
		return errs.ErrArgs.Wrap("邀请码列表无效")
	}
	return nil
}

func (x *GenInvitationCodeReq) Check() error {
	if x.Len < 1 {
		return errs.ErrArgs.Wrap("长度参数无效")
	}
	if x.Num < 1 {
		return errs.ErrArgs.Wrap("数量参数无效")
	}
	if x.Chars == "" {
		return errs.ErrArgs.Wrap("字符集参数无效")
	}
	return nil
}

func (x *FindInvitationCodeReq) Check() error {
	if x.Codes == nil {
		return errs.ErrArgs.Wrap("邀请码列表不能为空")
	}
	return nil
}

func (x *UseInvitationCodeReq) Check() error {
	if x.Code == "" {
		return errs.ErrArgs.Wrap("邀请码不能为空")
	}
	if x.UserID == "" {
		return errs.ErrArgs.Wrap("用户ID不能为空")
	}
	return nil
}

func (x *DelInvitationCodeReq) Check() error {
	if x.Codes == nil {
		return errs.ErrArgs.Wrap("邀请码列表不能为空")
	}
	return nil
}

func (x *SearchInvitationCodeReq) Check() error {
	if !utils.Contain(x.Status, constant.InvitationCodeUnused, constant.InvitationCodeUsed, constant.InvitationCodeAll) {
		return errs.ErrArgs.Wrap("状态参数无效")
	}
	if x.Pagination == nil {
		return errs.ErrArgs.Wrap("分页参数不能为空")
	}
	if x.Pagination.PageNumber < 1 {
		return errs.ErrArgs.Wrap("页码无效")
	}
	if x.Pagination.ShowNumber < 1 {
		return errs.ErrArgs.Wrap("每页显示数量无效")
	}
	return nil
}

func (x *SearchUserIPLimitLoginReq) Check() error {
	if x.Pagination == nil {
		return errs.ErrArgs.Wrap("分页参数不能为空")
	}
	if x.Pagination.PageNumber < 1 {
		return errs.ErrArgs.Wrap("页码无效")
	}
	if x.Pagination.ShowNumber < 1 {
		return errs.ErrArgs.Wrap("每页显示数量无效")
	}
	return nil
}

func (x *AddUserIPLimitLoginReq) Check() error {
	if x.Limits == nil {
		return errs.ErrArgs.Wrap("限制列表不能为空")
	}
	return nil
}

func (x *DelUserIPLimitLoginReq) Check() error {
	if x.Limits == nil {
		return errs.ErrArgs.Wrap("限制列表不能为空")
	}
	return nil
}

func (x *SearchIPForbiddenReq) Check() error {
	if x.Pagination == nil {
		return errs.ErrArgs.Wrap("分页参数不能为空")
	}
	if x.Pagination.PageNumber < 1 {
		return errs.ErrArgs.Wrap("页码无效")
	}
	if x.Pagination.ShowNumber < 1 {
		return errs.ErrArgs.Wrap("每页显示数量无效")
	}
	return nil
}

func (x *AddIPForbiddenReq) Check() error {
	if x.Forbiddens == nil {
		return errs.ErrArgs.Wrap("禁止列表不能为空")
	}
	return nil
}

func (x *DelIPForbiddenReq) Check() error {
	if x.Ips == nil {
		return errs.ErrArgs.Wrap("IP地址列表不能为空")
	}
	return nil
}

func (x *CheckRegisterForbiddenReq) Check() error {
	if x.Ip == "" {
		return errs.ErrArgs.Wrap("IP地址不能为空")
	}
	return nil
}

func (x *CheckLoginForbiddenReq) Check() error {
	if x.Ip == "" && x.UserID == "" {
		return errs.ErrArgs.Wrap("IP地址和用户ID不能同时为空")
	}
	return nil
}

func (x *CancellationUserReq) Check() error {
	if x.UserID == "" {
		return errs.ErrArgs.Wrap("用户ID不能为空")
	}
	return nil
}

func (x *BlockUserReq) Check() error {
	if x.UserID == "" {
		return errs.ErrArgs.Wrap("用户ID不能为空")
	}
	return nil
}

func (x *UnblockUserReq) Check() error {
	if x.UserIDs == nil {
		return errs.ErrArgs.Wrap("用户ID列表不能为空")
	}
	return nil
}

func (x *SearchBlockUserReq) Check() error {
	if x.Pagination == nil {
		return errs.ErrArgs.Wrap("分页参数不能为空")
	}
	if x.Pagination.PageNumber < 1 {
		return errs.ErrArgs.Wrap("页码无效")
	}
	if x.Pagination.ShowNumber < 1 {
		return errs.ErrArgs.Wrap("每页显示数量无效")
	}
	return nil
}

func (x *FindUserBlockInfoReq) Check() error {
	if x.UserIDs == nil {
		return errs.ErrArgs.Wrap("用户ID列表不能为空")
	}
	return nil
}

func (x *CreateTokenReq) Check() error {
	if x.UserID == "" {
		return errs.ErrArgs.Wrap("用户ID不能为空")
	}
	if x.UserType > constant.AdminUser || x.UserType < constant.NormalUser {
		return errs.ErrArgs.Wrap("用户类型无效")
	}
	return nil
}

func (x *ParseTokenReq) Check() error {
	if x.Token == "" {
		return errs.ErrArgs.Wrap("令牌不能为空")
	}
	return nil
}

func (x *AddAppletReq) Check() error {
	if x.Name == "" {
		return errs.ErrArgs.Wrap("名称不能为空")
	}
	if x.AppID == "" {
		return errs.ErrArgs.Wrap("应用ID不能为空")
	}
	if x.Icon == "" {
		return errs.ErrArgs.Wrap("图标不能为空")
	}
	if x.Url == "" {
		return errs.ErrArgs.Wrap("URL不能为空")
	}
	if x.Md5 == "" {
		return errs.ErrArgs.Wrap("MD5值不能为空")
	}
	if x.Size <= 0 {
		return errs.ErrArgs.Wrap("文件大小无效")
	}
	if x.Version == "" {
		return errs.ErrArgs.Wrap("版本号不能为空")
	}
	if x.Status < constant.StatusOnShelf || x.Status > constant.StatusUnShelf {
		return errs.ErrArgs.Wrap("状态值无效")
	}
	return nil
}

func (x *DelAppletReq) Check() error {
	if x.AppletIds == nil {
		return errs.ErrArgs.Wrap("小程序ID列表不能为空")
	}
	return nil
}

func (x *UpdateAppletReq) Check() error {
	if x.Id == "" {
		return errs.ErrArgs.Wrap("ID不能为空")
	}
	return nil
}

func (x *SearchAppletReq) Check() error {
	if x.Pagination == nil {
		return errs.ErrArgs.Wrap("分页参数不能为空")
	}
	if x.Pagination.PageNumber < 1 {
		return errs.ErrArgs.Wrap("页码无效")
	}
	if x.Pagination.ShowNumber < 1 {
		return errs.ErrArgs.Wrap("每页显示数量无效")
	}
	return nil
}

func (x *SetClientConfigReq) Check() error {
	if x.Config == nil {
		return errs.ErrArgs.Wrap("配置不能为空")
	}
	return nil
}

func (x *ChangeAdminPasswordReq) Check() error {
	if x.UserID == "" {
		return errs.ErrArgs.Wrap("用户ID不能为空")
	}
	if x.CurrentPassword == "" {
		return errs.ErrArgs.Wrap("当前密码不能为空")
	}
	if x.NewPassword == "" {
		return errs.ErrArgs.Wrap("新密码不能为空")
	}
	if x.CurrentPassword == x.NewPassword {
		return errs.ErrArgs.Wrap("新密码不能与当前密码相同")
	}
	return nil
}

func (x *AddAdminAccountReq) Check() error {
	if x.Account == "" {
		return errs.ErrArgs.Wrap("账号不能为空")
	}
	if x.Password == "" {
		return errs.ErrArgs.Wrap("密码不能为空")
	}
	return nil
}

func (x *DelAdminAccountReq) Check() error {
	if len(x.UserIDs) == 0 {
		return errs.ErrArgs.Wrap("用户ID列表不能为空")
	}
	return nil
}

func (x *SearchAdminAccountReq) Check() error {
	if x.Pagination.ShowNumber == 0 {
		return errs.ErrArgs.Wrap("每页显示数量不能为空")
	}
	if x.Pagination.PageNumber == 0 {
		return errs.ErrArgs.Wrap("页码不能为空")
	}
	return nil
}
