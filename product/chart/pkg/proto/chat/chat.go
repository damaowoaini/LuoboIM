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
	"regexp"
	"strconv"

	"github.com/OpenIMSDK/protocol/wrapperspb"
	"github.com/OpenIMSDK/tools/utils"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	constant2 "github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
)

func (x *UpdateUserInfoReq) Check() error {
	if x.UserID == "" {
		return errs.ErrArgs.Wrap("用户ID不能为空")
	}
	if x.Email != nil && x.Email.Value != "" {
		if err := EmailCheck(x.Email.Value); err != nil {
			return err
		}
	}
	if x.Nickname != nil {
		value := x.Nickname.Value
		if strings.TrimSpace(value) == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 64 {
			return errs.ErrArgs.Wrap("昵称须为1至64个字符")
		}
		for _, r := range value {
			if unicode.IsControl(r) {
				return errs.ErrArgs.Wrap("昵称不可包含控制字符")
			}
		}
	}
	if x.FaceURL != nil && x.FaceURL.Value != "" {
		value := x.FaceURL.Value
		parsed, err := url.Parse(value)
		if err != nil || len(value) > 255 || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
			return errs.ErrArgs.Wrap("头像须为不含账号信息的HTTPS地址，最长255字节")
		}
	}
	if x.Gender != nil && (x.Gender.Value < 0 || x.Gender.Value > 2) {
		return errs.ErrArgs.Wrap("性别参数无效")
	}
	if x.Birth != nil && (x.Birth.Value < time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli() || x.Birth.Value > time.Now().UnixMilli()) {
		return errs.ErrArgs.Wrap("生日必须介于1900年与当前日期之间")
	}
	for _, value := range []*wrapperspb.Int32Value{x.AllowAddFriend, x.AllowBeep, x.AllowVibration} {
		if value != nil && value.Value != 1 && value.Value != 2 {
			return errs.ErrArgs.Wrap("允许开关须为1或2")
		}
	}
	if x.GlobalRecvMsgOpt != nil && (x.GlobalRecvMsgOpt.Value < 0 || x.GlobalRecvMsgOpt.Value > 2) {
		return errs.ErrArgs.Wrap("消息接收参数无效")
	}
	if x.Level != nil && x.Level.Value < 0 {
		return errs.ErrArgs.Wrap("等级不可为负数")
	}
	return nil
}

func (x *FindUserPublicInfoReq) Check() error {
	if x.UserIDs == nil {
		return errs.ErrArgs.Wrap("用户ID列表不能为空")
	}
	return nil
}

