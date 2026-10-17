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
	"context"

	"github.com/OpenIMSDK/tools/log"

	"github.com/OpenIMSDK/tools/errs"

	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/proto/admin"
)

var publicClientConfigKeys = []string{
	"discoverPageURL",
	"ordinaryUserAddFriend",
	"allowSendMsgNotFriend",
	"needInvitationCodeRegister",
	"needVerificationCodeRegister",
}

func (o *adminServer) GetClientConfig(ctx context.Context, req *admin.GetClientConfigReq) (*admin.GetClientConfigResp, error) {
	defer log.ZDebug(ctx, "return")
	conf, err := o.Database.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if mctx.IsInternalCall(ctx) {
		return &admin.GetClientConfigResp{Config: conf}, nil
	}
	if _, err := mctx.CheckAdmin(ctx); err == nil {
		return &admin.GetClientConfigResp{Config: conf}, nil
	}
	filtered := make(map[string]string, len(publicClientConfigKeys))
	for _, key := range publicClientConfigKeys {
		if val, ok := conf[key]; ok {
			filtered[key] = val
		}
	}
	return &admin.GetClientConfigResp{Config: filtered}, nil
}

func (o *adminServer) SetClientConfig(ctx context.Context, req *admin.SetClientConfigReq) (*admin.SetClientConfigResp, error) {
	defer log.ZDebug(ctx, "return")
	if _, err := mctx.CheckAdmin(ctx); err != nil {
		return nil, err
	}
	if len(req.Config) == 0 {
		return nil, errs.ErrArgs.Wrap("更新配置不能为空")
	}
	if value, ok := req.Config[database.ForbidNewMemberHistoryConfigKey]; ok && value != "true" && value != "false" {
		return nil, errs.ErrArgs.Wrap("禁止新成员查看历史消息开关只能为 true 或 false")
	}
	if err := o.Database.SetConfig(ctx, req.Config); err != nil {
		return nil, err
	}
	if _, ok := req.Config[database.ForbidNewMemberHistoryConfigKey]; ok {
		if err := o.Database.SyncNewMemberHistoryPolicy(ctx); err != nil {
			return nil, err
		}
	}
	return &admin.SetClientConfigResp{}, nil
}

func (o *adminServer) DelClientConfig(ctx context.Context, req *admin.DelClientConfigReq) (*admin.DelClientConfigResp, error) {
	defer log.ZDebug(ctx, "return")
	if _, err := mctx.CheckAdmin(ctx); err != nil {
		return nil, err
	}
	if err := o.Database.DelConfig(ctx, req.Keys); err != nil {
		return nil, err
	}
	for _, key := range req.Keys {
		if key == database.ForbidNewMemberHistoryConfigKey {
			if err := o.Database.SyncNewMemberHistoryPolicy(ctx); err != nil {
				return nil, err
			}
			break
		}
	}
	return &admin.DelClientConfigResp{}, nil
}
