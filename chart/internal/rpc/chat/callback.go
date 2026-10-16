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
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"

	constant2 "github.com/OpenIMSDK/chat/pkg/common/constant"
	chat2 "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/eerrs"
	"github.com/OpenIMSDK/chat/pkg/proto/chat"
)

type CallbackBeforeAddFriendReq struct {
	CallbackCommand `json:"callbackCommand"`
	FromUserID      string `json:"fromUserID" `
	ToUserID        string `json:"toUserID"`
	ReqMsg          string `json:"reqMsg"`
	OperationID     string `json:"operationID"`
}

type CallbackCommand string

func (c CallbackCommand) GetCallbackCommand() string {
	return string(c)
}

func (o *chatSvr) OpenIMCallback(ctx context.Context, req *chat.OpenIMCallbackReq) (*chat.OpenIMCallbackResp, error) {
	defer log.ZDebug(ctx, "return")
	switch req.Command {
	case constant.CallbackBeforeAddFriendCommand:
		var data CallbackBeforeAddFriendReq
		if err := json.Unmarshal([]byte(req.Body), &data); err != nil {
			return nil, errs.Wrap(err)
		}
		user, err := o.Database.GetAttribute(ctx, data.ToUserID)
		if err != nil {
			return nil, err
		}
		if user.AllowAddFriend != constant2.OrdinaryUserAddFriendEnable {
			return nil, eerrs.ErrRefuseFriend.Wrap(fmt.Sprintf("拒绝添加好友状态 %d", user.AllowAddFriend))
		}

		// Check global configuration
		if err := o.checkAddFriendRestriction(ctx, data.FromUserID, user); err != nil {
			return nil, err
		}
		if err := o.checkDailyFriendApplyLimit(ctx, data.FromUserID, data.ToUserID); err != nil {
			return nil, err
		}
		return &chat.OpenIMCallbackResp{}, nil
	default:
		return nil, errs.ErrArgs.Wrap(fmt.Sprintf("无效的命令 %s", req.Command))
	}
}

// checkDailyFriendApplyLimit 限制每日向不同用户发起加好友申请的次数（不论对方是否同意）。
func (o *chatSvr) checkDailyFriendApplyLimit(ctx context.Context, fromUserID, toUserID string) error {
	if fromUserID == "" || toUserID == "" || o.FriendAddLimit == nil {
		return nil
	}
	clientConfig, err := o.Admin.GetConfig(ctx)
	if err != nil {
		log.ZError(ctx, "checkDailyFriendApplyLimit: 获取配置失败，降级放行", err)
		return nil
	}
	limitStr, ok := clientConfig[constant2.DailyAddFriendLimitConfigKey]
	if !ok || limitStr == "" || limitStr == "0" {
		return nil
	}
	limit, err := strconv.ParseInt(limitStr, 10, 64)
	if err != nil || limit <= 0 {
		return nil
	}
	limited, err := o.FriendAddLimit.AddDailyApplyTarget(ctx, fromUserID, toUserID, limit)
	if err != nil {
		log.ZError(ctx, "checkDailyFriendApplyLimit: Redis 记录失败，降级放行", err, "fromUserID", fromUserID, "toUserID", toUserID)
		return nil
	}
	if limited {
		return eerrs.ErrDailyAddFriendLimit.Wrap(fmt.Sprintf("今日加好友申请人数已达上限（%d人/天）", limit))
	}
	log.ZInfo(ctx, "daily friend apply recorded", "fromUserID", fromUserID, "toUserID", toUserID, "limit", limit)
	return nil
}

func (o *chatSvr) checkAddFriendRestriction(ctx context.Context, fromUserID string, toUser *chat2.Attribute) error {
	clientConfig, err := o.Admin.GetConfig(ctx)
	if err != nil {
		// 获取配置失败时是继续还是报错？
		// 为了业务连续性，这里选择降级处理（Fail Open），仅记录错误日志但不阻止操作。
		// 如果因为 Admin 服务异常导致无法获取配置，不应影响正常加好友功能。
		return nil
	}

	if allow, ok := clientConfig["allow_add_friend"]; ok && allow == "false" {
		// 获取发起者信息
		sender, err := o.Database.GetAttribute(ctx, fromUserID)
		if err != nil {
			// 如果用户不存在（例如系统用户或仅 IM 用户），降级处理，允许操作继续
			// 不阻止加好友，避免影响正常业务
			return nil
		}

		// 只要一方是高级用户（Level > 1），就允许添加
		// Check if sender or receiver is advanced user
		isPrivileged := false
		if sender.Level > constant2.OrdinaryUserLevel || toUser.Level > constant2.OrdinaryUserLevel {
			isPrivileged = true
		}

		if !isPrivileged {
			return eerrs.ErrRefuseFriend.Wrap("当前系统禁止普通用户添加好友")
		}
	}
	return nil
}