func (x *SearchUserPublicInfoReq) Check() error {
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

func (x *FindUserFullInfoReq) Check() error {
	if x.UserIDs == nil {
		return errs.ErrArgs.Wrap("用户ID列表不能为空")
	}
	return nil
}

func (x *SendVerifyCodeReq) Check() error {
	if x.UsedFor < constant.VerificationCodeForRegister || x.UsedFor > constant.VerificationCodeForLogin {
		return errs.ErrArgs.Wrap("用途字段不能为空")
	}
	if x.Email == "" {
		if x.AreaCode == "" {
			return errs.ErrArgs.Wrap("区号不能为空")
		} else if err := AreaCodeCheck(x.AreaCode); err != nil {
			return err
		}
		if x.PhoneNumber == "" {
			return errs.ErrArgs.Wrap("手机号不能为空")
		} else if err := PhoneNumberCheck(x.PhoneNumber); err != nil {
			return err
		}
	} else {
		if err := EmailCheck(x.Email); err != nil {
			return err
		}
	}

	return nil
}

func (x *VerifyCodeReq) Check() error {
	if x.Email == "" {
		if x.AreaCode == "" {
			return errs.ErrArgs.Wrap("区号不能为空")
		} else if err := AreaCodeCheck(x.AreaCode); err != nil {
			return err
		}
		if x.PhoneNumber == "" {
			return errs.ErrArgs.Wrap("手机号不能为空")
		} else if err := PhoneNumberCheck(x.PhoneNumber); err != nil {
			return err
		}
	} else {
		if err := EmailCheck(x.Email); err != nil {
			return err
		}
	}
	if x.VerifyCode == "" {
		return errs.ErrArgs.Wrap("验证码不能为空")
	}
	return nil
}

func (x *RegisterUserReq) Check() error {
	//if x.VerifyCode == "" {
	//	return errs.ErrArgs.Wrap("VerifyCode is empty")
	//}
	if x.Platform < constant2.IOSPlatformID || x.Platform > constant2.AdminPlatformID {
		return errs.ErrArgs.Wrap("平台参数无效")
	}
	if x.User == nil {
		return errs.ErrArgs.Wrap("用户信息不能为空")
	}
	if x.User.Email == "" {
		if x.User.AreaCode == "" {
			return errs.ErrArgs.Wrap("区号不能为空")
		} else if err := AreaCodeCheck(x.User.AreaCode); err != nil {
			return err
		}
		if x.User.PhoneNumber == "" {
			return errs.ErrArgs.Wrap("手机号不能为空")
		} else if err := PhoneNumberCheck(x.User.PhoneNumber); err != nil {
			return err
		}
	} else {
		if err := EmailCheck(x.User.Email); err != nil {
			return err
		}
	}
	return nil
}

func (x *LoginReq) Check() error {
	if x.Platform < constant2.IOSPlatformID || x.Platform > constant2.AdminPlatformID {
		return errs.ErrArgs.Wrap("平台参数无效")
	}
	if x.Account != "" {
		return nil
	}
	if x.Email == "" {
		if x.AreaCode == "" {
			return errs.ErrArgs.Wrap("区号不能为空")
		} else if err := AreaCodeCheck(x.AreaCode); err != nil {
			return err
		}
		if x.PhoneNumber == "" {
			return errs.ErrArgs.Wrap("手机号不能为空")
		} else if err := PhoneNumberCheck(x.PhoneNumber); err != nil {
			return err
		}
	} else {
		if err := EmailCheck(x.Email); err != nil {
			return err
		}
	}
	return nil
}

func (x *ResetPasswordReq) Check() error {
	if x.Password == "" {
		return errs.ErrArgs.Wrap("密码不能为空")
	}
	if x.Email == "" {
		if x.AreaCode == "" {
			return errs.ErrArgs.Wrap("区号不能为空")
		} else if err := AreaCodeCheck(x.AreaCode); err != nil {
			return err
		}
		if x.PhoneNumber == "" {
			return errs.ErrArgs.Wrap("手机号不能为空")
		} else if err := PhoneNumberCheck(x.PhoneNumber); err != nil {
			return err
		}
	} else {
		if err := EmailCheck(x.Email); err != nil {
			return err
		}
	}
	if x.VerifyCode == "" {
		return errs.ErrArgs.Wrap("验证码不能为空")
	}
	return nil
}

func (x *ChangePasswordReq) Check() error {
	if x.UserID == "" {
		return errs.ErrArgs.Wrap("用户ID不能为空")
	}

	// if x.CurrentPassword == "" {
	// 	return errs.ErrArgs.Wrap("currentPassword is empty")
	// }

	if x.NewPassword == "" {
		return errs.ErrArgs.Wrap("新密码不能为空")
	}

	return nil
}

func (x *FindUserAccountReq) Check() error {
	if x.UserIDs == nil {
		return errs.ErrArgs.Wrap("用户ID列表不能为空")
	}
	return nil
}

func (x *FindAccountUserReq) Check() error {
	if x.Accounts == nil {
		return errs.ErrArgs.Wrap("账号列表不能为空")
	}
	return nil
}

func (x *SearchUserFullInfoReq) Check() error {
	if x.Pagination == nil {
		return errs.ErrArgs.Wrap("分页参数不能为空")
	}
	if x.Pagination.PageNumber < 1 {
		return errs.ErrArgs.Wrap("页码无效")
	}
	if x.Pagination.ShowNumber < 1 {
		return errs.ErrArgs.Wrap("每页显示数量无效")
	}
	if x.Normal < constant.FinDAllUser || x.Normal > constant.FindNormalUser {
		return errs.ErrArgs.Wrap("查询类型参数无效")
	}
	return nil
}

func (x *DeleteLogsReq) Check() error {
	if x.LogIDs == nil {
		return errs.ErrArgs.Wrap("日志ID列表不能为空")
	}
	if utils.Duplicate(x.LogIDs) {
		return errs.ErrArgs.Wrap("日志ID存在重复")
	}
	return nil
}

func (x *UploadLogsReq) Check() error {
	if x.FileURLs == nil {
		return errs.ErrArgs.Wrap("文件URL列表不能为空")
	}
	if x.Platform < constant2.IOSPlatformID || x.Platform > constant2.AdminPlatformID {
		return errs.ErrArgs.Wrap("平台参数无效")
	}
	return nil
}

func (x *SearchLogsReq) Check() error {
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

func EmailCheck(email string) error {
	pattern := `^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`
	if err := regexMatch(pattern, email); err != nil {
		return errs.Wrap(err, "邮箱格式无效")
	}
	return nil
}

func AreaCodeCheck(areaCode string) error {
	//pattern := `\+[1-9][0-9]{1,2}`
	//if err := regexMatch(pattern, areaCode); err != nil {
	//	return errs.Wrap(err, "AreaCode is invalid")
	//}
	return nil
}

func PhoneNumberCheck(phoneNumber string) error {
	if phoneNumber == "" {
		return errs.ErrArgs.Wrap("手机号不能为空")
	}
	if len(phoneNumber) < 6 {
		return errs.ErrArgs.Wrap("手机号至少6位")
	}
	if len(phoneNumber) > 32 {
		return errs.ErrArgs.Wrap("手机号过长")
	}
	return nil
}

func regexMatch(pattern string, target string) error {
	reg := regexp.MustCompile(pattern)
	ok := reg.MatchString(target)
	if !ok {
		return errs.ErrArgs
	}
	return nil
}

func (x *SearchUserInfoReq) Check() error {
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

func (x *AddUserAccountReq) Check() error {
	if x.User == nil {
		return errs.ErrArgs.Wrap("用户信息不能为空")
	}

	if x.User.Email == "" {
		if x.User.AreaCode == "" || x.User.PhoneNumber == "" {
			return errs.ErrArgs.Wrap("区号或手机号不能为空")
		}
		if x.User.AreaCode[0] != '+' {
			x.User.AreaCode = "+" + x.User.AreaCode
		}
		if _, err := strconv.ParseUint(x.User.AreaCode[1:], 10, 64); err != nil {
			return errs.ErrArgs.Wrap("区号必须是数字")
		}
		if _, err := strconv.ParseUint(x.User.PhoneNumber, 10, 64); err != nil {
			return errs.ErrArgs.Wrap("手机号必须是数字")
		}
	} else {
		if err := EmailCheck(x.User.Email); err != nil {
			return errs.ErrArgs.Wrap("邮箱格式必须正确")
		}
	}

	return nil
}

func (x *GetUserLatestLoginRecordReq) Check() error {
	if x.UserID == "" {
		return errs.ErrArgs.Wrap("用户ID不能为空")
	}
	return nil
}

func (x *GetUserLoginRecordsReq) Check() error {
	if x.UserID == "" {
		return errs.ErrArgs.Wrap("用户ID不能为空")
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
